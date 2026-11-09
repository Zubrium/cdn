package config

import (
	"fmt"
	"os"
	"strconv"
)

type PaginationConfig struct {
	AdminPageSize           int
	PublicPageSize          int
	SubmissionExportMaxRows int
}

func LoadPagination() (PaginationConfig, error) {
	var cfg PaginationConfig
	for _, item := range []struct {
		key    string
		target *int
	}{{"ADMIN_PAGE_SIZE", &cfg.AdminPageSize}, {"PUBLIC_PAGE_SIZE", &cfg.PublicPageSize}, {"SUBMISSION_EXPORT_MAX_ROWS", &cfg.SubmissionExportMaxRows}} {
		key, target := item.key, item.target
		raw, ok := os.LookupEnv(key)
		if !ok || raw == "" {
			return cfg, fmt.Errorf("%s is required (see .env.example)", key)
		}
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 {
			return cfg, fmt.Errorf("%s must be a positive integer", key)
		}
		*target = value
	}
	if cfg.PublicPageSize > cfg.AdminPageSize {
		return cfg, fmt.Errorf("PUBLIC_PAGE_SIZE must not exceed ADMIN_PAGE_SIZE")
	}
	return cfg, nil
}
