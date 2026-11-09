package files

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
)

// UploadStream adapts ordinary admin multipart uploads to the same resumable
// transaction. Spooling uses disk; no allocation grows with file size.
func (s *Service) UploadStream(ctx context.Context, owner Owner, name string, size int64, visibility string, body io.Reader) (File, error) {
	if size < 1 || size > s.cfg.MaxBytes {
		return File{}, ErrInvalid
	}
	file, e := os.CreateTemp(s.cfg.TempDir, "engine-upload-*")
	if e != nil {
		return File{}, e
	}
	defer func() { _ = file.Close(); _ = os.Remove(file.Name()) }()
	hash := sha256.New()
	n, e := io.Copy(io.MultiWriter(file, hash), io.LimitReader(body, size+1))
	if e != nil {
		return File{}, e
	}
	if n != size {
		return File{}, fmt.Errorf("%w: upload length mismatch", ErrInvalid)
	}
	u, e := s.CreateUpload(ctx, owner, UploadInput{Name: name, Size: size, SHA256: hex.EncodeToString(hash.Sum(nil)), Visibility: visibility})
	if e != nil {
		return File{}, e
	}
	for offset := int64(0); offset < size; offset += u.PartSize {
		partSize := min(u.PartSize, size-offset)
		partHash := sha256.New()
		if _, e = io.Copy(partHash, io.NewSectionReader(file, offset, partSize)); e != nil {
			_ = s.Abort(context.Background(), owner, u.ID)
			return File{}, e
		}
		_, e = s.PutPart(ctx, owner, u.ID, int(offset/u.PartSize)+1, partSize, hex.EncodeToString(partHash.Sum(nil)), io.NewSectionReader(file, offset, partSize))
		if e != nil {
			_ = s.Abort(context.Background(), owner, u.ID)
			return File{}, e
		}
	}
	f, e := s.Commit(ctx, owner, u.ID)
	if e != nil {
		_ = s.Abort(context.Background(), owner, u.ID)
	}
	return f, e
}
