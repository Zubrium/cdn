package files

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/google/uuid"
)

// UploadStream adapts ordinary uploads to the resumable transaction. It
// reserves the requested quota before reading or staging any caller bytes.
func (s *Service) UploadStream(ctx context.Context, owner Owner, name string, size int64, visibility string, body io.Reader) (File, error) {
	if size < 1 || size > s.cfg.MaxBytes {
		return File{}, ErrInvalid
	}
	u, err := s.CreateUpload(ctx, owner, UploadInput{
		Name: name, Size: size, SHA256: strings.Repeat("0", sha256.Size*2), Visibility: visibility,
	})
	if err != nil {
		return File{}, err
	}
	return s.UploadReservedStream(ctx, owner, u.ID, name, size, visibility, body)
}

// UploadReservedStream stages a body only after an upload reservation exists,
// then shrinks that reservation to the verified file size before sending parts.
func (s *Service) UploadReservedStream(ctx context.Context, owner Owner, reservationID uuid.UUID, name string, size int64, visibility string, body io.Reader) (File, error) {
	reserved, err := s.UploadStatus(ctx, owner, reservationID)
	if err != nil {
		return File{}, err
	}
	if size < 1 || size > s.cfg.MaxBytes || size > reserved.Size {
		_ = s.Abort(context.Background(), owner, reservationID)
		return File{}, ErrInvalid
	}
	failed := true
	defer func() {
		if failed {
			_ = s.Abort(context.Background(), owner, reservationID)
		}
	}()
	file, err := os.CreateTemp(s.cfg.TempDir, "engine-upload-*")
	if err != nil {
		return File{}, err
	}
	defer func() { _ = file.Close(); _ = os.Remove(file.Name()) }()
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(file, hash), io.LimitReader(body, size+1))
	if err != nil {
		return File{}, err
	}
	if n != size {
		return File{}, fmt.Errorf("%w: upload length mismatch", ErrInvalid)
	}
	if err := file.Sync(); err != nil {
		return File{}, err
	}
	if _, err := s.FinalizeUpload(ctx, owner, reservationID, UploadInput{
		Name: name, Size: size, SHA256: hex.EncodeToString(hash.Sum(nil)), Visibility: visibility,
	}); err != nil {
		return File{}, err
	}
	for offset := int64(0); offset < size; offset += reserved.PartSize {
		partSize := min(reserved.PartSize, size-offset)
		partHash := sha256.New()
		if _, err := io.Copy(partHash, io.NewSectionReader(file, offset, partSize)); err != nil {
			return File{}, err
		}
		_, err := s.PutPart(ctx, owner, reservationID, int(offset/reserved.PartSize)+1, partSize, hex.EncodeToString(partHash.Sum(nil)), io.NewSectionReader(file, offset, partSize))
		if err != nil {
			return File{}, err
		}
	}
	result, err := s.Commit(ctx, owner, reservationID)
	if err != nil {
		return File{}, err
	}
	failed = false
	return result, nil
}
