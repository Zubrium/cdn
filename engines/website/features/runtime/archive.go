package runtime

import (
	"archive/zip"
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strings"
	"unicode/utf8"
)

var (
	ErrArchive      = errors.New("invalid feature archive")
	ErrArchiveLimit = errors.New("feature archive limit exceeded")
)

// ArchiveLimits bounds untrusted ZIP input before any output file is created.
type ArchiveLimits struct {
	MaxArchiveBytes     int64
	MaxEntries          int
	MaxEntryBytes       int64
	MaxExpandedBytes    int64
	MaxCompressionRatio int
	MaxManifestBytes    int64
}

// DefaultArchiveLimits are the conservative bounds used by the runtime
// preflight. A composition may tighten them but never disable them.
func DefaultArchiveLimits() ArchiveLimits {
	return ArchiveLimits{
		MaxArchiveBytes:     64 << 20,
		MaxEntries:          4096,
		MaxEntryBytes:       16 << 20,
		MaxExpandedBytes:    256 << 20,
		MaxCompressionRatio: 200,
		MaxManifestBytes:    maxManifestBytes,
	}
}

// ArchiveFile is one already-verified regular entry.
type ArchiveFile struct {
	Path string
	Size int64
	Mode os.FileMode
}

// PreflightArchive validates untrusted package bytes in memory. It rejects
// malformed archives, traversal (including Windows separators), symlinks,
// duplicate normalized paths, unbounded expansion and an invalid manifest.
// No output file, catalog entry or FileSet membership is touched here.
func PreflightArchive(data []byte, limits ArchiveLimits) ([]ArchiveFile, Manifest, error) {
	if limits.MaxArchiveBytes <= 0 || limits.MaxEntries <= 0 || limits.MaxEntryBytes <= 0 ||
		limits.MaxExpandedBytes <= 0 || limits.MaxCompressionRatio <= 0 || limits.MaxManifestBytes <= 0 {
		return nil, Manifest{}, fmt.Errorf("%w: limits must be positive", ErrArchive)
	}
	if len(data) == 0 || int64(len(data)) > limits.MaxArchiveBytes {
		return nil, Manifest{}, fmt.Errorf("%w: archive size", ErrArchiveLimit)
	}
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, Manifest{}, fmt.Errorf("%w: %v", ErrArchive, err)
	}
	if len(reader.File) > limits.MaxEntries {
		return nil, Manifest{}, fmt.Errorf("%w: entry count", ErrArchiveLimit)
	}

	files := make([]ArchiveFile, 0, len(reader.File))
	seen := make(map[string]struct{}, len(reader.File))
	var expanded int64
	var manifestBytes []byte
	for _, entry := range reader.File {
		if !utf8.ValidString(entry.Name) || !validArchiveEntryPath(entry.Name) {
			return nil, Manifest{}, fmt.Errorf("%w: path %q", ErrArchive, entry.Name)
		}
		clean := path.Clean(entry.Name)
		if _, duplicate := seen[clean]; duplicate {
			return nil, Manifest{}, fmt.Errorf("%w: duplicate path %q", ErrArchive, entry.Name)
		}
		seen[clean] = struct{}{}
		mode := entry.Mode()
		if mode&os.ModeSymlink != 0 || mode&os.ModeDevice != 0 || mode&os.ModeNamedPipe != 0 || !entry.FileInfo().Mode().IsRegular() {
			return nil, Manifest{}, fmt.Errorf("%w: non-regular entry %q", ErrArchive, entry.Name)
		}
		if entry.UncompressedSize64 > uint64(limits.MaxEntryBytes) {
			return nil, Manifest{}, fmt.Errorf("%w: entry %q", ErrArchiveLimit, entry.Name)
		}
		expanded += int64(entry.UncompressedSize64)
		if expanded > limits.MaxExpandedBytes {
			return nil, Manifest{}, fmt.Errorf("%w: expanded size", ErrArchiveLimit)
		}
		if entry.Method != zip.Store && entry.CompressedSize64 > 0 &&
			entry.UncompressedSize64 > uint64(limits.MaxCompressionRatio)*entry.CompressedSize64 {
			return nil, Manifest{}, fmt.Errorf("%w: compression ratio %q", ErrArchiveLimit, entry.Name)
		}
		files = append(files, ArchiveFile{Path: clean, Size: int64(entry.UncompressedSize64), Mode: mode})
		if clean != "manifest.json" {
			continue
		}
		if entry.UncompressedSize64 > uint64(limits.MaxManifestBytes) {
			return nil, Manifest{}, fmt.Errorf("%w: manifest size", ErrArchiveLimit)
		}
		handle, err := entry.Open()
		if err != nil {
			return nil, Manifest{}, fmt.Errorf("%w: open manifest", ErrArchive)
		}
		content, readErr := io.ReadAll(io.LimitReader(handle, limits.MaxManifestBytes+1))
		closeErr := handle.Close()
		if readErr != nil || closeErr != nil || int64(len(content)) > limits.MaxManifestBytes ||
			uint64(len(content)) != entry.UncompressedSize64 {
			return nil, Manifest{}, fmt.Errorf("%w: read manifest", ErrArchive)
		}
		manifestBytes = content
	}
	if manifestBytes == nil {
		return nil, Manifest{}, fmt.Errorf("%w: manifest missing", ErrArchive)
	}
	available := make(map[string]struct{}, len(files))
	for _, file := range files {
		available[file.Path] = struct{}{}
	}
	manifest, err := ValidateManifest(manifestBytes, available)
	if err != nil {
		return nil, Manifest{}, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, manifest, nil
}

func validArchiveEntryPath(value string) bool {
	if value == "" || strings.HasPrefix(value, "/") || strings.Contains(value, "\\") ||
		strings.Contains(value, "\x00") || strings.Contains(value, ":") ||
		strings.Contains(value, "//") || strings.Contains(value, "..") ||
		strings.Contains(strings.ToLower(value), "%2f") || strings.Contains(strings.ToLower(value), "%5c") {
		return false
	}
	if path.IsAbs(value) || path.Clean(value) != value || value == "." {
		return false
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

// ManifestDigest binds the immutable feature build to its validated bytes.
func ManifestDigest(manifest []byte) string {
	sum := sha256Sum(manifest)
	return "sha256:" + hex.EncodeToString(sum[:])
}
