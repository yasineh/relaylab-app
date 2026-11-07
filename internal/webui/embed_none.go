//go:build !embedweb

package webui

import "io/fs"

// FS reports that no web build is embedded (development builds).
func FS() (fs.FS, bool) { return nil, false }
