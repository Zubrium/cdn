package files

import (
	"io"
	"net/http"
	"path/filepath"
	"strings"

	"websiteengine/features/media"
)

// ClassifyContentType derives the type from bytes, not from a request header.
// A supplied SVG reader is parsed to EOF using the presentation-only SVG
// allowlist. Unsafe or malformed SVG remains a generic attachment. Callers
// should supply svgReader only for files within their configured bounded SVG
// inspection limit; parsing a huge XML attribute must not exhaust memory.
// No bytes are rewritten, so the content digest still describes the original.
func ClassifyContentType(name string, head []byte, svgReader io.Reader) string {
	typeName := http.DetectContentType(head)
	if strings.EqualFold(filepath.Ext(name), ".woff2") && len(head) >= 4 && string(head[:4]) == "wOF2" {
		return "font/woff2"
	}
	if strings.EqualFold(filepath.Ext(name), ".svg") && svgReader != nil && media.ValidateSVGReader(svgReader) == nil {
		return "image/svg+xml"
	}
	return typeName
}
