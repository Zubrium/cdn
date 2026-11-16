package sitebuilder

import (
	"fmt"
	"strings"
)

// parseJSONPointer decodes an RFC 6901 JSON Pointer. The root pointer is not
// accepted because data-input mappings must target a concrete value.
func parseJSONPointer(pointer string) ([]string, error) {
	if pointer == "" || pointer[0] != '/' {
		return nil, fmt.Errorf("JSON Pointer must start with /")
	}
	raw := strings.Split(pointer[1:], "/")
	segments := make([]string, len(raw))
	for i, part := range raw {
		var decoded strings.Builder
		for j := 0; j < len(part); j++ {
			if part[j] != '~' {
				decoded.WriteByte(part[j])
				continue
			}
			if j+1 >= len(part) {
				return nil, fmt.Errorf("invalid JSON Pointer escape")
			}
			j++
			switch part[j] {
			case '0':
				decoded.WriteByte('~')
			case '1':
				decoded.WriteByte('/')
			default:
				return nil, fmt.Errorf("invalid JSON Pointer escape")
			}
		}
		segments[i] = decoded.String()
	}
	return segments, nil
}

func pointerKey(segments []string) string {
	parts := make([]string, len(segments))
	for i, part := range segments {
		part = strings.ReplaceAll(part, "~", "~0")
		parts[i] = strings.ReplaceAll(part, "/", "~1")
	}
	return "/" + strings.Join(parts, "/")
}
