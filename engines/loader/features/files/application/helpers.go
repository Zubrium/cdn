package application

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	core "github.com/Zubrium/cdn/core/platform"
	api "github.com/Zubrium/cdn/engines/loader/presentation"
	"github.com/google/uuid"
)

func exposeUpload(upload storedUpload, stored []storedPart, partSize int64) api.UploadSession {
	parts := make([]api.UploadPart, 0, len(stored))
	for _, part := range stored {
		if part.State == "ready" {
			parts = append(parts, api.UploadPart{Number: part.Number, Size: part.Size, SHA256: part.SHA256})
		}
	}
	sort.Slice(parts, func(i, j int) bool { return parts[i].Number < parts[j].Number })
	return api.UploadSession{ID: upload.ID, State: upload.State, PartSize: partSize, Parts: parts, ExpiresAt: upload.ExpiresAt, FileID: upload.FileID}
}

func hashInput(input api.BeginUploadInput) (string, error) {
	input.IdempotencyKey = ""
	encoded, err := json.Marshal(input)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

func sha256Hex(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func sha256HexBytes(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

func validDigest(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func stageID(uploadID string, part int) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(fmt.Sprintf("%s:%d", uploadID, part))).String()
}

func (r *countingReader) Read(buffer []byte) (int, error) {
	n, err := r.reader.Read(buffer)
	if n > 0 {
		r.count += int64(n)
		_, _ = r.hash.Write(buffer[:n])
	}
	return n, err
}

func clone(source map[string]string) map[string]string {
	if source == nil {
		return nil
	}
	out := make(map[string]string, len(source))
	for key, value := range source {
		out[key] = value
	}
	return out
}

func mapCoreError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, api.ErrInvalid), errors.Is(err, api.ErrTooLarge), errors.Is(err, api.ErrConflict), errors.Is(err, api.ErrNotFound):
		return err
	case errors.Is(err, core.ErrUnauthorized):
		return api.ErrUnauthorized
	case errors.Is(err, core.ErrForbidden):
		return api.ErrForbidden
	case errors.Is(err, core.ErrFileNotFound):
		return api.ErrNotFound
	case errors.Is(err, core.ErrRevisionConflict), errors.Is(err, core.ErrFileSetConflict), errors.Is(err, core.ErrRequestConflict), errors.Is(err, core.ErrUploadInProgress), errors.Is(err, core.ErrRequestDeleted):
		return api.ErrConflict
	case errors.Is(err, core.ErrChecksumMismatch):
		return api.ErrInvalid
	case errors.Is(err, core.ErrInvalidSearch):
		return api.ErrInvalid
	default:
		return fmt.Errorf("%w: Core operation failed", api.ErrUnavailable)
	}
}

func mapDataError(err error) error {
	switch {
	case errors.Is(err, core.ErrModuleDataNotFound):
		return api.ErrNotFound
	case errors.Is(err, core.ErrModuleDataConflict):
		return api.ErrConflict
	default:
		return fmt.Errorf("%w: Loader state operation failed", api.ErrUnavailable)
	}
}
