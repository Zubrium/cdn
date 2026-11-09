package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

type SitepackConfig struct {
	TempDir             string
	MaxBytes            int64
	MaxExpandedBytes    int64
	MaxEntries          int
	MaxIndexBytes       int64
	MaxSVGBytes         int64
	MaxBrowserBlobBytes int64
}

func LoadSitepack() (SitepackConfig, error) {
	cfg := SitepackConfig{TempDir: strings.TrimSpace(os.Getenv("SITEPACK_TEMP_DIR"))}
	if cfg.TempDir == "" {
		return cfg, fmt.Errorf("SITEPACK_TEMP_DIR is required")
	}
	for _, setting := range []struct {
		name   string
		target *int64
	}{{"SITEPACK_MAX_BYTES", &cfg.MaxBytes}, {"SITEPACK_MAX_EXPANDED_BYTES", &cfg.MaxExpandedBytes}, {"SITEPACK_MAX_INDEX_BYTES", &cfg.MaxIndexBytes}, {"SITEPACK_MAX_SVG_BYTES", &cfg.MaxSVGBytes}, {"SITEPACK_BROWSER_FALLBACK_BYTES", &cfg.MaxBrowserBlobBytes}} {
		value, err := strconv.ParseInt(os.Getenv(setting.name), 10, 64)
		if err != nil || value < 1024 {
			return cfg, fmt.Errorf("%s must be explicitly configured as a byte limit of at least 1024", setting.name)
		}
		*setting.target = value
	}
	value, err := strconv.Atoi(os.Getenv("SITEPACK_MAX_ENTRIES"))
	if err != nil || value < 1 {
		return cfg, fmt.Errorf("SITEPACK_MAX_ENTRIES must be explicitly configured as a positive integer")
	}
	cfg.MaxEntries = value
	if cfg.MaxSVGBytes > 64<<20 {
		return cfg, fmt.Errorf("SITEPACK_MAX_SVG_BYTES must not exceed 64 MiB")
	}
	if cfg.MaxSVGBytes > cfg.MaxBytes {
		return cfg, fmt.Errorf("SITEPACK_MAX_SVG_BYTES must not exceed SITEPACK_MAX_BYTES")
	}
	if cfg.MaxExpandedBytes < cfg.MaxBytes {
		return cfg, fmt.Errorf("SITEPACK_MAX_EXPANDED_BYTES must be at least SITEPACK_MAX_BYTES")
	}
	if cfg.MaxIndexBytes > cfg.MaxExpandedBytes {
		return cfg, fmt.Errorf("SITEPACK_MAX_INDEX_BYTES must not exceed SITEPACK_MAX_EXPANDED_BYTES")
	}
	if cfg.MaxBrowserBlobBytes > cfg.MaxBytes {
		return cfg, fmt.Errorf("SITEPACK_BROWSER_FALLBACK_BYTES must not exceed SITEPACK_MAX_BYTES")
	}
	if err := validateWritableTempDir("SITEPACK_TEMP_DIR", cfg.TempDir); err != nil {
		return cfg, err
	}
	return cfg, nil
}
