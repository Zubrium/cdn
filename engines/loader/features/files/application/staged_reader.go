package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"hash"
	"io"

	core "github.com/Zubrium/cdn/core/presentation"
)

type stagedReader struct {
	ctx      context.Context
	files    Files
	session  core.Session
	parts    []storedPart
	index    int
	current  io.ReadCloser
	currentN int64
	currentH hash.Hash
	closed   bool
}

func newHashState() hash.Hash { return sha256.New() }

func (r *stagedReader) open() error {
	if r.index >= len(r.parts) {
		return io.EOF
	}
	part := r.parts[r.index]
	reader, size, checksum, err := r.files.ReadStagedObject(r.ctx, r.session, part.Stage)
	if err != nil {
		return err
	}
	if reader == nil || size != part.Size || checksum != part.SHA256 {
		if reader != nil {
			_ = reader.Close()
		}
		return errors.New("staged part metadata mismatch")
	}
	r.current = reader
	r.currentN = 0
	r.currentH = newHashState()
	return nil
}

func (r *stagedReader) Read(buffer []byte) (int, error) {
	if r.closed {
		return 0, errors.New("read from closed staged stream")
	}
	for {
		if r.current == nil {
			if err := r.open(); err != nil {
				return 0, err
			}
		}
		n, err := r.current.Read(buffer)
		if n > 0 {
			r.currentN += int64(n)
			_, _ = r.currentH.Write(buffer[:n])
		}
		if err == nil {
			return n, nil
		}
		if err != io.EOF {
			return n, err
		}
		part := r.parts[r.index]
		closeErr := r.current.Close()
		digest := hex.EncodeToString(r.currentH.Sum(nil))
		readSize := r.currentN
		r.current, r.currentH, r.currentN = nil, nil, 0
		r.index++
		if closeErr != nil {
			return n, closeErr
		}
		if readSize != part.Size || digest != part.SHA256 {
			return n, errors.New("staged part checksum mismatch")
		}
		if n > 0 {
			return n, nil
		}
	}
}

func (r *stagedReader) Close() error {
	if r.closed {
		return nil
	}
	r.closed = true
	if r.current != nil {
		return r.current.Close()
	}
	return nil
}
