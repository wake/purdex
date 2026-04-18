package dev

import "fmt"

// rebuildTrackedPaths lists repo-root-relative paths whose changes hint that
// a full electron-builder rebuild (not just electron-vite) may be needed.
// Kept narrow on purpose — over-flagging is worse than a false negative.
var rebuildTrackedPaths = []string{
	"package.json",
	"pnpm-lock.yaml",
	"electron-builder.yml",
	"build/",
}

// detectRequiresFullRebuild compares the rebuild-hash of the current source
// tree against the one recorded in the last build. Empty buildHash (old
// build or no build) returns (false, "") — we only flag when we have
// enough info to trust.
func (m *DevModule) detectRequiresFullRebuild(buildHash string) (bool, string) {
	if buildHash == "" || buildHash == "unknown" {
		return false, ""
	}
	currentHash := m.hashFn(rebuildTrackedPaths...)
	if currentHash == "unknown" || currentHash == buildHash {
		return false, ""
	}
	return true, fmt.Sprintf("rebuild-tracked paths changed (%s → %s)", buildHash, currentHash)
}
