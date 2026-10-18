package entry

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/wake/purdex/internal/wwd/manifest"
)

var errNoManifest = errors.New("no manifest")

func isNoManifest(err error) bool { return errors.Is(err, errNoManifest) }

// findManifest looks for the repository's manifest from cwd upward. The first directory that has the manifest is the repo's root;
// a directory that is a repository root (it has a .git, a directory or a linked worktree's file) and has none ends the search, so
// a project inside another repository does not borrow that repository's manifest. An error that is not errNoManifest means a
// manifest was found and does not read.
func findManifest(cwd string) (root string, m *manifest.Manifest, err error) {
	dir := filepath.Clean(cwd)
	for {
		file := filepath.Join(dir, filepath.FromSlash(ManifestPath))
		if st, serr := os.Stat(file); serr == nil && st.Mode().IsRegular() {
			m, err = manifest.Load(file)
			if err != nil {
				return "", nil, fmt.Errorf("manifest %s: %v", file, err)
			}
			return dir, m, nil
		}
		if _, gerr := os.Lstat(filepath.Join(dir, ".git")); gerr == nil {
			return "", nil, fmt.Errorf("%w (%s) in the repository at %s", errNoManifest, ManifestPath, dir)
		}
		up := filepath.Dir(dir)
		if up == dir {
			return "", nil, fmt.Errorf("%w (%s) above %s", errNoManifest, ManifestPath, cwd)
		}
		dir = up
	}
}
