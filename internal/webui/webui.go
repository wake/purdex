// internal/webui/webui.go
package webui

import (
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path"
	"strings"
)

// Handler serves the SPA. When spaDir is non-empty it serves that directory
// from disk via os.OpenRoot (symlink-contained; cannot escape spaDir) for dev
// iteration through PDX_SPA_DIR; otherwise it serves the embedded production
// build. Unknown paths fall back to index.html (SPA history routing). Only
// GET/HEAD are accepted — callers route /api/* and /ws/* to the protected mux
// before reaching here, and CORS handles OPTIONS preflight upstream.
func Handler(spaDir string) (http.Handler, error) {
	var fsys fs.FS
	if spaDir != "" {
		root, err := os.OpenRoot(spaDir)
		if err != nil {
			return nil, fmt.Errorf("webui: open spa dir %q: %w", spaDir, err)
		}
		fsys = root.FS()
	} else {
		sub, err := fs.Sub(embedded, "dist")
		if err != nil {
			return nil, err
		}
		fsys = sub
	}

	// Fail-fast: the SPA is unusable without index.html.
	if _, err := fs.Stat(fsys, "index.html"); err != nil {
		return nil, fmt.Errorf("webui: index.html missing under spa root: %w", err)
	}

	fileServer := http.FileServerFS(fsys)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		p := path.Clean(strings.TrimPrefix(r.URL.Path, "/"))
		if p == "" || p == "." {
			p = "index.html"
		}

		// SPA history fallback: a path that doesn't stat to an existing entry
		// (unknown client route, or an invalid/traversal path) serves
		// index.html. Existing files AND directories fall through to
		// FileServerFS, preserving standard static/dir semantics.
		if _, err := fs.Stat(fsys, p); err != nil {
			r2 := r.Clone(r.Context())
			r2.URL.Path = "/"
			fileServer.ServeHTTP(w, r2)
			return
		}
		fileServer.ServeHTTP(w, r)
	}), nil
}
