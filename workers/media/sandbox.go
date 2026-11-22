package media

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	mediaprofile "github.com/Zubrium/cdn/pkg/contracts/media"
)

var (
	containerSequence     atomic.Uint64
	ErrSandboxUnavailable = errors.New("media sandbox preflight failed")
	probeSeccomp          = regexp.MustCompile(`(?m)^seccomp=(\d+)$`)
	probeCapEff           = regexp.MustCompile(`(?m)^capeff=([0-9a-f]+)$`)
	probeNoNewPrivs       = regexp.MustCompile(`(?m)^nonewprivs=([01])$`)
	probeCPU              = regexp.MustCompile(`(?m)^cpu=(.+)$`)
	probeMemory           = regexp.MustCompile(`(?m)^memory=(\d+)$`)
	probePIDs             = regexp.MustCompile(`(?m)^pids=(\d+)$`)
	probeAppArmor         = regexp.MustCompile(`(?m)^apparmor=(.*)$`)
)

// SandboxState is the read-only result of a successful in-container preflight.
// It is supplied to trusted composition only; no HTTP request can select or
// modify any field.
type SandboxState struct {
	Backend            string    `json:"backend"`
	Image              string    `json:"image"`
	SeccompMode        int       `json:"seccomp_mode"`
	CapEff             string    `json:"cap_eff"`
	NoNewPrivs         bool      `json:"no_new_privs"`
	CPUQuota           string    `json:"cpu_quota"`
	MemoryLimitBytes   int64     `json:"memory_limit_bytes"`
	PIDsLimit          int64     `json:"pids_limit"`
	ReadOnlyRootFS     bool      `json:"read_only_root_fs"`
	NetworkDenied      bool      `json:"network_denied"`
	PrivateStaging0700 bool      `json:"private_staging_0700"`
	AppArmorLabel      string    `json:"apparmor_label,omitempty"`
	VerifiedAt         time.Time `json:"verified_at"`
}

// DockerSandboxConfig is trusted startup configuration. Every limit is
// explicit; the adapter has no hidden runtime defaults.
type DockerSandboxConfig struct {
	Image            string
	DockerBinary     string
	StagingRoot      string
	CPUs             string
	MemoryLimitBytes int64
	PIDsLimit        int64
	MaxInputBytes    int64
	MaxOutputBytes   int64
	Timeout          time.Duration
}

// DockerSandbox runs FFmpeg in a one-shot Linux container whose kernel
// enforces seccomp, dropped capabilities, no-new-privileges, cgroup quotas,
// read-only root filesystem and no network. The only writable path is a
// private per-call staging directory created by Core-mediated composition.
type DockerSandbox struct {
	config DockerSandboxConfig
	state  SandboxState
}

// NewDockerSandbox refuses to construct a runner unless the preflight proof
// succeeded inside the actual runtime container. There is no fallback.
func NewDockerSandbox(ctx context.Context, config DockerSandboxConfig) (*DockerSandbox, error) {
	if err := validateSandboxConfig(config); err != nil {
		return nil, err
	}
	sandbox := &DockerSandbox{config: config}
	state, err := sandbox.Probe(ctx)
	if err != nil {
		return nil, err
	}
	sandbox.state = state
	return sandbox, nil
}

func (s *DockerSandbox) Isolated() bool {
	return s != nil && !s.state.VerifiedAt.IsZero() && s.state.SeccompMode >= 2 &&
		s.state.CapEff == strings.Repeat("0", 16) && s.state.NoNewPrivs && s.state.ReadOnlyRootFS &&
		s.state.NetworkDenied && s.state.PrivateStaging0700 && s.state.CPUQuota != "" &&
		s.state.MemoryLimitBytes > 0 && s.state.PIDsLimit > 0
}

func (s *DockerSandbox) SandboxState() SandboxState {
	if s == nil {
		return SandboxState{}
	}
	return s.state
}

// Probe performs the effective-limit checks from inside a fresh container.
// It is exported so the disposable acceptance run can assert the same proof
// independently of the constructor.
func (s *DockerSandbox) Probe(ctx context.Context) (SandboxState, error) {
	if s == nil || ctx == nil {
		return SandboxState{}, ErrSandboxUnavailable
	}
	work, err := s.newWorkDir()
	if err != nil {
		return SandboxState{}, err
	}
	defer os.RemoveAll(work)

	script := `set -eu
seccomp=$(awk '/^Seccomp:/ {print $2}' /proc/self/status)
capeff=$(awk '/^CapEff:/ {print $2}' /proc/self/status)
nonewprivs=$(awk '/^NoNewPrivs:/ {print $2}' /proc/self/status)
cpu=$(cat /sys/fs/cgroup/cpu.max)
memory=$(cat /sys/fs/cgroup/memory.max)
pids=$(cat /sys/fs/cgroup/pids.max)
apparmor=$(cat /proc/self/attr/current 2>/dev/null || true)
rootopts=$(awk '$5 == "/" {print $6; exit}' /proc/self/mountinfo)
case "$rootopts" in *ro*) rootfs=ro ;; *) echo "root filesystem is writable" >&2; exit 1 ;; esac
touch /work/.probe
rm /work/.probe
if ip link show eth0 >/dev/null 2>&1; then echo "network interface is present" >&2; exit 1; fi
if wget -q -T 1 -O /dev/null http://203.0.113.1; then echo "network is reachable" >&2; exit 1; fi
printf 'seccomp=%s\ncapeff=%s\nnonewprivs=%s\ncpu=%s\nmemory=%s\npids=%s\napparmor=%s\nrootfs=%s\nstaging=writable\nnetwork=denied\n' \
  "$seccomp" "$capeff" "$nonewprivs" "$cpu" "$memory" "$pids" "$apparmor" "$rootfs"
`
	args := append(s.containerArgs("/bin/sh", work), "-c", script)
	stdout, stderr, err := s.runContainer(ctx, work, args)
	if err != nil {
		return SandboxState{}, fmt.Errorf("%w: %v", ErrSandboxUnavailable, strings.TrimSpace(stderr))
	}
	seccomp, err := parseProbeInt(stdout, probeSeccomp)
	if err != nil || seccomp < 2 {
		return SandboxState{}, ErrSandboxUnavailable
	}
	capEffMatch := probeCapEff.FindStringSubmatch(stdout)
	if len(capEffMatch) != 2 || capEffMatch[1] != strings.Repeat("0", 16) {
		return SandboxState{}, ErrSandboxUnavailable
	}
	noNewPrivs, err := parseProbeInt(stdout, probeNoNewPrivs)
	if err != nil || noNewPrivs != 1 {
		return SandboxState{}, ErrSandboxUnavailable
	}
	cpu := probeCPU.FindStringSubmatch(stdout)
	memory, err := parseProbeInt(stdout, probeMemory)
	pids, errPIDs := parseProbeInt(stdout, probePIDs)
	if len(cpu) != 2 || err != nil || errPIDs != nil || memory != s.config.MemoryLimitBytes || pids != s.config.PIDsLimit {
		return SandboxState{}, ErrSandboxUnavailable
	}
	appArmor := ""
	if match := probeAppArmor.FindStringSubmatch(stdout); len(match) == 2 {
		appArmor = match[1]
	}
	if !strings.Contains(stdout, "rootfs=ro\n") || !strings.Contains(stdout, "staging=writable\n") || !strings.Contains(stdout, "network=denied\n") {
		return SandboxState{}, ErrSandboxUnavailable
	}
	return SandboxState{
		Backend: "docker-linux", Image: s.config.Image, SeccompMode: int(seccomp), CapEff: capEffMatch[1],
		NoNewPrivs: true, CPUQuota: cpu[1], MemoryLimitBytes: memory, PIDsLimit: pids,
		ReadOnlyRootFS: true, NetworkDenied: true, PrivateStaging0700: true,
		AppArmorLabel: appArmor, VerifiedAt: time.Now().UTC(),
	}, nil
}

// Process stages the source bytes, derives all FFmpeg arguments from the
// pinned profile, executes inside the verified container and returns only a
// bounded, checked output stream.
func (s *DockerSandbox) Process(ctx context.Context, request mediaprofile.WorkRequest, profile mediaprofile.Profile, source io.Reader) (ProcessedOutput, error) {
	if s == nil || !s.Isolated() || ctx == nil || source == nil {
		return ProcessedOutput{}, ErrSandboxUnavailable
	}
	work, err := s.newWorkDir()
	if err != nil {
		return ProcessedOutput{}, err
	}
	sourcePath := filepath.Join(work, "source.input")
	outputPath := filepath.Join(work, "output.result")
	input, err := os.OpenFile(sourcePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		os.RemoveAll(work)
		return ProcessedOutput{}, ErrSandboxUnavailable
	}
	hash := sha256.New()
	size, err := io.Copy(io.MultiWriter(input, hash), io.LimitReader(source, s.config.MaxInputBytes+1))
	closeErr := input.Close()
	if err != nil || closeErr != nil || size <= 0 || size > s.config.MaxInputBytes ||
		!strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), request.SourceSHA256) {
		os.RemoveAll(work)
		return ProcessedOutput{}, ErrInvalidRequest
	}
	contentType, ok := inputContentType(profile, sourcePath)
	if !ok {
		os.RemoveAll(work)
		return ProcessedOutput{}, ErrInvalidRequest
	}
	if profile.Accept == "file" || profile.Accept == "image" {
		body, err := os.Open(sourcePath)
		if err != nil {
			os.RemoveAll(work)
			return ProcessedOutput{}, ErrSandboxUnavailable
		}
		return ProcessedOutput{Body: &stagedOutput{File: body, dir: work}, Size: size,
			SHA256: strings.ToLower(hex.EncodeToString(hash.Sum(nil))), ContentType: contentType}, nil
	}
	args, outputType, err := ffmpegArgs(profile, request.OutputName)
	if err != nil {
		os.RemoveAll(work)
		return ProcessedOutput{}, err
	}
	commandArgs := append(s.containerArgs(sandboxFFmpegBinary, work), args...)
	commandArgs = append(commandArgs, "/work/output.result")
	processCtx, cancel := context.WithTimeout(ctx, s.config.Timeout)
	defer cancel()
	if _, stderr, err := s.runContainer(processCtx, work, commandArgs); err != nil {
		os.RemoveAll(work)
		if errors.Is(processCtx.Err(), context.DeadlineExceeded) {
			return ProcessedOutput{}, fmt.Errorf("%w: FFmpeg deadline exceeded", context.DeadlineExceeded)
		}
		return ProcessedOutput{}, fmt.Errorf("%w: FFmpeg execution failed: %s", ErrRunnerFailed, strings.TrimSpace(stderr))
	}
	info, err := os.Stat(outputPath)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > s.config.MaxOutputBytes {
		os.RemoveAll(work)
		return ProcessedOutput{}, ErrOutputInvalid
	}
	body, err := os.Open(outputPath)
	if err != nil {
		os.RemoveAll(work)
		return ProcessedOutput{}, ErrSandboxUnavailable
	}
	if !matchesSignature(readSignature(body), outputType) {
		body.Close()
		os.RemoveAll(work)
		return ProcessedOutput{}, ErrOutputInvalid
	}
	if _, err := body.Seek(0, io.SeekStart); err != nil {
		body.Close()
		os.RemoveAll(work)
		return ProcessedOutput{}, ErrSandboxUnavailable
	}
	outputHash := sha256.New()
	count, err := io.Copy(outputHash, body)
	if err != nil || count != info.Size() {
		body.Close()
		os.RemoveAll(work)
		return ProcessedOutput{}, ErrOutputInvalid
	}
	if _, err := body.Seek(0, io.SeekStart); err != nil {
		body.Close()
		os.RemoveAll(work)
		return ProcessedOutput{}, ErrSandboxUnavailable
	}
	return ProcessedOutput{Body: &stagedOutput{File: body, dir: work}, Size: count,
		SHA256: hex.EncodeToString(outputHash.Sum(nil)), ContentType: outputType}, nil
}

const sandboxFFmpegBinary = "/bin/ffmpeg"

func ffmpegArgs(profile mediaprofile.Profile, outputName string) ([]string, string, error) {
	args := []string{"-nostdin", "-hide_banner", "-v", "error", "-protocol_whitelist", "file,pipe", "-i", "/work/source.input"}
	switch profile.Accept {
	case "raster-image":
		for _, rendition := range profile.Renditions {
			if rendition.Name != outputName {
				continue
			}
			filter := fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=decrease", rendition.Width, rendition.Height)
			if rendition.Fit == "cover" {
				filter += fmt.Sprintf(",crop=%d:%d", rendition.Width, rendition.Height)
			}
			return append(args, "-vf", filter, "-frames:v", "1", "-an", "-c:v", "libwebp", "-f", "image2"), "image/webp", nil
		}
	case "video":
		if profile.Video == nil {
			return nil, "", ErrInvalidPreset
		}
		scale := fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=decrease", profile.Video.MaxWidth, profile.Video.MaxHeight)
		switch outputName {
		case "video":
			return append(args, "-vf", scale, "-c:v", "libx264", "-c:a", "aac", "-f", "mp4"), "video/mp4", nil
		case "poster":
			return append(args, "-vf", "scale=640:-2", "-frames:v", "1", "-an", "-c:v", "libwebp", "-f", "image2"), "image/webp", nil
		}
	}
	return nil, "", ErrInvalidPreset
}

func inputContentType(profile mediaprofile.Profile, path string) (string, bool) {
	var allowed []string
	switch profile.Accept {
	case "raster-image", "image":
		allowed = []string{"image/jpeg", "image/png", "image/webp", "image/gif", "image/avif"}
	case "video":
		allowed = []string{"video/mp4", "video/webm"}
	case "file":
		allowed = []string{"image/jpeg", "image/png", "image/webp", "image/gif", "image/avif", "video/mp4", "video/webm"}
	default:
		return "", false
	}
	signature := readSignaturePath(path)
	for _, contentType := range allowed {
		if matchesSignature(signature, contentType) {
			return contentType, true
		}
	}
	return "", false
}

func readSignature(file *os.File) []byte {
	buffer := make([]byte, 16)
	n, _ := io.ReadFull(file, buffer)
	return buffer[:n]
}

func readSignaturePath(path string) []byte {
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()
	return readSignature(file)
}

func (s *DockerSandbox) containerArgs(entrypoint, work string) []string {
	name := "zubrium-media-" + strconv.FormatUint(containerSequence.Add(1), 10) + "-" +
		strconv.FormatInt(time.Now().UnixNano(), 10)
	return []string{
		"run", "--rm", "--name", name, "--platform", "linux/amd64",
		"--network", "none", "--read-only",
		"--cap-drop", "ALL", "--security-opt", "no-new-privileges:true",
		"--security-opt", "apparmor=docker-default",
		"--cpus", s.config.CPUs,
		"--memory", strconv.FormatInt(s.config.MemoryLimitBytes, 10),
		"--pids-limit", strconv.FormatInt(s.config.PIDsLimit, 10),
		"--volume", work + ":/work:rw",
		"--entrypoint", entrypoint, s.config.Image,
	}
}

func (s *DockerSandbox) runContainer(ctx context.Context, work string, args []string) (string, string, error) {
	command := exec.CommandContext(ctx, s.config.DockerBinary, args...)
	command.Dir = work
	command.Env = []string{"LANG=C", "LC_ALL=C", "PATH=/usr/bin:/bin"}
	var stdout, stderr strings.Builder
	command.Stdout, command.Stderr = &stdout, &stderr
	err := command.Run()
	if err != nil && len(args) > 3 && args[2] == "--name" {
		// CommandContext kills the Docker client, not the container. Remove the
		// run-scoped container explicitly so a failed deadline cannot leak a
		// still-running process.
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		cleanup := exec.CommandContext(cleanupCtx, s.config.DockerBinary, "rm", "-f", args[3])
		cleanup.Env = []string{"LANG=C", "LC_ALL=C", "PATH=/usr/bin:/bin"}
		_ = cleanup.Run()
	}
	return stdout.String(), stderr.String(), err
}

func (s *DockerSandbox) newWorkDir() (string, error) {
	work, err := os.MkdirTemp(s.config.StagingRoot, "media-")
	if err != nil {
		return "", ErrSandboxUnavailable
	}
	if err := os.Chmod(work, 0o700); err != nil {
		os.RemoveAll(work)
		return "", ErrSandboxUnavailable
	}
	return work, nil
}

func validateSandboxConfig(config DockerSandboxConfig) error {
	if !strings.Contains(config.Image, "@sha256:") || len(config.Image) < 32 ||
		strings.TrimSpace(config.DockerBinary) == "" ||
		!filepath.IsAbs(config.StagingRoot) || filepath.Clean(config.StagingRoot) != config.StagingRoot ||
		config.CPUs == "" || config.MemoryLimitBytes <= 0 || config.PIDsLimit <= 0 ||
		config.MaxInputBytes <= 0 || config.MaxOutputBytes <= 0 || config.Timeout <= 0 {
		return ErrSandboxUnavailable
	}
	info, err := os.Lstat(config.StagingRoot)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o077 != 0 {
		return ErrSandboxUnavailable
	}
	return nil
}

func parseProbeInt(output string, pattern *regexp.Regexp) (int64, error) {
	match := pattern.FindStringSubmatch(output)
	if len(match) != 2 {
		return 0, ErrSandboxUnavailable
	}
	value, err := strconv.ParseInt(match[1], 10, 64)
	if err != nil {
		return 0, ErrSandboxUnavailable
	}
	return value, nil
}

type stagedOutput struct {
	*os.File
	dir string
}

func (o *stagedOutput) Close() error {
	err := o.File.Close()
	os.RemoveAll(o.dir)
	return err
}
