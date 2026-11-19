package prerender

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"regexp"
	"strings"
	"unicode/utf8"
)

var (
	ErrInvalidPlan        = errors.New("prerender_invalid_plan")
	ErrPlanDigestMismatch = errors.New("prerender_plan_digest_mismatch")
	ErrInvalidResult      = errors.New("prerender_invalid_result")
	digestPattern         = regexp.MustCompile(`^[a-f0-9]{64}$`)
	identifierPattern     = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._:-]{0,127}$`)
)

// Limits are required operator configuration, never silently defaulted.
type Limits struct {
	MaxPlanBytes     int
	MaxArtifacts     int
	MaxArtifactBytes int64
	MaxTotalBytes    int64
}

func (limits Limits) Validate() error {
	if limits.MaxPlanBytes <= 0 || limits.MaxPlanBytes > 16<<20 || limits.MaxArtifacts <= 0 || limits.MaxArtifacts > 10000 ||
		limits.MaxArtifactBytes <= 0 || limits.MaxTotalBytes < limits.MaxArtifactBytes {
		return ErrInvalidPlan
	}
	return nil
}

// ValidatePlan verifies the bounded serialized plan envelope and its exact
// byte digest before any renderer is started. The plan body remains opaque;
// this package does not claim that it enforces semantic input projection.
func ValidatePlan(request Request, limits Limits) error {
	if limits.Validate() != nil || !utf8.Valid(request.Plan) || !validIdentifier(request.JobID) || !validIdentifier(request.IdempotencyKey) || len(request.Plan) == 0 || len(request.Plan) > limits.MaxPlanBytes ||
		!digestPattern.MatchString(request.PlanDigest) || !digestPattern.MatchString(request.InputDigest) {
		return ErrInvalidPlan
	}
	sum := sha256.Sum256(request.Plan)
	if hex.EncodeToString(sum[:]) != request.PlanDigest {
		return ErrPlanDigestMismatch
	}
	decoder := json.NewDecoder(bytes.NewReader(request.Plan))
	decoder.UseNumber()
	value, err := readUniqueJSONValue(decoder, 0)
	if err != nil {
		return ErrInvalidPlan
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return ErrInvalidPlan
	}
	envelope, ok := value.(map[string]any)
	if !ok || len(envelope) != 3 {
		return ErrInvalidPlan
	}
	version, versionOK := envelope["version"].(string)
	inputDigest, digestOK := envelope["input_digest"].(string)
	payload, payloadOK := envelope["payload"].(map[string]any)
	if !versionOK || version != RenderPlanVersion || !digestOK || inputDigest != request.InputDigest || !payloadOK || payload == nil {
		return ErrInvalidPlan
	}
	return nil
}

// readUniqueJSONValue rejects duplicate object keys at every depth. Standard
// encoding/json decoding otherwise silently accepts duplicates, making signed
// or digested plans ambiguous to different consumers.
func readUniqueJSONValue(decoder *json.Decoder, depth int) (any, error) {
	if depth > 64 {
		return nil, ErrInvalidPlan
	}
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	delim, isDelim := token.(json.Delim)
	if !isDelim {
		return token, nil
	}
	switch delim {
	case '{':
		object := make(map[string]any)
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return nil, err
			}
			key, ok := keyToken.(string)
			if !ok {
				return nil, ErrInvalidPlan
			}
			if _, exists := object[key]; exists {
				return nil, ErrInvalidPlan
			}
			value, err := readUniqueJSONValue(decoder, depth+1)
			if err != nil {
				return nil, err
			}
			object[key] = value
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim('}') {
			return nil, ErrInvalidPlan
		}
		return object, nil
	case '[':
		array := make([]any, 0)
		for decoder.More() {
			value, err := readUniqueJSONValue(decoder, depth+1)
			if err != nil {
				return nil, err
			}
			array = append(array, value)
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim(']') {
			return nil, ErrInvalidPlan
		}
		return array, nil
	default:
		return nil, ErrInvalidPlan
	}
}

// ValidateResult checks that a completed artifact set is bounded, unique,
// checksummed, and tied to this exact input digest. It does not activate it.
func ValidateResult(limits Limits, expectedInputDigest string, result Result) error {
	if limits.Validate() != nil || !digestPattern.MatchString(expectedInputDigest) || !validIdentifier(result.Manifest.FileID) || result.Manifest.ContentType != "application/json" ||
		result.InputDigest != expectedInputDigest || len(result.Artifacts)+1 > limits.MaxArtifacts {
		return ErrInvalidResult
	}
	seen := make(map[string]struct{}, len(result.Artifacts)+1)
	artifacts := make([]Artifact, 0, len(result.Artifacts)+1)
	artifacts = append(artifacts, result.Manifest)
	artifacts = append(artifacts, result.Artifacts...)
	var total int64
	documentCount := 0
	for _, artifact := range artifacts {
		if !validIdentifier(artifact.FileID) || !digestPattern.MatchString(artifact.Checksum) || !validArtifactContentType(artifact.ContentType) || artifact.Size <= 0 || artifact.Size > limits.MaxArtifactBytes ||
			artifact.Size > limits.MaxTotalBytes-total {
			return ErrInvalidResult
		}
		if mediaType, _, _ := mime.ParseMediaType(artifact.ContentType); mediaType == "text/html" {
			documentCount++
		}
		if _, duplicate := seen[artifact.FileID]; duplicate {
			return ErrInvalidResult
		}
		seen[artifact.FileID] = struct{}{}
		total += artifact.Size
	}
	if documentCount != 1 {
		return ErrInvalidResult
	}
	return nil
}

func validArtifactContentType(value string) bool {
	if value == "" || len(value) > 127 || strings.ContainsAny(value, "\r\n") {
		return false
	}
	mediaType, parameters, err := mime.ParseMediaType(value)
	if err != nil || mediaType == "" {
		return false
	}
	switch mediaType {
	case "text/html", "text/css", "text/javascript", "application/javascript", "application/json",
		"image/jpeg", "image/png", "image/webp", "image/avif", "image/gif",
		"font/woff", "font/woff2", "application/font-woff":
	default:
		return false
	}
	if len(parameters) == 0 {
		return true
	}
	return len(parameters) == 1 && parameters["charset"] == "utf-8" &&
		(mediaType == "text/html" || mediaType == "text/css" || mediaType == "text/javascript" || mediaType == "application/javascript")
}

func validIdentifier(value string) bool { return identifierPattern.MatchString(value) }

func PlanDigest(plan []byte) string {
	sum := sha256.Sum256(plan)
	return hex.EncodeToString(sum[:])
}
