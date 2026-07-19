package execution

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// ErrCanonical is returned when a repo_location cannot be resolved to a safe
// canonical key: empty input, a path that does not exist, a resolution failure,
// or a path that escapes the configured allowlist root (spec §7.1). Admission
// treats this as a hard reject (the dispatch becomes `failed`).
var ErrCanonical = errors.New("repo location not canonicalisable")

// Canonicalize resolves localDir (repo_location.local_dir) to a single canonical
// absolute key used for the per-repo lock and the single-live rule (spec §7.1).
//
// It makes the path absolute (collapsing "." / ".." segments and trailing
// slashes) and then resolves every symlink component via filepath.EvalSymlinks,
// so a symlink alias and its real target always map to the SAME key — closing
// the "dispatch against the symlink to bypass single-live" hole. Because
// EvalSymlinks requires the path to exist, a non-existent directory is rejected.
//
// When allowedRoots is non-empty, the resolved path must lie within one of the
// (also symlink-resolved) roots; a path that escapes every root — whether via
// ".." or via a symlink pointing outside — is rejected. An empty allowedRoots
// imposes no containment restriction (M0 default when no allowlist is set).
func Canonicalize(localDir string, allowedRoots []string) (string, error) {
	if strings.TrimSpace(localDir) == "" {
		return "", fmt.Errorf("%w: empty local_dir", ErrCanonical)
	}

	// Abs collapses "." / ".." lexically and anchors relative paths to the cwd,
	// before we resolve symlinks (which requires the path to exist).
	abs, err := filepath.Abs(localDir)
	if err != nil {
		return "", fmt.Errorf("%w: abs %q: %v", ErrCanonical, localDir, err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		// Non-existent path or broken symlink — cannot establish a safe key.
		return "", fmt.Errorf("%w: resolve %q: %v", ErrCanonical, localDir, err)
	}
	resolved = filepath.Clean(resolved)

	if len(allowedRoots) > 0 && !withinAnyRoot(resolved, allowedRoots) {
		return "", fmt.Errorf("%w: %q escapes allowed roots", ErrCanonical, resolved)
	}
	return resolved, nil
}

// withinAnyRoot reports whether resolved lies within at least one allowlist
// root. Each root is itself symlink-resolved so the comparison is between two
// fully canonical paths; a root that cannot be resolved is skipped (it can never
// admit anything).
func withinAnyRoot(resolved string, roots []string) bool {
	for _, root := range roots {
		canonRoot, err := filepath.EvalSymlinks(root)
		if err != nil {
			continue
		}
		if withinRoot(resolved, filepath.Clean(canonRoot)) {
			return true
		}
	}
	return false
}

// withinRoot reports whether path is root or a descendant of root, using a
// path-segment boundary (so "/a/bc" is NOT inside "/a/b").
func withinRoot(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
