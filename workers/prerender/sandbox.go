package prerender

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

var (
	containerSequence     atomic.Uint64
	ErrSandboxUnavailable = errors.New("prerender sandbox preflight failed")
	probeSeccomp          = regexp.MustCompile(`(?m)^seccomp=(\d+)$`)
	probeCapEff           = regexp.MustCompile(`(?m)^capeff=([0-9a-f]+)$`)
	probeNoNewPrivs       = regexp.MustCompile(`(?m)^nonewprivs=([01])$`)
	probeCPU              = regexp.MustCompile(`(?m)^cpu=(.+)$`)
	probeMemory           = regexp.MustCompile(`(?m)^memory=(\d+)$`)
	probePIDs             = regexp.MustCompile(`(?m)^pids=(\d+)$`)
	probeBrowser          = regexp.MustCompile(`(?m)^browser=(.+)$`)
)

// SandboxState is the read-only proof gathered from inside the exact container
// used for rendering. It is never derived from host state.
type SandboxState struct {
	Backend          string    `json:"backend"`
	Image            string    `json:"image"`
	Platform         string    `json:"platform"`
	SeccompMode      int       `json:"seccomp_mode"`
	CapEff           string    `json:"cap_eff"`
	NoNewPrivs       bool      `json:"no_new_privs"`
	CPUQuota         string    `json:"cpu_quota"`
	MemoryLimitBytes int64     `json:"memory_limit_bytes"`
	PIDsLimit        int64     `json:"pids_limit"`
	ReadOnlyRootFS   bool      `json:"read_only_root_fs"`
	NetworkDenied    bool      `json:"network_denied"`
	BrowserVersion   string    `json:"browser_version"`
	VerifiedAt       time.Time `json:"verified_at"`
}

// DockerSandboxConfig is trusted startup configuration. Every limit is
// explicit and the image must be pinned by digest.
type DockerSandboxConfig struct {
	Image            string
	Platform         string
	DockerBinary     string
	StagingRoot      string
	CPUs             string
	MemoryLimitBytes int64
	PIDsLimit        int64
	MaxDocumentBytes int64
	Timeout          time.Duration
}

// DockerSandbox renders one trusted, bounded document in a one-shot Linux
// container. The kernel enforces seccomp, dropped capabilities, no-new-privs,
// cgroup limits, read-only rootfs and no network. Staging is the only writable
// path and is private to the call.
type DockerSandbox struct {
	config DockerSandboxConfig
	state  SandboxState
}

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
		s.state.NetworkDenied && s.state.CPUQuota != "" && s.state.MemoryLimitBytes > 0 &&
		s.state.PIDsLimit > 0 && s.state.BrowserVersion != ""
}

func (s *DockerSandbox) SandboxState() SandboxState {
	if s == nil {
		return SandboxState{}
	}
	return s.state
}

// Probe independently verifies the effective in-container limits, that every
// forbidden address family is unreachable, and that Chromium executes there.
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
rootopts=$(awk '$5 == "/" {print $6; exit}' /proc/self/mountinfo)
case "$rootopts" in *ro*) rootfs=ro ;; *) echo "root filesystem is writable" >&2; exit 1 ;; esac
touch /work/.probe
rm /work/.probe
for target in http://127.0.0.1:9/ http://127.0.0.1/ http://10.0.0.1/ http://169.254.169.254/latest/meta-data/ http://[::1]/ http://192.168.1.1/ http://172.16.0.1/; do
  if wget -q -T 1 -O /dev/null "$target" 2>/dev/null; then
    echo "forbidden network target reachable: $target" >&2
    exit 1
  fi
done
browser=$(chromium --version)
printf 'browser=%s\n' "$browser"
printf 'seccomp=%s\ncapeff=%s\nnonewprivs=%s\ncpu=%s\nmemory=%s\npids=%s\nrootfs=%s\nnetwork=denied\n' \
  "$seccomp" "$capeff" "$nonewprivs" "$cpu" "$memory" "$pids" "$rootfs"
`
	args := append(s.containerArgs("/bin/sh", work), "-c", script)
	stdout, stderr, err := s.runContainer(ctx, work, args)
	if err != nil {
		return SandboxState{}, fmt.Errorf("%w: %s", ErrSandboxUnavailable, strings.TrimSpace(stderr))
	}
	seccomp, err1 := parseProbeInt(stdout, probeSeccomp)
	cap := probeCapEff.FindStringSubmatch(stdout)
	noNewPrivs, err2 := parseProbeInt(stdout, probeNoNewPrivs)
	cpu := probeCPU.FindStringSubmatch(stdout)
	memory, err3 := parseProbeInt(stdout, probeMemory)
	pids, err4 := parseProbeInt(stdout, probePIDs)
	browser := probeBrowser.FindStringSubmatch(stdout)
	if err1 != nil || err2 != nil || err3 != nil || err4 != nil || seccomp < 2 || len(cap) != 2 ||
		cap[1] != strings.Repeat("0", 16) || noNewPrivs != 1 || len(cpu) != 2 ||
		memory != s.config.MemoryLimitBytes || pids != s.config.PIDsLimit || len(browser) != 2 ||
		!strings.Contains(browser[1], "Chromium") ||
		!strings.Contains(stdout, "rootfs=ro\n") || !strings.Contains(stdout, "network=denied\n") {
		return SandboxState{}, ErrSandboxUnavailable
	}
	return SandboxState{
		Backend: "docker-linux", Image: s.config.Image, Platform: s.config.Platform,
		SeccompMode: int(seccomp), CapEff: cap[1], NoNewPrivs: true, CPUQuota: cpu[1],
		MemoryLimitBytes: memory, PIDsLimit: pids, ReadOnlyRootFS: true, NetworkDenied: true,
		BrowserVersion: strings.TrimSpace(browser[1]), VerifiedAt: time.Now().UTC(),
	}, nil
}

// Render stages the trusted document, captures Chromium's post-script DOM in
// the isolated container, and returns only verified bounded bytes.
func (s *DockerSandbox) Render(ctx context.Context, document []byte, marker string) ([]byte, error) {
	if s == nil || !s.Isolated() || ctx == nil || len(document) == 0 || int64(len(document)) > s.config.MaxDocumentBytes || marker == "" {
		return nil, ErrSandboxUnavailable
	}
	work, err := s.newWorkDir()
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(work)
	inputPath := filepath.Join(work, "input.html")
	if err := os.WriteFile(inputPath, document, 0o600); err != nil {
		return nil, ErrSandboxUnavailable
	}
	script := `set -u
chromium_status=0
chromium --headless=new --virtual-time-budget=1000 --no-zygote --single-process --no-sandbox \
  --disable-gpu --disable-dev-shm-usage --disable-background-networking --disable-default-apps \
  --disable-extensions --disable-sync --no-first-run --user-data-dir=/work/profile \
  --dump-dom file:///work/input.html > /work/output.html 2>/work/chrome.log || chromium_status=$?
if [ ! -s /work/output.html ]; then
  echo "browser produced no DOM" >&2
  exit 1
fi
grep -q "$1" /work/output.html || {
  echo "browser DOM does not contain the pinned locale marker" >&2
  exit 1
}
printf 'chromium_status=%s\n' "$chromium_status"
`
	args := append(s.containerArgs("/bin/sh", work), "-c", script, "sh", marker)
	processCtx, cancel := context.WithTimeout(ctx, s.config.Timeout)
	defer cancel()
	if _, stderr, err := s.runContainer(processCtx, work, args); err != nil {
		if errors.Is(processCtx.Err(), context.DeadlineExceeded) {
			return nil, context.DeadlineExceeded
		}
		return nil, fmt.Errorf("%w: browser render failed: %s", ErrSandboxUnavailable, strings.TrimSpace(stderr))
	}
	outputPath := filepath.Join(work, "output.html")
	info, err := os.Stat(outputPath)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > s.config.MaxDocumentBytes {
		return nil, ErrSandboxUnavailable
	}
	output, err := os.ReadFile(outputPath)
	if err != nil || len(output) != int(info.Size()) || !strings.Contains(string(output), marker) {
		return nil, ErrSandboxUnavailable
	}
	return output, nil
}

func (s *DockerSandbox) containerArgs(entrypoint, work string) []string {
	name := "zubrium-prerender-" + strconv.FormatUint(containerSequence.Add(1), 10) + "-" +
		strconv.FormatInt(time.Now().UnixNano(), 10)
	return []string{
		"run", "--rm", "--name", name, "--platform", s.config.Platform,
		"--network", "none", "--read-only", "--tmpfs", "/tmp:rw,noexec,nosuid,size=64m,mode=1777",
		"--cap-drop", "ALL", "--security-opt", "no-new-privileges:true",
		"--security-opt", "apparmor=docker-default",
		"--cpus", s.config.CPUs,
		"--memory", strconv.FormatInt(s.config.MemoryLimitBytes, 10),
		"--pids-limit", strconv.FormatInt(s.config.PIDsLimit, 10),
		"--env", "HOME=/work", "--env", "XDG_CACHE_HOME=/work/cache",
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
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		cleanup := exec.CommandContext(cleanupCtx, s.config.DockerBinary, "rm", "-f", args[3])
		cleanup.Env = []string{"LANG=C", "LC_ALL=C", "PATH=/usr/bin:/bin"}
		_ = cleanup.Run()
	}
	return stdout.String(), stderr.String(), err
}

func (s *DockerSandbox) newWorkDir() (string, error) {
	work, err := os.MkdirTemp(s.config.StagingRoot, "prerender-")
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
		(config.Platform != "linux/amd64" && config.Platform != "linux/arm64") ||
		strings.TrimSpace(config.DockerBinary) == "" ||
		!filepath.IsAbs(config.StagingRoot) || filepath.Clean(config.StagingRoot) != config.StagingRoot ||
		config.CPUs == "" || config.MemoryLimitBytes <= 0 || config.PIDsLimit <= 0 ||
		config.MaxDocumentBytes <= 0 || config.Timeout <= 0 {
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
