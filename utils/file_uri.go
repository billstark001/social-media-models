package utils

import (
	"net/url"
	"path/filepath"
	"runtime"
)

// FileURI preserves Windows drive letters and escapes SQLite filename delimiters.
func FileURI(path string) url.URL {
	path = filepath.ToSlash(path)
	if runtime.GOOS == "windows" && filepath.VolumeName(path) != "" {
		path = "/" + path
	}
	return url.URL{Scheme: "file", Path: path}
}
