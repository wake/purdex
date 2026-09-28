// Package fsutil holds small filesystem helpers shared by daemon modules.
package fsutil

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// CreateDedupFile atomically creates a file in dir using O_CREATE|O_EXCL to
// avoid TOCTOU races. If "photo.png" already exists it tries "photo-1.png",
// "photo-2.png", etc. Returns the open file and the chosen filename.
func CreateDedupFile(dir, name string) (*os.File, string, error) {
	path := filepath.Join(dir, name)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err == nil {
		return f, name, nil
	}
	if !os.IsExist(err) {
		return nil, "", err
	}
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	for i := 1; ; i++ {
		candidate := fmt.Sprintf("%s-%d%s", base, i, ext)
		path = filepath.Join(dir, candidate)
		f, err = os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if err == nil {
			return f, candidate, nil
		}
		if !os.IsExist(err) {
			return nil, "", err
		}
	}
}
