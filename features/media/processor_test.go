package media

import (
	"bytes"
	"context"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAnimatedWebPDetection(t *testing.T) {
	data := []byte("RIFF\x00\x00\x00\x00WEBPVP8X\x0a\x00\x00\x00\x02\x00\x00\x00\x00\x00\x00\x00\x00\x00")
	if !isAnimatedWebP(data) {
		t.Fatal("VP8X animation flag was not detected")
	}
	if isAnimatedWebP([]byte("RIFF\x00\x00\x00\x00WEBP")) {
		t.Fatal("static or incomplete WebP was reported as animated")
	}
	anim := append([]byte("RIFF\x00\x00\x00\x00WEBP"), []byte("ANIM\x00\x00\x00\x00")...)
	if !isAnimatedWebP(anim) {
		t.Fatal("ANIM chunk was not detected")
	}
}

func TestRenditionWorkerRequiresExplicitTiming(t *testing.T) {
	service := &Service{}
	if err := service.SetRenditionWorkerConfig(0, time.Second); err == nil {
		t.Fatal("zero lease accepted")
	}
	if err := service.SetRenditionWorkerConfig(time.Minute, 0); err == nil {
		t.Fatal("zero polling interval accepted")
	}
	if err := service.SetRenditionWorkerConfig(20*time.Minute, time.Second); err != nil {
		t.Fatal(err)
	}
}

func TestCoverRenditionFilterUsesPositivePixelDimensions(t *testing.T) {
	filter, err := renditionFilter(Rendition{Name: "small", Width: 640, Height: 400, Fit: "cover"}, 1264, 848)
	if err != nil {
		t.Fatal(err)
	}
	want := "crop=w=1264:h=790:x='(iw-ow)/2':y='(ih-oh)/2',scale=w='min(iw,640)':h='min(ih,400)':force_original_aspect_ratio=decrease"
	if filter != want {
		t.Fatalf("cover filter = %q, want %q", filter, want)
	}

	if _, err := renditionFilter(Rendition{Name: "small", Width: 640, Height: 400, Fit: "cover"}, 0, 848); err == nil {
		t.Fatal("cover filter accepted a zero-width source")
	}
}

func TestFFmpegProcessorCreatesWebPWithoutUpscaling(t *testing.T) {
	processor, err := NewFFmpegProcessor(15 * time.Minute)
	if err != nil {
		t.Skipf("ffmpeg unavailable: %v", err)
	}
	requireWebPEncoder(t, processor)
	input := pngFixture(t, 320, 240)
	profile := FieldProfile{Accept: "raster-image", Renditions: []Rendition{
		{Name: "small", Width: 160, Height: 160, Fit: "contain"},
		{Name: "large", Width: 640, Height: 480, Fit: "contain"},
		{Name: "square", Width: 160, Height: 160, Fit: "cover"},
	}}
	outputs, err := processor.Process(context.Background(), Media{ObjectKey: "original.png", ContentType: "image/png"}, profile, input)
	if err != nil {
		t.Fatal(err)
	}
	checkDimensions := func(name string, width, height int) {
		t.Helper()
		config, _, err := image.DecodeConfig(bytes.NewReader(outputs[name].Data))
		if err != nil {
			t.Fatalf("decode %s rendition: %v", name, err)
		}
		if config.Width != width || config.Height != height {
			t.Fatalf("%s dimensions = %dx%d, want %dx%d", name, config.Width, config.Height, width, height)
		}
		if outputs[name].MIMEType != "image/webp" {
			t.Fatalf("%s MIME = %q, want image/webp", name, outputs[name].MIMEType)
		}
	}
	checkDimensions("small", 160, 120)
	checkDimensions("large", 320, 240)
	checkDimensions("square", 160, 160)
}

func TestFFmpegProcessorRejectsOversizedSourceDimensionsBeforeEncoding(t *testing.T) {
	processor := &FFmpegProcessor{timeout: time.Minute, tempDir: t.TempDir()}
	data := pngFixture(t, 1, 1)
	binary.BigEndian.PutUint32(data[16:20], uint32(maxImagePixels))
	binary.BigEndian.PutUint32(data[20:24], 2)
	binary.BigEndian.PutUint32(data[29:33], crc32.ChecksumIEEE(data[12:29]))

	profile := FieldProfile{Accept: "raster-image", Renditions: []Rendition{{Name: "small", Width: 320, Height: 240, Fit: "cover"}}}
	_, err := processor.Process(context.Background(), Media{ContentType: "image/png"}, profile, data)
	if err == nil || !strings.Contains(err.Error(), "dimensions are not supported") {
		t.Fatalf("oversized source dimensions error=%v, want a clear pre-encode rejection", err)
	}
}

func TestFFmpegProcessorEncodesVideoAndPoster(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg unavailable")
	}
	processor, err := NewFFmpegProcessor(15 * time.Minute)
	if err != nil {
		t.Skipf("ffmpeg processor unavailable: %v", err)
	}
	requireWebPEncoder(t, processor)
	dir := t.TempDir()
	inputPath := filepath.Join(dir, "input.mp4")
	if _, err := runCommand(context.Background(), ffmpeg,
		"-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "color=c=blue:s=320x240:d=0.5",
		"-an", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-y", inputPath); err != nil {
		t.Skipf("host ffmpeg cannot create H.264 fixture: %v", err)
	}
	input, err := os.ReadFile(inputPath)
	if err != nil {
		t.Fatal(err)
	}
	profile := FieldProfile{Accept: "video", Video: &VideoProfile{
		MaxWidth: 160, MaxHeight: 120,
		Poster: Rendition{Name: "poster", Width: 100, Height: 100, Fit: "cover"},
	}}
	outputs, err := processor.Process(context.Background(), Media{ObjectKey: "original.mp4", ContentType: "video/mp4"}, profile, input)
	if err != nil {
		t.Fatal(err)
	}
	video := outputs["video"]
	if video.MIMEType != "video/mp4" || video.Width > 160 || video.Height > 120 || video.Width <= 0 || video.Height <= 0 {
		t.Fatalf("unexpected encoded video: %+v", video)
	}
	poster := outputs["poster"]
	config, _, err := image.DecodeConfig(bytes.NewReader(poster.Data))
	if err != nil {
		t.Fatal(err)
	}
	if poster.MIMEType != "image/webp" || config.Width != 100 || config.Height != 100 {
		t.Fatalf("unexpected poster: mime=%s size=%dx%d", poster.MIMEType, config.Width, config.Height)
	}
}

func TestFFmpegProcessorPreservesAnimatedAndVectorImages(t *testing.T) {
	processor := &FFmpegProcessor{timeout: time.Minute, tempDir: t.TempDir()}
	profile := FieldProfile{Accept: "raster-image", Renditions: []Rendition{{Name: "card", Width: 400, Height: 300, Fit: "cover"}}}
	animatedWebP := []byte("RIFF\x00\x00\x00\x00WEBPVP8X\x0a\x00\x00\x00\x02\x00\x00\x00\x00\x00\x00\x00\x00\x00")
	for name, input := range map[string]Media{
		"animated GIF":  {ContentType: "image/gif"},
		"SVG":           {ContentType: "image/svg+xml"},
		"animated WebP": {ContentType: "image/webp"},
	} {
		t.Run(name, func(t *testing.T) {
			data := []byte("original")
			if name == "animated WebP" {
				data = animatedWebP
			}
			outputs, err := processor.Process(context.Background(), input, profile, data)
			if err != nil {
				t.Fatal(err)
			}
			if len(outputs) != 0 {
				t.Fatalf("original-only image unexpectedly produced renditions: %#v", outputs)
			}
		})
	}
}

func pngFixture(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 90, A: 255})
		}
	}
	var output bytes.Buffer
	if err := png.Encode(&output, img); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func requireWebPEncoder(t *testing.T, processor *FFmpegProcessor) {
	t.Helper()
	output, err := runCommand(context.Background(), processor.ffmpeg, "-hide_banner", "-encoders")
	if err != nil {
		t.Skipf("cannot inspect ffmpeg encoders: %v", err)
	}
	if !strings.Contains(string(output), "libwebp") {
		t.Skip("ffmpeg build does not include the libwebp encoder")
	}
}

// Keep the fixture IO isolated so tests don't accidentally share a temporary
// file handle while FFmpeg is running.
