package config

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// FilesConfig contains explicit operational limits; LoadFiles has no defaults.
type FilesConfig struct {
	MaxBytes          int64
	PartBytes         int64
	UploadTTL         time.Duration
	CleanupInterval   time.Duration
	ProcessingTimeout time.Duration
	OwnerQuotaBytes   int64
	MaxSessions       int
	ListLimit         int
	RateLimit         int
	RateMaxClients    int
	RateWindow        time.Duration
	LinkMaxTTL        time.Duration
	PublicBaseURL     string
	TempDir           string
	SigningKey        []byte
	Services          []FileServiceConfig
}
type FileServiceConfig struct {
	Name        string          `json:"name"`
	Permissions []string        `json:"permissions"`
	Keys        []FileSecretRef `json:"keys"`
}
type FileSecretRef struct {
	Env  string `json:"env,omitempty"`
	File string `json:"file,omitempty"`
}

func LoadFiles() (FilesConfig, error) {
	var c FilesConfig
	ints := []struct {
		name   string
		target *int64
	}{{"FILES_MAX_BYTES", &c.MaxBytes}, {"FILES_PART_BYTES", &c.PartBytes}, {"FILES_OWNER_QUOTA_BYTES", &c.OwnerQuotaBytes}}
	for _, v := range ints {
		n, e := strconv.ParseInt(os.Getenv(v.name), 10, 64)
		if e != nil || n <= 0 {
			return c, fmt.Errorf("%s must be an explicitly configured positive integer", v.name)
		}
		*v.target = n
	}
	for _, v := range []struct {
		name   string
		target *int
	}{{"FILES_MAX_SESSIONS", &c.MaxSessions}, {"FILES_LIST_LIMIT", &c.ListLimit}, {"FILES_RATE_LIMIT", &c.RateLimit}, {"FILES_RATE_MAX_CLIENTS", &c.RateMaxClients}} {
		n, e := strconv.Atoi(os.Getenv(v.name))
		if e != nil || n <= 0 {
			return c, fmt.Errorf("%s must be an explicitly configured positive integer", v.name)
		}
		*v.target = n
	}
	for _, v := range []struct {
		name   string
		target *time.Duration
	}{{"FILES_UPLOAD_TTL", &c.UploadTTL}, {"FILES_CLEANUP_INTERVAL", &c.CleanupInterval}, {"FILES_LINK_MAX_TTL", &c.LinkMaxTTL}, {"FILES_PROCESS_TIMEOUT", &c.ProcessingTimeout}, {"FILES_RATE_WINDOW", &c.RateWindow}} {
		d, e := time.ParseDuration(os.Getenv(v.name))
		if e != nil || d <= 0 {
			return c, fmt.Errorf("%s must be an explicitly configured positive duration", v.name)
		}
		*v.target = d
	}
	c.TempDir = strings.TrimSpace(os.Getenv("FILES_TEMP_DIR"))
	c.PublicBaseURL = strings.TrimRight(os.Getenv("FILES_PUBLIC_BASE_URL"), "/")
	if c.TempDir == "" {
		return c, fmt.Errorf("FILES_TEMP_DIR is required")
	}
	u, e := url.Parse(c.PublicBaseURL)
	if e != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return c, fmt.Errorf("FILES_PUBLIC_BASE_URL must be an absolute http(s) URL")
	}
	if raw, ok := os.LookupEnv("FILE_SERVICES"); !ok {
		return c, fmt.Errorf("FILE_SERVICES is required (use [] for no services)")
	} else {
		servicesJSON := strings.TrimSpace(raw)
		if len(servicesJSON) == 0 || servicesJSON[0] != '[' {
			return c, fmt.Errorf("FILE_SERVICES must be a single JSON array")
		}
		d := json.NewDecoder(strings.NewReader(servicesJSON))
		d.DisallowUnknownFields()
		if e := d.Decode(&c.Services); e != nil {
			return c, fmt.Errorf("FILE_SERVICES: %w", e)
		}
		var trailing any
		if e := d.Decode(&trailing); e != io.EOF {
			if e == nil {
				return c, fmt.Errorf("FILE_SERVICES must contain exactly one JSON value")
			}
			return c, fmt.Errorf("FILE_SERVICES: %w", e)
		}
	}
	signingKeyEnv := strings.TrimSpace(os.Getenv("FILES_SIGNING_KEY_ENV"))
	signingKeyFile := strings.TrimSpace(os.Getenv("FILES_SIGNING_KEY_FILE"))
	if (signingKeyEnv == "") == (signingKeyFile == "") {
		return c, fmt.Errorf("configure exactly one of FILES_SIGNING_KEY_ENV or FILES_SIGNING_KEY_FILE")
	}
	c.SigningKey, e = ReadFileSecret(FileSecretRef{Env: signingKeyEnv, File: signingKeyFile})
	if e != nil {
		return c, fmt.Errorf("FILES_SIGNING_KEY_ENV or FILES_SIGNING_KEY_FILE: %w", e)
	}
	if len(c.SigningKey) < 32 {
		return c, fmt.Errorf("FILES_SIGNING_KEY_ENV or FILES_SIGNING_KEY_FILE must reference a secret containing at least 32 bytes")
	}
	if err := c.Validate(); err != nil {
		return c, err
	}
	if err := validateWritableTempDir("FILES_TEMP_DIR", c.TempDir); err != nil {
		return c, err
	}
	return c, nil
}
func (c FilesConfig) Validate() error {
	if c.MaxBytes < 1 || c.MaxBytes > 5<<30 {
		return fmt.Errorf("FILES_MAX_BYTES must be between 1 byte and 5 GiB")
	}
	if c.PartBytes < 1 || c.PartBytes > c.MaxBytes {
		return fmt.Errorf("FILES_PART_BYTES must be positive and not exceed FILES_MAX_BYTES")
	}
	if c.UploadTTL <= 0 {
		return fmt.Errorf("FILES_UPLOAD_TTL must be positive")
	}
	if c.CleanupInterval <= 0 {
		return fmt.Errorf("FILES_CLEANUP_INTERVAL must be positive")
	}
	if c.ProcessingTimeout <= 0 {
		return fmt.Errorf("FILES_PROCESS_TIMEOUT must be positive")
	}
	if c.OwnerQuotaBytes < c.MaxBytes {
		return fmt.Errorf("FILES_OWNER_QUOTA_BYTES must be at least FILES_MAX_BYTES")
	}
	if c.MaxSessions < 1 {
		return fmt.Errorf("FILES_MAX_SESSIONS must be positive")
	}
	if c.ListLimit < 1 || c.ListLimit > 1000 {
		return fmt.Errorf("FILES_LIST_LIMIT must be between 1 and 1000")
	}
	if c.RateLimit < 1 {
		return fmt.Errorf("FILES_RATE_LIMIT must be positive")
	}
	if c.RateMaxClients < 1 {
		return fmt.Errorf("FILES_RATE_MAX_CLIENTS must be positive")
	}
	if c.RateWindow <= 0 {
		return fmt.Errorf("FILES_RATE_WINDOW must be positive")
	}
	if c.LinkMaxTTL <= 0 {
		return fmt.Errorf("FILES_LINK_MAX_TTL must be positive")
	}
	if len(c.SigningKey) < 32 {
		return fmt.Errorf("FILES_SIGNING_KEY_ENV or FILES_SIGNING_KEY_FILE must reference a secret containing at least 32 bytes")
	}
	if c.TempDir == "" {
		return fmt.Errorf("FILES_TEMP_DIR is required")
	}
	names := map[string]bool{}
	for _, s := range c.Services {
		if strings.TrimSpace(s.Name) != s.Name || s.Name == "" || len(s.Name) > 100 || names[s.Name] {
			return fmt.Errorf("FILE_SERVICES service names must be unique and contain 1-100 characters")
		}
		names[s.Name] = true
		if len(s.Keys) == 0 || len(s.Permissions) == 0 {
			return fmt.Errorf("FILE_SERVICES service %q requires keys and permissions", s.Name)
		}
		for _, p := range s.Permissions {
			switch p {
			case "upload", "read", "list", "delete", "link":
			default:
				return fmt.Errorf("FILE_SERVICES service %q has unknown file permission %q", s.Name, p)
			}
		}
		for _, r := range s.Keys {
			k, e := ReadFileSecret(r)
			if e != nil {
				return fmt.Errorf("FILE_SERVICES service %q key: %w", s.Name, e)
			}
			if len(k) < 24 {
				return fmt.Errorf("FILE_SERVICES service %q key must contain at least 24 bytes", s.Name)
			}
		}
	}
	return nil
}
func ReadFileSecret(r FileSecretRef) ([]byte, error) {
	if (r.Env == "") == (r.File == "") {
		return nil, fmt.Errorf("configure exactly one secret env or file")
	}
	var b []byte
	if r.Env != "" {
		v, ok := os.LookupEnv(r.Env)
		if !ok {
			return nil, fmt.Errorf("secret environment %s is missing", r.Env)
		}
		b = []byte(v)
	} else {
		var e error
		b, e = os.ReadFile(r.File)
		if e != nil {
			return nil, fmt.Errorf("cannot read configured secret file")
		}
	}
	return []byte(strings.TrimSpace(string(b))), nil
}
