package render

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"unicode/utf8"
)

// ErrUnsafeHTML reports an HTML fragment that cannot be accepted as trusted
// feature or prerender output.
var ErrUnsafeHTML = errors.New("unsafe feature HTML")

// ReadyHTML is an opaque, validated HTML fragment. Its payload is private so
// callers cannot construct one by converting an arbitrary string or byte slice:
// NewReadyHTML is the only way to mark output as ready for document insertion.
type ReadyHTML struct {
	bytes []byte
}

var (
	// Structural tags would let a placement redefine the page shell, nest a
	// second main landmark, or load executable/inline code outside the
	// renderer-controlled bootstrap.
	forbiddenFragmentTags = regexp.MustCompile(`(?i)<\s*/?\s*(?:!doctype|base|body|head|html|link|main|meta|script|style|title|template|slot|iframe|object|embed|frame|frameset|noscript)\b`)

	// A feature must not create a placement root of its own: that would break
	// the one-root-per-placement and non-nesting invariants of the DOM contract.
	forbiddenFragmentRoots = regexp.MustCompile(`(?i)(?:data-placement-id|id\s*=\s*["']?placement-)`)
)

// NewReadyHTML validates and copies a trusted prerender/feature fragment.
// The copy prevents later mutation of the caller's buffer from changing an
// already accepted view.
func NewReadyHTML(data []byte) (ReadyHTML, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return ReadyHTML{}, fmt.Errorf("%w: empty fragment", ErrUnsafeHTML)
	}
	if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		return ReadyHTML{}, fmt.Errorf("%w: invalid encoding", ErrUnsafeHTML)
	}
	for _, r := range string(data) {
		if r < 0x20 && r != '\t' && r != '\n' && r != '\r' {
			return ReadyHTML{}, fmt.Errorf("%w: control character", ErrUnsafeHTML)
		}
	}
	if forbiddenFragmentTags.Match(data) {
		return ReadyHTML{}, fmt.Errorf("%w: forbidden structural tag", ErrUnsafeHTML)
	}
	if forbiddenFragmentRoots.Match(data) {
		return ReadyHTML{}, fmt.Errorf("%w: nested placement root", ErrUnsafeHTML)
	}
	return ReadyHTML{bytes: append([]byte(nil), data...)}, nil
}

func (h ReadyHTML) isEmpty() bool { return len(h.bytes) == 0 }
