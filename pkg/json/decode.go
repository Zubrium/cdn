// Package httpjson provides strict, bounded JSON decoding for HTTP presentation
// modules. It deliberately has no dependencies on engines or Core contracts.
package json

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"unicode/utf8"
)

var (
	ErrInvalid  = errors.New("invalid JSON request")
	ErrTooLarge = errors.New("JSON request exceeds its size limit")
)

const maxNestingDepth = 64

// DecodeRequest decodes one application/json request body, enforcing the
// supplied byte limit, UTF-8 validity, unique object keys, known struct fields,
// and the absence of trailing JSON values.
func DecodeRequest(w http.ResponseWriter, r *http.Request, maxBytes int64, target any) error {
	if w == nil || r == nil || r.Body == nil || maxBytes <= 0 || target == nil {
		return ErrInvalid
	}
	contentTypes := r.Header.Values("Content-Type")
	if len(contentTypes) != 1 || strings.TrimSpace(contentTypes[0]) != contentTypes[0] {
		return ErrInvalid
	}
	mediaType, _, err := mime.ParseMediaType(contentTypes[0])
	if err != nil || mediaType != "application/json" {
		return ErrInvalid
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return ErrTooLarge
		}
		return ErrInvalid
	}
	return DecodeBytes(raw, target)
}

// DecodeBytes decodes one strict JSON object from raw. The caller is
// responsible for applying an input-size limit before calling this function.
func DecodeBytes(raw []byte, target any) error {
	trimmed := bytes.TrimSpace(raw)
	if target == nil || len(trimmed) == 0 || trimmed[0] != '{' || !ValidJSON(raw) {
		return ErrInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return ErrInvalid
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return ErrInvalid
	}
	return nil
}

// ValidJSON reports whether raw contains exactly one UTF-8 JSON value without
// duplicate object keys or excessive nesting.
func ValidJSON(raw []byte) bool {
	return len(raw) > 0 && utf8.Valid(raw) && validUniqueJSON(raw)
}

// ValidObject reports whether raw is a bounded, unique-key JSON object.
func ValidObject(raw []byte, maxBytes int) bool {
	return validBoundedComposite(raw, maxBytes, '{')
}

// ValidArray reports whether raw is a bounded, unique-key JSON array.
func ValidArray(raw []byte, maxBytes int) bool {
	return validBoundedComposite(raw, maxBytes, '[')
}

func validBoundedComposite(raw []byte, maxBytes int, opening byte) bool {
	if maxBytes <= 0 || len(raw) == 0 || len(raw) > maxBytes || !utf8.Valid(raw) {
		return false
	}
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && trimmed[0] == opening && ValidJSON(trimmed)
}

func validUniqueJSON(raw []byte) bool {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := consumeJSONValue(decoder, 1); err != nil {
		return false
	}
	var trailing any
	return errors.Is(decoder.Decode(&trailing), io.EOF)
}

func consumeJSONValue(decoder *json.Decoder, depth int) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, composite := token.(json.Delim)
	if !composite {
		return nil
	}
	if depth > maxNestingDepth {
		return ErrInvalid
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return ErrInvalid
			}
			if _, duplicate := seen[key]; duplicate {
				return ErrInvalid
			}
			seen[key] = struct{}{}
			if err := consumeJSONValue(decoder, depth+1); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim('}') {
			return ErrInvalid
		}
	case '[':
		for decoder.More() {
			if err := consumeJSONValue(decoder, depth+1); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim(']') {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	return nil
}
