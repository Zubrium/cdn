// Package static exposes the built Admin SPA assets embedded into the binary.
package static

import (
	"embed"
	"io/fs"
)

//go:embed dist
var dist embed.FS

// Assets returns the dist directory as a filesystem rooted at its contents.
func Assets() (fs.FS, error) {
	return fs.Sub(dist, "dist")
}
