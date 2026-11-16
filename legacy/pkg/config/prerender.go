package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	publicruntime "websiteengine/pkg/runtime"
)

// LoadPrerender requires an explicit switch even when rendering is disabled.
// Paths refer to preinstalled operator-managed executables; startup never
// downloads a browser or installs a JavaScript package.
func LoadPrerender() (*publicruntime.PrerenderOptions, error) {
	raw := strings.TrimSpace(os.Getenv("PRERENDER_ENABLED"))
	if raw == "" {
		return nil, fmt.Errorf("PRERENDER_ENABLED is required")
	}
	enabled, err := strconv.ParseBool(raw)
	if err != nil {
		return nil, fmt.Errorf("PRERENDER_ENABLED must be true or false")
	}
	if !enabled {
		return nil, nil
	}
	options := publicruntime.PrerenderOptions{NodePath: os.Getenv("PRERENDER_NODE_PATH"), WorkerPath: os.Getenv("PRERENDER_WORKER_PATH"), PlaywrightModule: os.Getenv("PRERENDER_PLAYWRIGHT_MODULE"), BrowserPath: os.Getenv("PRERENDER_BROWSER_PATH")}
	for _, setting := range []struct {
		name   string
		target *int
		min    int
	}{{"PRERENDER_CONCURRENCY", &options.Concurrency, 1}, {"PRERENDER_QUEUE_SIZE", &options.QueueSize, 1}, {"PRERENDER_CACHE_ENTRIES", &options.CacheEntries, 1}, {"PRERENDER_MAX_HTML_BYTES", &options.MaxHTMLBytes, 1024}, {"PRERENDER_HEAP_MB", &options.HeapMB, 32}, {"PRERENDER_VIEWPORT_WIDTH", &options.ViewportWidth, 320}, {"PRERENDER_VIEWPORT_HEIGHT", &options.ViewportHeight, 240}, {"PRERENDER_MAX_ATTEMPTS", &options.MaxAttempts, 1}} {
		value, err := strconv.Atoi(os.Getenv(setting.name))
		if err != nil {
			return nil, fmt.Errorf("%s must be explicitly configured as an integer", setting.name)
		}
		if value < setting.min {
			return nil, fmt.Errorf("%s must be at least %d", setting.name, setting.min)
		}
		*setting.target = value
	}
	for _, setting := range []struct {
		name   string
		target *time.Duration
	}{{"PRERENDER_TIMEOUT", &options.Timeout}, {"PRERENDER_ATTEMPT_TIMEOUT", &options.AttemptTimeout}, {"PRERENDER_QUIET_INTERVAL", &options.QuietInterval}, {"PRERENDER_CACHE_TTL", &options.CacheTTL}} {
		value, err := time.ParseDuration(os.Getenv(setting.name))
		if err != nil || value <= 0 {
			return nil, fmt.Errorf("%s must be explicitly configured as a positive duration", setting.name)
		}
		*setting.target = value
	}
	options.MaxRequests, err = strconv.Atoi(os.Getenv("PRERENDER_MAX_REQUESTS"))
	if err != nil {
		return nil, fmt.Errorf("PRERENDER_MAX_REQUESTS must be explicitly configured as an integer")
	}
	if options.MaxRequests < 1 {
		return nil, fmt.Errorf("PRERENDER_MAX_REQUESTS must be positive")
	}
	options.MaxNetworkBytes, err = strconv.ParseInt(os.Getenv("PRERENDER_MAX_NETWORK_BYTES"), 10, 64)
	if err != nil {
		return nil, fmt.Errorf("PRERENDER_MAX_NETWORK_BYTES must be explicitly configured as an integer")
	}
	if options.MaxNetworkBytes < 1024 {
		return nil, fmt.Errorf("PRERENDER_MAX_NETWORK_BYTES must be at least 1024 bytes")
	}
	if options.AttemptTimeout > options.Timeout {
		return nil, fmt.Errorf("PRERENDER_ATTEMPT_TIMEOUT must not exceed PRERENDER_TIMEOUT")
	}
	if options.QuietInterval >= options.AttemptTimeout {
		return nil, fmt.Errorf("PRERENDER_QUIET_INTERVAL must be shorter than PRERENDER_ATTEMPT_TIMEOUT")
	}
	for _, setting := range []struct {
		name       string
		path       string
		executable bool
	}{{"PRERENDER_NODE_PATH", options.NodePath, true}, {"PRERENDER_WORKER_PATH", options.WorkerPath, false}, {"PRERENDER_PLAYWRIGHT_MODULE", options.PlaywrightModule, false}, {"PRERENDER_BROWSER_PATH", options.BrowserPath, true}} {
		info, err := os.Stat(setting.path)
		if err != nil || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("%s must name an existing regular file", setting.name)
		}
		if setting.executable && info.Mode().Perm()&0o111 == 0 {
			return nil, fmt.Errorf("%s must name an executable file", setting.name)
		}
	}
	if err := options.Validate(); err != nil {
		return nil, fmt.Errorf("prerender: %w", err)
	}
	return &options, nil
}
