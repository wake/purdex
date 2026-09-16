// cmd/pdx/path.go — `pdx path`, the offline repair command that reports and
// fixes whether the `pdx` command is reachable at all (spec
// docs/specs/2026-09-16-pdx-path-gate-spec.md §3).
//
// This file must never load the config, open the store, or make a network
// request — not even to ask whether the daemon is alive. It is the command a
// user reaches for precisely when the daemon will not start, so anything it
// depends on is another thing that can stop it from working. Its only inputs
// are os.Executable(), $HOME, $PATH, $SHELL and the filesystem, and they all
// arrive through pathEnv so tests never touch the real ones.
package main

import (
	"path/filepath"
)

// evalOrKeep resolves symlinks, keeping the input when that is not possible.
// Comparisons in this file are between *resolved* paths, and both sides need
// the same treatment: on macOS a temp dir under /var is really /private/var,
// so comparing a resolved path against an unresolved one never matches.
func evalOrKeep(p string) string {
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved
	}
	return p
}
