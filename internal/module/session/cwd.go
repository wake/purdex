package session

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// resolveCwd turns a requested working directory into an absolute, existing
// directory before it is handed to `tmux new-session -c`.
//
// tmux does neither of those things itself: it does not expand ~, and it does
// not fail on an unusable -c — it silently starts the session in $HOME. A
// session rooted in the wrong directory is far worse than a 400, so anything
// that would not land where the caller asked is rejected here.
//
// The rules mirror hostconfig.checkPath, the one other place in the daemon
// that already expands ~ with the daemon user's home (not a pane's $HOME):
// only "~" and "~/…" are supported — "~user" and $VAR are not.
//
// An empty (or all-whitespace) request keeps the historical "/" default, which
// is then stat'd like any other path; it exists, so it passes.
func resolveCwd(raw string, home func() (string, error)) (string, error) {
	p := strings.TrimSpace(raw)
	if p == "" {
		p = "/"
	}
	if strings.ContainsRune(p, 0) {
		return "", errors.New("cwd must not contain NUL")
	}
	if p == "~" || strings.HasPrefix(p, "~/") {
		h, err := home()
		if err != nil || h == "" {
			return "", errors.New("cannot resolve home directory")
		}
		p = h + p[1:]
	}
	if !filepath.IsAbs(p) {
		return "", errors.New("cwd must be absolute or start with ~/")
	}
	resolved := filepath.Clean(p)
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("cwd is not usable: %w", err)
	}
	if !info.IsDir() {
		return "", errors.New("cwd is not a directory: " + resolved)
	}
	return resolved, nil
}
