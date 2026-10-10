package teammod

import "github.com/wake/purdex/internal/team"

// underGrant is the stub the red tests stand on.
func underGrant(dir string, g team.Grant) bool { return underRoots(dir, g.Roots) }

func canonicalRoots(roots []string) []string { return roots }
