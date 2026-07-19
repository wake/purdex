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

// ErrNoAllowedRoots is the fail-closed rejection used when no allowed repo root
// is configured. It wraps ErrCanonical so callers that already map ErrCanonical
// onto the `invalid_repo_location` report code keep working.
//
// Repo paths arrive from Ploom (repo_location.local_dir) and are only as
// trustworthy as that input: an unrestricted daemon would happily start an agent
// in ANY git checkout on the host (and, under a wide host sandbox policy, write
// to it). The sandbox clamp limits what an execution may do; it does not
// establish which repos may be touched at all. So "no allowlist" means "nothing
// is admissible", never "everything is".
var ErrNoAllowedRoots = fmt.Errorf("%w: no allowed repo roots configured", ErrCanonical)

// Canonicalize resolves localDir (repo_location.local_dir) to a single canonical
// absolute key used for the per-repo lock and the single-live rule (spec §7.1).
//
// It makes the path absolute (collapsing "." / ".." segments and trailing
// slashes) and then resolves every symlink component via filepath.EvalSymlinks,
// so a symlink alias and its real target always map to the SAME key — closing
// the "dispatch against the symlink to bypass single-live" hole. Because
// EvalSymlinks requires the path to exist, a non-existent directory is rejected.
//
// The resolved path must lie within one of the (also symlink-resolved)
// allowedRoots; a path that escapes every root — whether via ".." or via a
// symlink pointing outside — is rejected, and so is EVERY path when no usable
// root is configured (ErrNoAllowedRoots — fail closed, see above).
func Canonicalize(localDir string, allowedRoots []string) (string, error) {
	roots := usableRoots(allowedRoots)
	if len(roots) == 0 {
		return "", ErrNoAllowedRoots
	}
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

	if !withinAnyRoot(resolved, roots) {
		return "", fmt.Errorf("%w: %q escapes allowed roots", ErrCanonical, resolved)
	}
	return resolved, nil
}

// usableRoots drops blank entries so a config like `allowed_repo_roots = [""]`
// cannot pass the fail-closed gate with a root that admits nothing meaningful.
func usableRoots(roots []string) []string {
	out := make([]string, 0, len(roots))
	for _, r := range roots {
		if strings.TrimSpace(r) != "" {
			out = append(out, r)
		}
	}
	return out
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
