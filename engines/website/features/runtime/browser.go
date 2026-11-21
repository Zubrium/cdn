package runtime

import (
	_ "embed"
	"errors"
	"strings"
)

//go:embed runtime.mjs
var embeddedRuntime string

// EmbeddedRuntimeSource returns the browser host source. A prerender or browser
// adapter loads this module verbatim; it is never generated from host strings.
func EmbeddedRuntimeSource() string {
	return embeddedRuntime
}

// ValidateEmbeddedRuntime is a fail-closed static check used by build/test code
// to detect accidental removal of a lifecycle guarantee from the host source.
func ValidateEmbeddedRuntime() error {
	for _, required := range []string{
		"createFeatureHost", "'render'", "'hydrate'", "'update'", "'dispose'",
		"AbortController", "assertExports", "serializableContext", "lifecycle timeout",
	} {
		if !strings.Contains(embeddedRuntime, required) {
			return errors.New("embedded static-lifecycle host is incomplete")
		}
	}
	return nil
}
