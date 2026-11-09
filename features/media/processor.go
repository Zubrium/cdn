package media

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	_ "golang.org/x/image/webp"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type ProcessedOutput struct {
	Data     []byte
	MIMEType string
	Width    int
	Height   int
}

type RenditionProcessor interface {
	Process(context.Context, Media, FieldProfile, []byte) (map[string]ProcessedOutput, error)
	Ready(context.Context) error
}

// ProcessedFile is a ready rendition on disk; callers stream it to storage
// and then invoke the returned cleanup function.
type ProcessedFile struct {
	Path          string
	Size          int64
	MIMEType      string
	Width, Height int
}
type StreamingRenditionProcessor interface {
	ProcessStream(context.Context, Media, FieldProfile, io.Reader) (map[string]ProcessedFile, func(), error)
}
type FFmpegProcessor struct {
	ffmpeg  string
	ffprobe string
	tempDir string
	timeout time.Duration
}

func (p *FFmpegProcessor) ProcessingTimeout() time.Duration { return p.timeout }

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(buffer)
}

func NewFFmpegProcessor(timeout time.Duration) (*FFmpegProcessor, error) {
	if timeout <= 0 {
		return nil, fmt.Errorf("media processing timeout must be explicitly configured")
	}
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		return nil, fmt.Errorf("ffmpeg executable not found: %w", err)
	}
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		return nil, fmt.Errorf("ffprobe executable not found: %w", err)
	}
	return &FFmpegProcessor{ffmpeg: ffmpeg, ffprobe: ffprobe, timeout: timeout}, nil
}

// NewFFmpegProcessorWithSettings is the production constructor: the
// temporary storage directory and processing timeout are operator configured.
func NewFFmpegProcessorWithSettings(tempDir string, timeout time.Duration) (*FFmpegProcessor, error) {
	if tempDir == "" || timeout <= 0 {
		return nil, fmt.Errorf("media processing directory and positive timeout are required")
	}
	if err := os.MkdirAll(tempDir, 0700); err != nil {
		return nil, err
	}
	p, err := NewFFmpegProcessor(timeout)
	if err != nil {
		return nil, err
	}
	p.tempDir = tempDir
	return p, nil
}

func (p *FFmpegProcessor) Ready(_ context.Context) error {
	if _, err := os.Stat(p.ffmpeg); err != nil {
		return fmt.Errorf("ffmpeg is unavailable: %w", err)
	}
	if _, err := os.Stat(p.ffprobe); err != nil {
		return fmt.Errorf("ffprobe is unavailable: %w", err)
	}
	return nil
}

// Process preserves the legacy in-memory processor contract for callers that
// already supply small in-memory fixtures; production uses ProcessStream.
func (p *FFmpegProcessor) Process(ctx context.Context, original Media, profile FieldProfile, data []byte) (map[string]ProcessedOutput, error) {
	original.SizeBytes = int64(len(data))
	files, cleanup, err := p.ProcessStream(ctx, original, profile, bytes.NewReader(data))
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		return nil, err
	}
	outputs := make(map[string]ProcessedOutput, len(files))
	for name, file := range files {
		data, err := os.ReadFile(file.Path)
		if err != nil {
			return nil, err
		}
		outputs[name] = ProcessedOutput{Data: data, MIMEType: file.MIMEType, Width: file.Width, Height: file.Height}
	}
	return outputs, nil
}

func (p *FFmpegProcessor) ProcessStream(ctx context.Context, original Media, profile FieldProfile, source io.Reader) (map[string]ProcessedFile, func(), error) {
	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	if err := profile.Validate(); err != nil {
		return nil, nil, err
	}
	if err := validateProfileType(profile, original.ContentType); err != nil {
		return nil, nil, err
	}
	if profile.Accept == "raster-image" && (original.ContentType == "image/gif" || original.ContentType == "image/svg+xml") {
		return map[string]ProcessedFile{}, nil, nil
	}
	if original.SizeBytes < 1 {
		return nil, nil, fmt.Errorf("invalid original size")
	}
	workDir, err := os.MkdirTemp(p.tempDir, "website-engine-media-*")
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() { _ = os.RemoveAll(workDir) }
	fail := func(err error) (map[string]ProcessedFile, func(), error) { cleanup(); return nil, nil, err }
	inputPath := filepath.Join(workDir, "original.media")
	input, err := os.OpenFile(inputPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return fail(err)
	}
	n, copyErr := io.Copy(input, io.LimitReader(contextReader{ctx: ctx, reader: source}, original.SizeBytes+1))
	closeErr := input.Close()
	if copyErr != nil {
		return fail(copyErr)
	}
	if closeErr != nil {
		return fail(closeErr)
	}
	if n != original.SizeBytes {
		return fail(fmt.Errorf("original size does not match its metadata"))
	}
	if original.ContentType == "image/webp" {
		animated, err := isAnimatedWebPFile(inputPath)
		if err != nil {
			return fail(err)
		}
		if animated {
			return map[string]ProcessedFile{}, cleanup, nil
		}
	}
	if profile.Accept == "video" {
		outputs, err := p.processVideoFiles(ctx, workDir, inputPath, profile)
		if err != nil {
			return fail(err)
		}
		return outputs, cleanup, nil
	}
	input, err = os.Open(inputPath)
	if err != nil {
		return fail(err)
	}
	config, _, decodeErr := image.DecodeConfig(input)
	closeErr = input.Close()
	if decodeErr != nil {
		return fail(fmt.Errorf("read source image dimensions: %w", decodeErr))
	}
	if closeErr != nil {
		return fail(closeErr)
	}
	if config.Width < 1 || config.Height < 1 || int64(config.Width)*int64(config.Height) > maxImagePixels {
		return fail(fmt.Errorf("source image dimensions are not supported"))
	}
	outputs := make(map[string]ProcessedFile, len(profile.Renditions))
	for _, rendition := range profile.Renditions {
		outputPath := filepath.Join(workDir, rendition.Name+".webp")
		filter, err := renditionFilter(rendition, config.Width, config.Height)
		if err != nil {
			return fail(fmt.Errorf("build image rendition %q filter: %w", rendition.Name, err))
		}
		if _, err := runCommand(ctx, p.ffmpeg, "-hide_banner", "-loglevel", "error", "-y", "-protocol_whitelist", "file,pipe", "-i", inputPath, "-map_metadata", "-1", "-frames:v", "1", "-vf", filter, "-c:v", "libwebp", "-quality", "82", "-compression_level", "6", outputPath); err != nil {
			return fail(fmt.Errorf("encode image rendition %q: %w", rendition.Name, err))
		}
		output, err := inspectImageFile(outputPath)
		if err != nil {
			return fail(fmt.Errorf("read image rendition %q: %w", rendition.Name, err))
		}
		outputs[rendition.Name] = output
	}
	return outputs, cleanup, nil
}

// Scan WebP chunks without loading the original or any large payload into RAM.
func isAnimatedWebPFile(path string) (bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer file.Close()
	var header [12]byte
	if _, err := io.ReadFull(file, header[:]); err != nil {
		return false, err
	}
	if string(header[:4]) != "RIFF" || string(header[8:]) != "WEBP" {
		return false, nil
	}
	stat, err := file.Stat()
	if err != nil {
		return false, err
	}
	offset := int64(12)
	for offset+8 <= stat.Size() {
		var chunk [8]byte
		if _, err := file.ReadAt(chunk[:], offset); err != nil {
			return false, err
		}
		size := int64(binary.LittleEndian.Uint32(chunk[4:]))
		start := offset + 8
		if size > stat.Size()-start {
			return false, fmt.Errorf("truncated WebP chunk")
		}
		kind := string(chunk[:4])
		if kind == "ANIM" || kind == "ANMF" {
			return true, nil
		}
		if kind == "VP8X" && size > 0 {
			var flag [1]byte
			if _, err := file.ReadAt(flag[:], start); err != nil {
				return false, err
			}
			if flag[0]&0x02 != 0 {
				return true, nil
			}
		}
		offset = start + size + (size & 1)
	}
	return false, nil
}
func inspectImageFile(path string) (ProcessedFile, error) {
	f, err := os.Open(path)
	if err != nil {
		return ProcessedFile{}, err
	}
	defer f.Close()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return ProcessedFile{}, err
	}
	stat, err := f.Stat()
	if err != nil {
		return ProcessedFile{}, err
	}
	return ProcessedFile{Path: path, Size: stat.Size(), MIMEType: "image/webp", Width: cfg.Width, Height: cfg.Height}, nil
}

func isAnimatedWebP(data []byte) bool {
	if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WEBP" {
		return false
	}
	for offset := 12; offset+8 <= len(data); {
		chunk := string(data[offset : offset+4])
		size := int(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
		start := offset + 8
		end := start + size
		if end > len(data) {
			return false
		}
		if chunk == "ANIM" || chunk == "ANMF" {
			return true
		}
		if chunk == "VP8X" && size > 0 && data[start]&0x02 != 0 {
			return true
		}
		offset = end + (size & 1)
	}
	return false
}

func (p *FFmpegProcessor) processVideoFiles(ctx context.Context, workDir, inputPath string, profile FieldProfile) (map[string]ProcessedFile, error) {
	v := profile.Video
	videoPath := filepath.Join(workDir, "video.mp4")
	videoFilter := fmt.Sprintf("scale=w='min(iw,%d)':h='min(ih,%d)':force_original_aspect_ratio=decrease:force_divisible_by=2", v.MaxWidth, v.MaxHeight)
	if _, err := runCommand(ctx, p.ffmpeg, "-hide_banner", "-loglevel", "error", "-y", "-protocol_whitelist", "file,pipe", "-i", inputPath, "-map", "0:v:0", "-map", "0:a?", "-map_metadata", "-1", "-vf", videoFilter, "-c:v", "libx264", "-preset", "medium", "-crf", "23", "-pix_fmt", "yuv420p", "-c:a", "aac", "-b:a", "128k", "-movflags", "+faststart", videoPath); err != nil {
		return nil, fmt.Errorf("encode H.264 video: %w", err)
	}
	width, height, err := p.probeDimensions(ctx, videoPath)
	if err != nil {
		return nil, fmt.Errorf("probe encoded video: %w", err)
	}
	stat, err := os.Stat(videoPath)
	if err != nil {
		return nil, err
	}
	posterPath := filepath.Join(workDir, "poster.webp")
	posterWidth, posterHeight, err := p.probeDimensions(ctx, inputPath)
	if err != nil {
		return nil, fmt.Errorf("probe source video for poster: %w", err)
	}
	posterFilter, err := renditionFilter(v.Poster, posterWidth, posterHeight)
	if err != nil {
		return nil, err
	}
	if _, err := runCommand(ctx, p.ffmpeg, "-hide_banner", "-loglevel", "error", "-y", "-protocol_whitelist", "file,pipe", "-i", inputPath, "-map_metadata", "-1", "-vf", posterFilter, "-frames:v", "1", "-c:v", "libwebp", "-quality", "82", "-compression_level", "6", posterPath); err != nil {
		return nil, fmt.Errorf("encode video poster: %w", err)
	}
	poster, err := inspectImageFile(posterPath)
	if err != nil {
		return nil, err
	}
	return map[string]ProcessedFile{"video": {Path: videoPath, Size: stat.Size(), MIMEType: "video/mp4", Width: width, Height: height}, "poster": poster}, nil
}

func renditionFilter(r Rendition, sourceWidth, sourceHeight int) (string, error) {
	if r.Fit == "cover" {
		if sourceWidth < 1 || sourceHeight < 1 {
			return "", fmt.Errorf("source dimensions must be positive, got %dx%d", sourceWidth, sourceHeight)
		}
		cropWidth, cropHeight := coverCropDimensions(sourceWidth, sourceHeight, r.Width, r.Height)
		return fmt.Sprintf("crop=w=%d:h=%d:x='(iw-ow)/2':y='(ih-oh)/2',scale=w='min(iw,%d)':h='min(ih,%d)':force_original_aspect_ratio=decrease",
			cropWidth, cropHeight, r.Width, r.Height), nil
	}
	return fmt.Sprintf("scale=w='min(iw,%d)':h='min(ih,%d)':force_original_aspect_ratio=decrease", r.Width, r.Height), nil
}

// coverCropDimensions calculates a positive, center-crop rectangle before it
// reaches FFmpeg. Passing dynamic crop expressions for both dimensions can be
// evaluated as a zero-height crop by FFmpeg 6 on some source/profile pairs.
func coverCropDimensions(sourceWidth, sourceHeight, targetWidth, targetHeight int) (int, int) {
	sourceRatio := float64(sourceWidth) / float64(sourceHeight)
	targetRatio := float64(targetWidth) / float64(targetHeight)
	if sourceRatio > targetRatio {
		return max(1, int(math.Floor(float64(sourceHeight)*targetRatio))), sourceHeight
	}
	return sourceWidth, max(1, int(math.Floor(float64(sourceWidth)/targetRatio)))
}

func (p *FFmpegProcessor) probeDimensions(ctx context.Context, path string) (int, int, error) {
	out, err := runCommand(ctx, p.ffprobe, "-v", "error", "-protocol_whitelist", "file,pipe", "-select_streams", "v:0", "-show_entries", "stream=width,height", "-of", "csv=s=x:p=0", path)
	if err != nil {
		return 0, 0, err
	}
	parts := strings.Split(strings.TrimSpace(string(out)), "x")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("unexpected ffprobe dimensions %q", strings.TrimSpace(string(out)))
	}
	width, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, err
	}
	height, err := strconv.Atoi(parts[1])
	return width, height, err
}

func runCommand(ctx context.Context, executable string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, executable, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		message := strings.TrimSpace(stderr.String())
		if message != "" {
			return nil, fmt.Errorf("%w: %s", err, message)
		}
		return nil, err
	}
	return out, nil
}

func readImageOutput(path string) (ProcessedOutput, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return ProcessedOutput{}, err
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return ProcessedOutput{}, err
	}
	return ProcessedOutput{Data: data, MIMEType: "image/webp", Width: config.Width, Height: config.Height}, nil
}

func validateProfileType(profile FieldProfile, contentType string) error {
	switch profile.Accept {
	case "image":
		if !strings.HasPrefix(contentType, "image/") {
			return &ValidationError{Message: "Для поля требуется изображение"}
		}
	case "raster-image":
		switch contentType {
		case "image/jpeg", "image/png", "image/webp", "image/gif", "image/svg+xml":
		default:
			return &ValidationError{Message: "Для изображения поддерживаются JPEG, PNG, WebP, GIF и SVG"}
		}
	case "video":
		if !strings.HasPrefix(contentType, "video/") {
			return &ValidationError{Message: "Для поля требуется видео"}
		}
	case "file":
		return nil
	}
	return nil
}
