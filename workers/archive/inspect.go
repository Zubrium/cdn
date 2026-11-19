// Package archive implements bounded ZIP inspection without storage access.
// Path and archive limits enforce the archive ingestion contract.
package archive

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"unicode/utf8"
)

type Limits struct {
	MaxArchiveBytes     int64
	MaxEntries          int
	MaxEntryBytes       int64
	MaxExpandedBytes    int64
	MaxCompressionRatio uint64
}

type File struct {
	Path   string
	Size   int64
	SHA256 string
}

const maxPortableArchivePathBytes = 1024

// Inspect verifies every entry and its checksum before returning metadata.
// It does not persist files: writing outputs and durable job recovery belong
// to the worker application which will use Core capabilities.
func Inspect(ctx context.Context, input io.ReaderAt, size int64, limits Limits) ([]File, error) {
	if ctx == nil || input == nil || size <= 0 || limits.MaxArchiveBytes <= 0 || limits.MaxArchiveBytes == int64(^uint64(0)>>1) || size > limits.MaxArchiveBytes || limits.MaxEntries <= 0 || limits.MaxEntryBytes <= 0 || limits.MaxEntryBytes == int64(^uint64(0)>>1) || limits.MaxExpandedBytes <= 0 || limits.MaxCompressionRatio == 0 {
		return nil, errors.New("invalid archive input or limits")
	}
	archive, err := zip.NewReader(input, size)
	if err != nil {
		return nil, fmt.Errorf("read ZIP: %w", err)
	}
	if len(archive.File) == 0 || len(archive.File) > limits.MaxEntries {
		return nil, errors.New("invalid archive entry count")
	}
	files := make([]File, 0, len(archive.File))
	paths := make(map[string]archivePath, len(archive.File))
	var total int64
	for _, entry := range archive.File {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		name := strings.TrimSuffix(entry.Name, "/")
		if !validPath(name) {
			return nil, errors.New("unsafe archive path")
		}
		isDir := entry.FileInfo().IsDir()
		if err := registerArchivePath(paths, name, isDir); err != nil {
			return nil, err
		}
		if isDir {
			if entry.UncompressedSize64 != 0 || entry.CompressedSize64 != 0 {
				return nil, errors.New("archive directory contains data")
			}
			continue
		}
		if !entry.Mode().IsRegular() {
			return nil, errors.New("archive entry is not a regular file")
		}
		if entry.UncompressedSize64 > uint64(limits.MaxEntryBytes) || entry.UncompressedSize64 > uint64(limits.MaxExpandedBytes-total) {
			return nil, errors.New("archive expanded size limit exceeded")
		}
		if ratioExceeded(entry.UncompressedSize64, entry.CompressedSize64, limits.MaxCompressionRatio) {
			return nil, errors.New("archive compression ratio exceeded")
		}
		reader, err := entry.Open()
		if err != nil {
			return nil, fmt.Errorf("open archive entry: %w", err)
		}
		hash := sha256.New()
		count, readErr := io.Copy(hash, io.LimitReader(&contextReader{ctx: ctx, reader: reader}, limits.MaxEntryBytes+1))
		closeErr := reader.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read archive entry: %w", readErr)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("close archive entry: %w", closeErr)
		}
		if count > limits.MaxEntryBytes || uint64(count) != entry.UncompressedSize64 {
			return nil, errors.New("archive entry size mismatch")
		}
		total += count
		files = append(files, File{Path: name, Size: count, SHA256: hex.EncodeToString(hash.Sum(nil))})
	}
	if len(files) == 0 {
		return nil, errors.New("archive contains no files")
	}
	return files, nil
}

type archivePath struct {
	name      string
	directory bool
	explicit  bool
}

func registerArchivePath(paths map[string]archivePath, name string, isDir bool) error {
	parts := strings.Split(name, "/")
	prefix := ""
	for index, part := range parts {
		if prefix == "" {
			prefix = part
		} else {
			prefix += "/" + part
		}
		key := strings.ToLower(prefix)
		final := index == len(parts)-1
		directory := !final || isDir
		existing, ok := paths[key]
		if ok {
			if existing.name != prefix {
				return errors.New("ambiguous archive path")
			}
			if !existing.directory {
				return errors.New("archive file path conflicts with a directory")
			}
			if final {
				if !isDir || existing.explicit {
					return errors.New("duplicate archive path")
				}
				existing.explicit = true
				paths[key] = existing
			}
			continue
		}
		paths[key] = archivePath{name: prefix, directory: directory, explicit: final}
	}
	return nil
}

func ratioExceeded(uncompressed, compressed, maximum uint64) bool {
	if uncompressed == 0 {
		return false
	}
	if compressed == 0 {
		return true
	}
	quotient := uncompressed / compressed
	return quotient > maximum || quotient == maximum && uncompressed%compressed != 0
}

func validPath(name string) bool {
	return name != "" && len(name) <= maxPortableArchivePathBytes && utf8.ValidString(name) && !strings.ContainsAny(name, "\\\x00") && !strings.HasPrefix(name, "/") && !hasWindowsDrivePrefix(name) && path.Clean(name) == name && name != "." && name != ".." && !strings.HasPrefix(name, "../")
}

func hasWindowsDrivePrefix(name string) bool {
	if len(name) < 2 || name[1] != ':' {
		return false
	}
	letter := name[0]
	return letter >= 'A' && letter <= 'Z' || letter >= 'a' && letter <= 'z'
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}
