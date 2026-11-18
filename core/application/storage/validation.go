package storage

import (
	"encoding/hex"
	"mime"
	"strings"
	"unicode"
	"unicode/utf8"

	appaccess "github.com/Zubrium/cdn/core/application/access"
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
	encodedBytes := int64(2)
	for key, value := range metadata {
		if key == "" || len(key) > 128 || !utf8.ValidString(key) || !utf8.ValidString(value) || strings.ContainsRune(value, '\x00') {
			return false
		}
		keyBytes := jsonEncodedStringBytes(key)
		separatorBytes := int64(3)
		if encodedBytes > 2 {
			separatorBytes++
		}
		remaining := maxBytes - encodedBytes
		if keyBytes+separatorBytes > remaining || int64(len(value)) > remaining-keyBytes-separatorBytes {
			return false
		}
		addition := keyBytes + jsonEncodedStringBytes(value) + 1
		if encodedBytes > 2 {
			addition++
		}
		if addition > maxBytes-encodedBytes {
			return false
		}
		encodedBytes += addition
		for _, r := range key {
			if unicode.IsControl(r) {
				return false
			}
		}
		if key == "name" && !ValidName(value) {
			return false
		}
	}
	return encodedBytes <= maxBytes
}

func jsonEncodedStringBytes(value string) int64 {
	encodedBytes := int64(2)
	for _, char := range value {
		switch {
		case char == '"' || char == '\\' || char == '\b' || char == '\f' || char == '\n' || char == '\r' || char == '\t':
			encodedBytes += 2
		case char < 0x20 || char == '<' || char == '>' || char == '&' || char == '\u2028' || char == '\u2029':
			encodedBytes += 6
		default:
			encodedBytes += int64(utf8.RuneLen(char))
		}
	}
	return encodedBytes
}

func ValidGroups(groups []string) bool {
	if len(groups) == 0 {
		return false
	}
	seen := make(map[string]struct{}, len(groups))
	for _, group := range groups {
		if !appaccess.ValidGroupKey(group) {
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

func ValidSHA256(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32 && hex.EncodeToString(decoded) == value
}
