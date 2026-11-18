package coordinator

import (
	"encoding/json"
	"strings"
	"unicode"
	"unicode/utf8"
)

const maxWorkflowPayloadBytes = 1 << 20

func validCoordinatorIdempotencyKey(value string) bool {
	if value == "" || len(value) > 256 || !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return false
	}
	for _, char := range value {
		if unicode.IsControl(char) {
			return false
		}
	}
	return true
}

func validWorkflowPayload(payload json.RawMessage) bool {
	if len(payload) == 0 || len(payload) > maxWorkflowPayloadBytes || !utf8.Valid(payload) || !json.Valid(payload) {
		return false
	}
	var object map[string]json.RawMessage
	return json.Unmarshal(payload, &object) == nil && object != nil
}
