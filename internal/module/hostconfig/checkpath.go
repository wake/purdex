package hostconfig

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// PathCheck is the verdict for one project path on this host.
type PathCheck struct {
	Status   string `json:"status"` // dir | not_dir | missing | error
	Resolved string `json:"resolved"`
	Reason   string `json:"reason,omitempty"`
}

// checkPath expands a leading ~ with the daemon user's home (not a pane's
// $HOME) and classifies what is there. Input errors are returned as error.
func checkPath(path string, home func() (string, error)) (PathCheck, error) {
	p := strings.TrimSpace(path)
	if p == "" || strings.ContainsRune(p, 0) {
		return PathCheck{}, errors.New("path is required")
	}
	if p == "~" || strings.HasPrefix(p, "~/") {
		h, err := home()
		if err != nil || h == "" {
			return PathCheck{}, errors.New("cannot resolve home directory")
		}
		p = h + p[1:]
	}
	if !filepath.IsAbs(p) {
		return PathCheck{}, errors.New("path must be absolute or start with ~/")
	}
	resolved := filepath.Clean(p)
	info, err := os.Stat(resolved)
	switch {
	case err == nil && info.IsDir():
		return PathCheck{Status: "dir", Resolved: resolved}, nil
	case err == nil:
		return PathCheck{Status: "not_dir", Resolved: resolved}, nil
	case errors.Is(err, fs.ErrNotExist):
		return PathCheck{Status: "missing", Resolved: resolved}, nil
	default:
		return PathCheck{Status: "error", Resolved: resolved, Reason: fmt.Sprint(err)}, nil
	}
}
