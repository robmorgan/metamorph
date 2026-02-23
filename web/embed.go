//go:build !noui

package web

import (
	"embed"
	"io/fs"
)

//go:embed all:out
var staticFiles embed.FS

// StaticFS is the embedded web UI filesystem.
var StaticFS fs.FS = staticFiles
