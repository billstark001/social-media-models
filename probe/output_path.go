package probe

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// Resolve existing symlinks even when the final output directory is new.
func resolvedOutputPath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	current := absolute
	var tail []string
	for {
		resolved, e := filepath.EvalSymlinks(current)
		if e == nil {
			for i := len(tail) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, tail[i])
			}
			return resolved, nil
		}
		if !errors.Is(e, os.ErrNotExist) {
			return "", e
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", e
		}
		tail = append(tail, filepath.Base(current))
		current = parent
	}
}
func pathWithin(parent, child string) bool {
	relative, err := filepath.Rel(parent, child)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
