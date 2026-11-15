package media

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
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
	ffmpeg         string
	ffprobe        string
	tempDir        string
	timeout        time.Duration
	maxOutputBytes int64
}

const maxCommandDiagnosticBytes = 64 * 1024
const defaultMaxProcessedOutputBytes = 5 << 30
const maxVideoDuration = 10 * time.Minute
const maxVideoDimension = 8192
const maxVideoPixels int64 = 33_554_432
const maxVideoFrameRate = 60
const maxVideoPixelFrames int64 = 40_000_000_000
const maxVideoPosterBytes int64 = 32 << 20

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
	return &FFmpegProcessor{ffmpeg: ffmpeg, ffprobe: ffprobe, timeout: timeout, maxOutputBytes: defaultMaxProcessedOutputBytes}, nil
}

// NewFFmpegProcessorWithSettings is the production constructor: the
// temporary storage directory and processing timeout are operator configured.
func NewFFmpegProcessorWithSettings(tempDir string, timeout time.Duration, maxOutputBytes int64) (*FFmpegProcessor, error) {
	if tempDir == "" || timeout <= 0 || maxOutputBytes <= 0 {
		return nil, fmt.Errorf("media processing directory, positive timeout, and output byte limit are required")
	}
	if err := os.MkdirAll(tempDir, 0700); err != nil {
		return nil, err
	}
	p, err := NewFFmpegProcessor(timeout)
	if err != nil {
		return nil, err
	}
	p.tempDir = tempDir
	p.maxOutputBytes = maxOutputBytes
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
		if err := p.validateOutputBudget(outputs); err != nil {
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
	if err := p.validateOutputBudget(outputs); err != nil {
		return fail(err)
	}
	return outputs, cleanup, nil
}

func (p *FFmpegProcessor) validateOutputBudget(outputs map[string]ProcessedFile) error {
	limit := p.maxOutputBytes
	if limit <= 0 {
		limit = defaultMaxProcessedOutputBytes
	}
	var total int64
	for _, output := range outputs {
		if output.Size < 0 || total > limit-output.Size {
			return fmt.Errorf("processed media exceeds the configured %d-byte output budget", limit)
		}
		total += output.Size
	}
	return nil
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
	probe, err := p.probeVideo(ctx, inputPath)
	if err != nil {
		return nil, fmt.Errorf("probe source video complexity: %w", err)
	}
	if err := validateVideoProbe(probe); err != nil {
		return nil, err
	}
	videoPath := filepath.Join(workDir, "video.mp4")
	videoFilter := fmt.Sprintf("scale=w='min(iw,%d)':h='min(ih,%d)':force_original_aspect_ratio=decrease:force_divisible_by=2", v.MaxWidth, v.MaxHeight)
	videoCap, posterCap := p.videoOutputBudgets()
	if _, err := runCommand(ctx, p.ffmpeg, "-hide_banner", "-loglevel", "error", "-y", "-protocol_whitelist", "file,pipe", "-threads", "2", "-filter_threads", "1", "-i", inputPath, "-map", "0:v:0", "-map", "0:a:0?", "-map_metadata", "-1", "-vf", videoFilter, "-c:v", "libx264", "-threads", "2", "-preset", "medium", "-crf", "23", "-pix_fmt", "yuv420p", "-c:a", "aac", "-b:a", "128k", "-movflags", "+faststart", "-fs", strconv.FormatInt(videoCap, 10), videoPath); err != nil {
		return nil, fmt.Errorf("encode H.264 video: %w", err)
	}
	videoStat, err := os.Stat(videoPath)
	if err != nil {
		return nil, err
	}
	if videoStat.Size() >= videoCap {
		return nil, fmt.Errorf("encoded video reached its %d-byte output cap", videoCap)
	}
	width, height, err := p.probeDimensions(ctx, videoPath)
	if err != nil {
		return nil, fmt.Errorf("probe encoded video: %w", err)
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
	if _, err := runCommand(ctx, p.ffmpeg, "-hide_banner", "-loglevel", "error", "-y", "-protocol_whitelist", "file,pipe", "-threads", "2", "-filter_threads", "1", "-i", inputPath, "-map_metadata", "-1", "-vf", posterFilter, "-frames:v", "1", "-c:v", "libwebp", "-quality", "82", "-compression_level", "6", "-fs", strconv.FormatInt(posterCap, 10), posterPath); err != nil {
		return nil, fmt.Errorf("encode video poster: %w", err)
	}
	posterStat, err := os.Stat(posterPath)
	if err != nil {
		return nil, err
	}
	if posterStat.Size() >= posterCap {
		return nil, fmt.Errorf("encoded video poster reached its %d-byte output cap", posterCap)
	}
	poster, err := inspectImageFile(posterPath)
	if err != nil {
		return nil, err
	}
	return map[string]ProcessedFile{"video": {Path: videoPath, Size: videoStat.Size(), MIMEType: "video/mp4", Width: width, Height: height}, "poster": poster}, nil
}

func (p *FFmpegProcessor) videoOutputBudgets() (video, poster int64) {
	limit := p.maxOutputBytes
	if limit <= 0 {
		limit = defaultMaxProcessedOutputBytes
	}
	poster = min(maxVideoPosterBytes, limit/2)
	if poster < 1 {
		poster = 1
	}
	video = limit - poster
	if video < 1 {
		video = 1
	}
	return video, poster
}

func validateVideoDuration(duration time.Duration) error {
	if duration <= 0 || duration > maxVideoDuration {
		return fmt.Errorf("video duration must be positive and no longer than %s", maxVideoDuration)
	}
	return nil
}

type videoProbe struct {
	Width      int
	Height     int
	FrameRate  float64
	FrameCount int64
	Duration   time.Duration
}

type ffprobeVideoMetadata struct {
	Streams []struct {
		Width       int    `json:"width"`
		Height      int    `json:"height"`
		AverageRate string `json:"avg_frame_rate"`
		RealRate    string `json:"r_frame_rate"`
		FrameCount  string `json:"nb_frames"`
	} `json:"streams"`
	Format struct {
		Duration string `json:"duration"`
	} `json:"format"`
}

func validateVideoProbe(probe videoProbe) error {
	if err := validateVideoDuration(probe.Duration); err != nil {
		return err
	}
	if probe.Width < 1 || probe.Height < 1 || probe.Width > maxVideoDimension || probe.Height > maxVideoDimension {
		return fmt.Errorf("source video dimensions must be between 1 and %d pixels", maxVideoDimension)
	}
	pixels := int64(probe.Width) * int64(probe.Height)
	if pixels > maxVideoPixels {
		return fmt.Errorf("source video exceeds the %d-pixel frame budget", maxVideoPixels)
	}
	if math.IsNaN(probe.FrameRate) || math.IsInf(probe.FrameRate, 0) || probe.FrameRate <= 0 || probe.FrameRate > maxVideoFrameRate {
		return fmt.Errorf("source video frame rate must be positive and no greater than %d fps", maxVideoFrameRate)
	}
	frames := int64(math.Ceil(probe.FrameRate * probe.Duration.Seconds()))
	if probe.FrameCount > frames {
		frames = probe.FrameCount
	}
	if frames < 1 || frames > maxVideoPixelFrames/pixels {
		return fmt.Errorf("source video exceeds the %d pixel-frame processing budget", maxVideoPixelFrames)
	}
	return nil
}

func parseVideoFrameRate(raw string) (float64, bool) {
	parts := strings.Split(strings.TrimSpace(raw), "/")
	if len(parts) > 2 || len(parts) == 0 {
		return 0, false
	}
	numerator, err := strconv.ParseFloat(parts[0], 64)
	if err != nil || math.IsNaN(numerator) || math.IsInf(numerator, 0) || numerator <= 0 {
		return 0, false
	}
	if len(parts) == 2 {
		denominator, err := strconv.ParseFloat(parts[1], 64)
		if err != nil || math.IsNaN(denominator) || math.IsInf(denominator, 0) || denominator <= 0 {
			return 0, false
		}
		numerator /= denominator
	}
	if math.IsNaN(numerator) || math.IsInf(numerator, 0) || numerator <= 0 {
		return 0, false
	}
	return numerator, true
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

func (p *FFmpegProcessor) probeVideo(ctx context.Context, path string) (videoProbe, error) {
	out, err := runCommand(ctx, p.ffprobe, "-v", "error", "-protocol_whitelist", "file,pipe", "-probesize", "16777216", "-analyzeduration", "5000000", "-select_streams", "v:0", "-show_entries", "stream=width,height,avg_frame_rate,r_frame_rate,nb_frames:format=duration", "-show_streams", "-show_format", "-of", "json", path)
	if err != nil {
		return videoProbe{}, err
	}
	var metadata ffprobeVideoMetadata
	if err := json.Unmarshal(out, &metadata); err != nil {
		return videoProbe{}, fmt.Errorf("invalid ffprobe response")
	}
	if len(metadata.Streams) != 1 {
		return videoProbe{}, fmt.Errorf("source video stream is unavailable")
	}
	seconds, err := strconv.ParseFloat(strings.TrimSpace(metadata.Format.Duration), 64)
	if err != nil || math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds <= 0 || seconds > float64(maxVideoDuration)/float64(time.Second) {
		return videoProbe{}, fmt.Errorf("unsupported source video duration")
	}
	stream := metadata.Streams[0]
	averageRate, averageOK := parseVideoFrameRate(stream.AverageRate)
	realRate, realOK := parseVideoFrameRate(stream.RealRate)
	frameRate := 0.0
	if averageOK {
		frameRate = averageRate
	} else if realOK {
		frameRate = realRate
	}
	var frameCount int64
	if raw := strings.TrimSpace(stream.FrameCount); raw != "" && raw != "N/A" {
		frameCount, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || frameCount < 0 {
			return videoProbe{}, fmt.Errorf("invalid source video frame count")
		}
	}
	return videoProbe{Width: stream.Width, Height: stream.Height, FrameRate: frameRate, FrameCount: frameCount, Duration: time.Duration(seconds * float64(time.Second))}, nil
}

func runCommand(ctx context.Context, executable string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, executable, args...)
	var stdout cappedBuffer
	var stderr cappedBuffer
	stdout.remaining = 4 * 1024 * 1024
	stderr.remaining = maxCommandDiagnosticBytes
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		message := strings.TrimSpace(stderr.String())
		if message != "" {
			return nil, fmt.Errorf("%w: %s", err, message)
		}
		return nil, err
	}
	if stdout.truncated {
		return nil, errors.New("media command output exceeded the configured diagnostic limit")
	}
	return stdout.buffer.Bytes(), nil
}

type cappedBuffer struct {
	buffer    bytes.Buffer
	remaining int
	truncated bool
}

func (b *cappedBuffer) Write(data []byte) (int, error) {
	original := len(data)
	keep := 0
	if b.remaining > 0 {
		keep = min(len(data), b.remaining)
		_, _ = b.buffer.Write(data[:keep])
		b.remaining -= keep
	}
	if keep < original {
		b.truncated = true
	}
	return original, nil
}

func (b *cappedBuffer) String() string {
	value := b.buffer.String()
	if b.truncated {
		value += "\n[diagnostic truncated]"
	}
	return value
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
