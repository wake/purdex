package teammod

import (
	"os"
	"path/filepath"

	"github.com/wake/purdex/internal/team"
)

// canonicalRoots resolves each root to its real path (symlinks evaluated, a directory), so what the approval card shows
// and what the grant stores is the directory that is meant (#2450), as for a forwarded grant (config.CanonicalTeamRoots).
// A root that cannot be resolved (missing, not a directory) is kept as given: it admits nothing at a spawn anyway, and
// refusing it would change the create wire. Duplicates are dropped, order kept.
func canonicalRoots(roots []string) []string {
	out := make([]string, 0, len(roots))
	seen := make(map[string]bool, len(roots))
	for _, r := range roots {
		if real, err := filepath.EvalSymlinks(r); err == nil {
			if st, err := os.Stat(real); err == nil && st.IsDir() {
				r = real
			}
		}
		if !seen[r] {
			seen[r] = true
			out = append(out, r)
		}
	}
	return out
}

// liveRoots are the roots that are still, now, the directory that was granted: a directory that resolves to itself. One
// replaced by a symlink (or sitting behind one) since it was granted is not that directory and is left out.
func liveRoots(roots []string) []string {
	var live []string
	for _, root := range roots {
		if r, err := filepath.EvalSymlinks(root); err != nil || r != filepath.Clean(root) {
			continue
		}
		if st, err := os.Stat(root); err == nil && st.IsDir() {
			live = append(live, root)
		}
	}
	return live
}

// underGrant reports whether dir (absolute, symlinks evaluated) is under one of a local team's granted roots. A grant
// with canonical roots (#2450) is judged like a forwarded one: only a root that still resolves to itself counts. A grant
// made before that keeps resolving each root at the spawn, as it always did, so no live grant loses a root on upgrade.
func underGrant(dir string, g team.Grant) bool {
	if !g.RootsCanonical {
		return underRoots(dir, g.Roots)
	}
	return underRoots(dir, liveRoots(g.Roots))
}
