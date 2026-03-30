package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// deduplicateFilename returns a filename that does not conflict with
// existing files in dir. If "photo.png" exists it tries "photo-1.png",
// "photo-2.png", etc.
func deduplicateFilename(dir, name string) string {
	if _, err := os.Stat(filepath.Join(dir, name)); os.IsNotExist(err) {
		return name
	}
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	for i := 1; ; i++ {
		candidate := fmt.Sprintf("%s-%d%s", base, i, ext)
		if _, err := os.Stat(filepath.Join(dir, candidate)); os.IsNotExist(err) {
			return candidate
		}
	}
}
