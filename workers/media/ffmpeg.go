package media

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

var ErrRunnerFailed = errors.New("media_runner_failed")

// FFmpegRunner executes one absolute, server-selected binary without a shell.
// It provides private temporary files, wall-clock cancellation and byte checks;
// it does NOT provide an OS sandbox or enforce CPU/memory/process quotas.
type FFmpegRunner struct {
	executable string
	tempRoot   string
}

func NewFFmpegRunner(executable, tempRoot string) (*FFmpegRunner, error) {
	if !filepath.IsAbs(executable) || filepath.Clean(executable) != executable || !filepath.IsAbs(tempRoot) || filepath.Clean(tempRoot) != tempRoot {
		return nil, ErrInvalidPreset
	}
	info, err := os.Stat(executable)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return nil, ErrInvalidPreset
	}
	root, err := os.Lstat(tempRoot)
	if err != nil || !root.IsDir() || root.Mode()&os.ModeSymlink != 0 || root.Mode().Perm()&0077 != 0 {
		return nil, ErrInvalidPreset
	}
	return &FFmpegRunner{executable: executable, tempRoot: tempRoot}, nil
}

func (r *FFmpegRunner) Isolated() bool { return false }

func (r *FFmpegRunner) Run(ctx context.Context, preset Preset, source io.Reader) (ProcessedOutput, error) {
	if r == nil || ctx == nil || source == nil || preset.Timeout <= 0 || preset.MaxInputBytes <= 0 || preset.MaxOutputBytes <= 0 ||
		!safeOutputFormat(preset.OutputFormat) || !formatMatchesContentType(preset.OutputFormat, preset.OutputContentType) || len(preset.FFmpegArgs) == 0 {
		return ProcessedOutput{}, ErrInvalidPreset
	}
	workdir, err := os.MkdirTemp(r.tempRoot, "media-task-")
	if err != nil {
		return ProcessedOutput{}, ErrRunnerFailed
	}
	defer os.RemoveAll(workdir)
	if err := os.Chmod(workdir, 0700); err != nil {
		return ProcessedOutput{}, ErrRunnerFailed
	}
	inputPath, outputPath := filepath.Join(workdir, "source.input"), filepath.Join(workdir, "result.output")
	input, err := os.OpenFile(inputPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return ProcessedOutput{}, ErrRunnerFailed
	}
	inputSize, copyErr := io.Copy(input, io.LimitReader(source, preset.MaxInputBytes+1))
	closeErr := input.Close()
	if copyErr != nil || closeErr != nil || inputSize <= 0 || inputSize > preset.MaxInputBytes || !matchesAnyInputType(inputPath, preset.InputContentTypes) {
		return ProcessedOutput{}, ErrRunnerFailed
	}

	boundedCtx, cancel := context.WithTimeout(ctx, preset.Timeout)
	defer cancel()
	args := []string{"-nostdin", "-hide_banner", "-v", "error", "-protocol_whitelist", "file,pipe", "-i", inputPath}
	for _, arg := range preset.FFmpegArgs {
		if !validTrustedArg(arg) {
			return ProcessedOutput{}, ErrInvalidPreset
		}
		args = append(args, arg)
	}
	args = append(args, "-f", preset.OutputFormat, outputPath)
	command := exec.CommandContext(boundedCtx, r.executable, args...)
	command.Dir = workdir
	command.Env = []string{"LANG=C", "LC_ALL=C", "HOME=" + workdir}
	// Do not surface FFmpeg diagnostics: they may include local paths/input data.
	command.Stdout, command.Stderr = io.Discard, io.Discard
	done := make(chan error, 1)
	go func() { done <- command.Run() }()
	quota := time.NewTicker(40 * time.Millisecond)
	defer quota.Stop()
	for {
		select {
		case err := <-done:
			if err != nil || boundedCtx.Err() != nil {
				return ProcessedOutput{}, ErrRunnerFailed
			}
			return openProcessedOutput(outputPath, preset)
		case <-quota.C:
			if info, statErr := os.Stat(outputPath); statErr == nil && info.Size() > preset.MaxOutputBytes {
				cancel()
				<-done
				return ProcessedOutput{}, ErrRunnerFailed
			}
		case <-boundedCtx.Done():
			// CommandContext kills the direct FFmpeg process; this is not a process-tree sandbox.
			<-done
			return ProcessedOutput{}, ErrRunnerFailed
		}
	}
}

func openProcessedOutput(path string, preset Preset) (ProcessedOutput, error) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > preset.MaxOutputBytes {
		return ProcessedOutput{}, ErrRunnerFailed
	}
	file, err := os.Open(path)
	if err != nil {
		return ProcessedOutput{}, ErrRunnerFailed
	}
	if !matchesFormat(file, preset.OutputFormat) {
		file.Close()
		return ProcessedOutput{}, ErrRunnerFailed
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		file.Close()
		return ProcessedOutput{}, ErrRunnerFailed
	}
	hash := sha256.New()
	count, err := io.Copy(hash, file)
	_, seekErr := file.Seek(0, io.SeekStart)
	if err != nil || count != info.Size() || seekErr != nil {
		file.Close()
		return ProcessedOutput{}, ErrRunnerFailed
	}
	return ProcessedOutput{Body: file, Size: count, SHA256: hex.EncodeToString(hash.Sum(nil)), ContentType: preset.OutputContentType}, nil
}

func validTrustedArg(arg string) bool {
	return arg != "" && len(arg) <= 256 && !strings.ContainsAny(arg, "\x00\r\n") && !strings.Contains(arg, "{{") && !strings.Contains(arg, "}}")
}

func matchesAnyInputType(path string, contentTypes []string) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	buf := make([]byte, 16)
	n, err := io.ReadFull(file, buf)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return false
	}
	buf = buf[:n]
	for _, contentType := range contentTypes {
		if matchesSignature(buf, contentType) {
			return true
		}
	}
	return false
}

func matchesFormat(file *os.File, format string) bool {
	buf := make([]byte, 16)
	n, err := io.ReadFull(file, buf)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return false
	}
	return matchesSignature(buf[:n], formatContentType(format))
}

func matchesSignature(buf []byte, contentType string) bool {
	switch contentType {
	case "image/jpeg":
		return len(buf) >= 3 && buf[0] == 0xff && buf[1] == 0xd8 && buf[2] == 0xff
	case "image/png":
		return len(buf) >= 8 && string(buf[:8]) == "\x89PNG\r\n\x1a\n"
	case "image/webp":
		return len(buf) >= 12 && string(buf[:4]) == "RIFF" && string(buf[8:12]) == "WEBP"
	case "image/gif":
		return len(buf) >= 6 && (string(buf[:6]) == "GIF87a" || string(buf[:6]) == "GIF89a")
	case "image/avif":
		return len(buf) >= 12 && string(buf[4:8]) == "ftyp" && (string(buf[8:12]) == "avif" || string(buf[8:12]) == "avis")
	case "video/mp4":
		return len(buf) >= 8 && string(buf[4:8]) == "ftyp"
	case "video/webm":
		return len(buf) >= 4 && string(buf[:4]) == "\x1aE\xdf\xa3"
	default:
		return false
	}
}

func formatContentType(format string) string {
	switch format {
	case "webp":
		return "image/webp"
	case "jpeg":
		return "image/jpeg"
	case "png":
		return "image/png"
	case "avif":
		return "image/avif"
	case "gif":
		return "image/gif"
	case "mp4":
		return "video/mp4"
	case "webm":
		return "video/webm"
	default:
		return ""
	}
}

func safeOutputFormat(format string) bool {
	switch format {
	case "webp", "jpeg", "png", "mp4", "webm", "avif", "gif":
		return true
	default:
		return false
	}
}
