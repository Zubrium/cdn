package storage

import (
	"encoding/json"
	"mime"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Zubrium/cdn/core/access"
	"github.com/google/uuid"
)

func ValidName(name string) bool {
	if name == "" || len(name) > 255 || !utf8.ValidString(name) || strings.TrimSpace(name) != name || name == "." || name == ".." {
		return false
	}
	for _, r := range name {
		if unicode.IsControl(r) || r == '/' || r == '\\' {
			return false
		}
	}
	return true
}

func ValidContentType(value string) bool {
	if value == "" || len(value) > 255 || strings.ContainsAny(value, "\r\n") {
		return false
	}
	mediaType, _, err := mime.ParseMediaType(value)
	return err == nil && strings.Contains(mediaType, "/")
}

func ValidMetadata(metadata map[string]string, maxBytes int64) bool {
	if maxBytes <= 0 {
		return false
	}
	// Bound work before JSON encoding so a caller cannot force an oversized
	// temporary allocation that will only be rejected after marshaling.
	approximateBytes := int64(2)
	for key, value := range metadata {
		if key == "" || len(key) > 128 || !utf8.ValidString(key) || !utf8.ValidString(value) {
			return false
		}
		addition := int64(len(key)) + int64(len(value)) + 6
		if addition > maxBytes-approximateBytes {
			return false
		}
		approximateBytes += addition
		for _, r := range key {
			if unicode.IsControl(r) {
				return false
			}
		}
		if key == "name" && !ValidName(value) {
			return false
		}
	}
	encoded, err := json.Marshal(metadata)
	return err == nil && int64(len(encoded)) <= maxBytes
}

func ValidGroupKey(value string) bool {
	return access.ValidGroupKey(value)
}

func ValidGroups(groups []string) bool {
	if len(groups) == 0 {
		return false
	}
	seen := make(map[string]struct{}, len(groups))
	for _, group := range groups {
		if !ValidGroupKey(group) {
			return false
		}
		if _, exists := seen[group]; exists {
			return false
		}
		seen[group] = struct{}{}
	}
	return true
}

func ValidUUID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id != uuid.Nil && id.String() == value
}
