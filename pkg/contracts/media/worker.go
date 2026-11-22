package media

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strings"
)

const WorkContractVersion = 2

var (
	ErrInvalidWork        = errors.New("invalid media work contract")
	uuidPattern           = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	sha256Pattern         = regexp.MustCompile(`^[a-f0-9]{64}$`)
	idempotencyKeyPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{16,128}$`)
	processorPattern      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)
)

// WorkRequest is the canonical, versioned worker payload emitted by the
// Website media workflow. It contains no executable, command, path, URL or
// caller-selected processing argument; the worker derives every FFmpeg
// argument from the pinned canonical Profile.
type WorkRequest struct {
	Version          int     `json:"version"`
	JobID            string  `json:"job_id"`
	SnapshotID       string  `json:"snapshot_id"`
	PlacementID      string  `json:"placement_id"`
	FieldPath        string  `json:"field_path"`
	SourceFileID     string  `json:"source_file_id"`
	SourceSHA256     string  `json:"source_sha256"`
	ProfileDigest    string  `json:"profile_digest"`
	ProcessorVersion string  `json:"processor_version"`
	OutputName       string  `json:"output_name"`
	IdempotencyKey   string  `json:"idempotency_key"`
	InputDigest      string  `json:"input_digest"`
	Profile          Profile `json:"profile"`
}

// WorkResult is emitted only after one immutable derivative has been created
// and verified. Website publishes outputs only after every child result in the
// job has been verified.
type WorkResult struct {
	Version          int    `json:"version"`
	Ready            bool   `json:"ready"`
	OutputName       string `json:"output_name"`
	FileID           string `json:"file_id"`
	Checksum         string `json:"checksum"`
	ContentType      string `json:"content_type"`
	Size             int64  `json:"size"`
	ProfileDigest    string `json:"profile_digest"`
	ProcessorVersion string `json:"processor_version"`
	Fingerprint      string `json:"fingerprint"`
}

// ValidateWorkRequest strictly decodes one worker task payload and recomputes
// its canonical digest. A changed profile or processor version can never be
// presented under an existing job identity.
func ValidateWorkRequest(raw []byte) (WorkRequest, error) {
	var request WorkRequest
	if err := decodeStrict(raw, &request); err != nil {
		return WorkRequest{}, ErrInvalidWork
	}
	encoded, err := json.Marshal(request.Profile)
	if err != nil {
		return WorkRequest{}, ErrInvalidWork
	}
	profile, err := ValidateProfile(encoded)
	if err != nil {
		return WorkRequest{}, ErrInvalidWork
	}
	request.Profile = profile
	digest, err := Digest(profile, request.ProcessorVersion)
	if err != nil || digest != request.ProfileDigest {
		return WorkRequest{}, ErrInvalidWork
	}
	if request.Version != WorkContractVersion || !uuidPattern.MatchString(request.JobID) ||
		!uuidPattern.MatchString(request.SnapshotID) || !uuidPattern.MatchString(request.SourceFileID) ||
		!processorPattern.MatchString(request.PlacementID) || !validFieldPath(request.FieldPath) ||
		!sha256Pattern.MatchString(request.SourceSHA256) || !sha256Pattern.MatchString(request.InputDigest) ||
		!renditionNamePattern.MatchString(request.OutputName) || !idempotencyKeyPattern.MatchString(request.IdempotencyKey) ||
		!processorPattern.MatchString(request.ProcessorVersion) {
		return WorkRequest{}, ErrInvalidWork
	}
	outputs := Outputs(profile)
	found := false
	for _, name := range outputs {
		if name == request.OutputName {
			found = true
			break
		}
	}
	if !found {
		return WorkRequest{}, ErrInvalidWork
	}
	return request, nil
}

// ValidateWorkResult strictly decodes one bounded task result. It does not
// establish that the referenced Core file exists; Website performs that check
// through Core before publishing any output.
func ValidateWorkResult(raw []byte) (WorkResult, error) {
	var result WorkResult
	if err := decodeStrict(raw, &result); err != nil {
		return WorkResult{}, ErrInvalidWork
	}
	if result.Version != WorkContractVersion || !result.Ready ||
		!renditionNamePattern.MatchString(result.OutputName) || !uuidPattern.MatchString(result.FileID) ||
		!sha256Pattern.MatchString(result.Checksum) || result.ContentType == "" || len(result.ContentType) > 127 ||
		strings.ContainsAny(result.ContentType, "\x00\r\n;") || result.Size <= 0 ||
		!sha256Pattern.MatchString(result.ProfileDigest) || !processorPattern.MatchString(result.ProcessorVersion) ||
		!sha256Pattern.MatchString(result.Fingerprint) {
		return WorkResult{}, ErrInvalidWork
	}
	return result, nil
}

func decodeStrict(raw []byte, target any) error {
	if len(raw) == 0 || len(raw) > MaxFieldBytes {
		return ErrInvalidWork
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if !errors.Is(decoder.Decode(&trailing), io.EOF) {
		return ErrInvalidWork
	}
	return nil
}

func validFieldPath(path string) bool {
	if path == "" || len(path) > 512 || !strings.HasPrefix(path, "/") {
		return false
	}
	segments := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if len(segments) < 1 || len(segments) > 2 {
		return false
	}
	for _, segment := range segments {
		if !processorPattern.MatchString(segment) || strings.Contains(segment, "..") {
			return false
		}
	}
	return true
}
