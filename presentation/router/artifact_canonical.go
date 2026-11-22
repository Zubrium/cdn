package router

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"unicode/utf8"

	engine "github.com/Zubrium/cdn/engines/router/presentation"
)

const canonicalArtifactMaxNesting = 64

// canonicalArtifactPayload is the Router-side implementation of the shared
// static-page-artifact/v1 canonical payload contract: the manifest object
// without its "digest" field, with object keys sorted at every level and no
// insignificant whitespace. It is representation-independent, so the same
// payload yields the same bytes whether the manifest was produced from a map
// or from a struct. The Website runtime owns an identical implementation;
// engines cannot be imported by this Presentation module, so the equivalence
// is enforced by the cross-module conformance test.
func canonicalArtifactPayload(data []byte) ([]byte, error) {
	if len(data) == 0 || !utf8.Valid(data) {
		return nil, engine.ErrInvalidTarget
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	value, err := decodeUniqueArtifactJSON(decoder, 1)
	if err != nil || ensureNoTrailingArtifactJSON(decoder) != nil {
		return nil, engine.ErrInvalidTarget
	}
	payload, ok := value.(map[string]any)
	if !ok {
		return nil, engine.ErrInvalidTarget
	}
	delete(payload, "digest")
	canonical, err := json.Marshal(payload)
	if err != nil {
		return nil, engine.ErrInvalidTarget
	}
	return canonical, nil
}

func artifactPayloadDigest(data []byte) (string, error) {
	canonical, err := canonicalArtifactPayload(data)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func decodeUniqueArtifactJSON(decoder *json.Decoder, depth int) (any, error) {
	if depth > canonicalArtifactMaxNesting {
		return nil, errors.New("JSON nesting too deep")
	}
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	delimiter, composite := token.(json.Delim)
	if !composite {
		return token, nil
	}
	switch delimiter {
	case '{':
		object := make(map[string]any)
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return nil, err
			}
			key, ok := keyToken.(string)
			if !ok {
				return nil, errors.New("JSON object key")
			}
			if _, duplicate := object[key]; duplicate {
				return nil, errors.New("duplicate JSON key")
			}
			value, err := decodeUniqueArtifactJSON(decoder, depth+1)
			if err != nil {
				return nil, err
			}
			object[key] = value
		}
		if closing, err := decoder.Token(); err != nil || closing != json.Delim('}') {
			return nil, errors.New("JSON object close")
		}
		return object, nil
	case '[':
		array := make([]any, 0)
		for decoder.More() {
			value, err := decodeUniqueArtifactJSON(decoder, depth+1)
			if err != nil {
				return nil, err
			}
			array = append(array, value)
		}
		if closing, err := decoder.Token(); err != nil || closing != json.Delim(']') {
			return nil, errors.New("JSON array close")
		}
		return array, nil
	default:
		return nil, errors.New("unexpected JSON delimiter")
	}
}

func ensureNoTrailingArtifactJSON(decoder *json.Decoder) error {
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON data")
	}
	return nil
}
