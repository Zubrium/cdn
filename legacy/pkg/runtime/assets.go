package runtime

import "embed"

//go:embed all:dist
var embedded embed.FS

//go:embed styles/*.css
var styles embed.FS
