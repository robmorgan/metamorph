//go:build noui

package web

import "io/fs"

// StaticFS is nil when built with -tags noui.
var StaticFS fs.FS
