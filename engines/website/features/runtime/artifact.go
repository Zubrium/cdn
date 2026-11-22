package runtime

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strings"
	"unicode/utf8"
)

var (
	ErrArtifact = errors.New("invalid static-page-artifact/v1 manifest")
	ErrFileMeta = errors.New("artifact file metadata mismatch")
)

const (
	artifactFormat      = "static-page-artifact/v1"
	canonicalMaxNesting = 64
)

var (
	sha256Pattern       = regexp.MustCompile(`^[0-9a-f]{64}$`)
	artifactIDPattern   = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-8][0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$`)
	percentPattern      = regexp.MustCompile(`%[0-9A-Fa-f]{2}`)
	allowedContentTypes = map[string]bool{
		"text/html": true, "text/css": true, "text/javascript": true,
		"application/javascript": true, "application/json": true,
		"image/jpeg": true, "image/png": true, "image/webp": true,
		"image/avif": true, "image/gif": true, "font/woff": true,
		"font/woff2": true, "application/font-woff": true,
	}
	allowedCacheClasses = map[string]bool{"immutable": true, "short": true, "none": true}
)

// ArtifactFile is the trusted Core metadata for one immutable artifact file.
type ArtifactFile struct {
	FileID      string `json:"file_id"`
	ContentType string `json:"content_type"`
	SHA256      string `json:"sha256"`
	Size        int64  `json:"size"`
	CacheClass  string `json:"cache_class,omitempty"`
}

// ArtifactManifest describes exactly one HTML document and its scoped assets.
type ArtifactManifest struct {
	Format   string                  `json:"format"`
	Digest   string                  `json:"digest"`
	Document ArtifactFile            `json:"document"`
	Assets   map[string]ArtifactFile `json:"assets"`
}

// CoreFileMetadata is the observed metadata of a Core file, supplied by a
// trusted capability. It is never derived from the artifact manifest itself.
type CoreFileMetadata struct {
	ContentType string
	SHA256      string
	Size        int64
}

// ValidateArtifact checks the complete versioned manifest before a Router
// target may reference it. It rejects visitor-controlled identifiers, illegal
// paths, unknown content types and a digest that does not match the payload.
func ValidateArtifact(data []byte, maxFiles int, maxBytes int64) (ArtifactManifest, error) {
	if maxFiles <= 0 || maxBytes <= 0 || len(data) == 0 || len(data) > int(maxBytes) {
		return ArtifactManifest{}, fmt.Errorf("%w: size limits", ErrArtifact)
	}
	var manifest ArtifactManifest
	decoder := strictDecoder(data)
	if decoder.Decode(&manifest) != nil {
		return ArtifactManifest{}, fmt.Errorf("%w: JSON", ErrArtifact)
	}
	if err := ensureNoTrailingJSON(decoder); err != nil {
		return ArtifactManifest{}, fmt.Errorf("%w: trailing JSON", ErrArtifact)
	}
	if manifest.Format != artifactFormat || manifest.Assets == nil || len(manifest.Assets) >= maxFiles {
		return ArtifactManifest{}, fmt.Errorf("%w: format or file count", ErrArtifact)
	}
	expectedDigest, err := ArtifactDigest(data)
	if err != nil {
		return ArtifactManifest{}, err
	}
	if manifest.Digest != expectedDigest {
		return ArtifactManifest{}, fmt.Errorf("%w: digest mismatch", ErrArtifact)
	}
	if documentDeclaresCacheClass(data) {
		return ArtifactManifest{}, fmt.Errorf("%w: document cache class", ErrArtifact)
	}
	if err := validateArtifactFile(manifest.Document, true, maxBytes); err != nil {
		return ArtifactManifest{}, err
	}
	seenPaths := make(map[string]struct{}, len(manifest.Assets))
	seenFileIDs := map[string]struct{}{manifest.Document.FileID: {}}
	var total int64 = manifest.Document.Size
	for assetPath, file := range manifest.Assets {
		if !validArtifactPath(assetPath) {
			return ArtifactManifest{}, fmt.Errorf("%w: asset path %q", ErrArtifact, assetPath)
		}
		normalized := path.Clean(assetPath)
		if normalized != assetPath {
			return ArtifactManifest{}, fmt.Errorf("%w: non-canonical asset path %q", ErrArtifact, assetPath)
		}
		if _, duplicate := seenPaths[normalized]; duplicate {
			return ArtifactManifest{}, fmt.Errorf("%w: duplicate asset path %q", ErrArtifact, assetPath)
		}
		seenPaths[normalized] = struct{}{}
		if _, duplicate := seenFileIDs[file.FileID]; duplicate {
			return ArtifactManifest{}, fmt.Errorf("%w: duplicate file %q", ErrArtifact, file.FileID)
		}
		seenFileIDs[file.FileID] = struct{}{}
		if err := validateArtifactFile(file, false, maxBytes); err != nil {
			return ArtifactManifest{}, fmt.Errorf("%w: asset %q: %v", ErrArtifact, assetPath, err)
		}
		total += file.Size
		if total > maxBytes {
			return ArtifactManifest{}, fmt.Errorf("%w: total size", ErrArtifact)
		}
	}
	return manifest, nil
}

// documentDeclaresCacheClass rejects even an explicitly empty cache_class on
// the document. The document always uses the no-store policy and Router's
// strict schema does not admit the field there.
func documentDeclaresCacheClass(data []byte) bool {
	var root map[string]json.RawMessage
	if json.Unmarshal(data, &root) != nil {
		return true
	}
	var document map[string]json.RawMessage
	if json.Unmarshal(root["document"], &document) != nil {
		return true
	}
	_, present := document["cache_class"]
	return present
}

func validateArtifactFile(file ArtifactFile, document bool, maxBytes int64) error {
	if !artifactIDPattern.MatchString(file.FileID) || !sha256Pattern.MatchString(file.SHA256) ||
		file.Size <= 0 || file.Size > maxBytes {
		return fmt.Errorf("%w: file metadata", ErrArtifact)
	}
	mediaType := strings.ToLower(strings.TrimSpace(file.ContentType))
	baseType, charset, hasCharset := strings.Cut(mediaType, ";")
	baseType = strings.TrimSpace(baseType)
	if !allowedContentTypes[baseType] {
		return fmt.Errorf("%w: content type %q", ErrArtifact, baseType)
	}
	if hasCharset {
		if strings.TrimSpace(charset) != "charset=utf-8" ||
			(baseType != "text/html" && baseType != "text/css" && baseType != "text/javascript" && baseType != "application/javascript") {
			return fmt.Errorf("%w: charset for %q", ErrArtifact, baseType)
		}
	}
	if document && baseType != "text/html" {
		return fmt.Errorf("%w: document content type", ErrArtifact)
	}
	if !document && baseType == "text/html" {
		return fmt.Errorf("%w: nested HTML document", ErrArtifact)
	}
	if document && file.CacheClass != "" {
		return fmt.Errorf("%w: document cache class", ErrArtifact)
	}
	if file.CacheClass != "" && !allowedCacheClasses[file.CacheClass] {
		return fmt.Errorf("%w: cache class", ErrArtifact)
	}
	return nil
}

func validArtifactPath(value string) bool {
	if value == "" || strings.TrimSpace(value) != value || strings.HasPrefix(value, "/") ||
		strings.Contains(value, "\\") || strings.Contains(value, "\x00") ||
		strings.Contains(value, "..") || percentPattern.MatchString(value) ||
		strings.Contains(value, "#") || strings.Contains(value, "?") {
		return false
	}
	if parsed, err := url.Parse(value); err != nil || parsed.Scheme != "" || parsed.Host != "" {
		return false
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

// CanonicalArtifactPayload returns the single canonical JSON payload of a
// static-page-artifact/v1 manifest: the manifest object without its "digest"
// field, with object keys sorted at every level and no insignificant
// whitespace. The result is identical for any valid encoding of the same
// payload, independent of Go struct field order or JSON key order.
func CanonicalArtifactPayload(data []byte) ([]byte, error) {
	if len(data) == 0 || !utf8.Valid(data) {
		return nil, fmt.Errorf("%w: canonical payload", ErrArtifact)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	value, err := decodeUniqueJSONValue(decoder, 1)
	if err != nil || ensureNoTrailingJSON(decoder) != nil {
		return nil, fmt.Errorf("%w: canonical payload JSON", ErrArtifact)
	}
	payload, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%w: canonical payload object", ErrArtifact)
	}
	delete(payload, "digest")
	canonical, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("%w: canonical payload", ErrArtifact)
	}
	return canonical, nil
}

// ArtifactDigest returns the digest of CanonicalArtifactPayload(data) using the
// shared static-page-artifact/v1 "sha256:" prefix.
func ArtifactDigest(data []byte) (string, error) {
	canonical, err := CanonicalArtifactPayload(data)
	if err != nil {
		return "", err
	}
	sum := sha256Sum(canonical)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func decodeUniqueJSONValue(decoder *json.Decoder, depth int) (any, error) {
	if depth > canonicalMaxNesting {
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
			value, err := decodeUniqueJSONValue(decoder, depth+1)
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
			value, err := decodeUniqueJSONValue(decoder, depth+1)
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

// VerifyArtifactFiles compares trusted Core metadata with the manifest. A
// mismatch never activates the target and never changes the previous artifact.
func VerifyArtifactFiles(manifest ArtifactManifest, observed map[string]CoreFileMetadata) error {
	document, exists := observed[manifest.Document.FileID]
	if !exists {
		return fmt.Errorf("%w: document file missing", ErrFileMeta)
	}
	if err := compareFileMetadata(manifest.Document, document); err != nil {
		return fmt.Errorf("%w: document", ErrFileMeta)
	}
	for assetPath, declared := range manifest.Assets {
		metadata, exists := observed[declared.FileID]
		if !exists {
			return fmt.Errorf("%w: asset %q missing", ErrFileMeta, assetPath)
		}
		if err := compareFileMetadata(declared, metadata); err != nil {
			return fmt.Errorf("%w: asset %q: %v", ErrFileMeta, assetPath, err)
		}
	}
	return nil
}

func compareFileMetadata(declared ArtifactFile, observed CoreFileMetadata) error {
	if declared.Size != observed.Size || !strings.EqualFold(declared.SHA256, observed.SHA256) {
		return errors.New("checksum or size")
	}
	declaredType, _, _ := strings.Cut(strings.ToLower(declared.ContentType), ";")
	observedType, _, _ := strings.Cut(strings.ToLower(observed.ContentType), ";")
	if strings.TrimSpace(declaredType) != strings.TrimSpace(observedType) {
		return errors.New("content type")
	}
	return nil
}

func sha256Sum(data []byte) [32]byte {
	return sha256.Sum256(data)
}
