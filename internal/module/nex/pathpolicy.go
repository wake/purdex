package nex

import (
	"os"
	"strings"
)

// composePath computes the PATH the Nexen engine's `claude -p` children
// should see (spec §4.6).
//
// entries are deduplicated (keeping first-occurrence order) and filtered
// through isDir, producing a prefix. The result is that prefix followed by
// the elements of current (split on os.PathListSeparator) with any element
// already present in the prefix removed, joined with
// os.PathListSeparator.
//
// composePath is pure: it never inspects or mutates process state (that is
// applyPathPolicy's job), and it never mutates entries. Callers are
// responsible for `~`-expanding and absolutizing entries beforehand.
//
// composePath is idempotent: composePath(composePath(c, e, f), e, f) ==
// composePath(c, e, f), because every entry that survives isDir already
// sits at the front of the result and is deduplicated.
func composePath(current string, entries []string, isDir func(string) bool) string {
	sep := string(os.PathListSeparator)

	prefix := make([]string, 0, len(entries))
	inPrefix := make(map[string]bool, len(entries))
	for _, e := range entries {
		if inPrefix[e] {
			continue
		}
		if !isDir(e) {
			continue
		}
		inPrefix[e] = true
		prefix = append(prefix, e)
	}

	var rest []string
	if current != "" {
		for _, p := range strings.Split(current, sep) {
			if inPrefix[p] {
				continue
			}
			rest = append(rest, p)
		}
	}

	return strings.Join(append(prefix, rest...), sep)
}

// applyPathPolicy reads the process PATH, applies composePath, and writes
// it back via os.Setenv only if it actually changed. It returns the final
// PATH value and whether the environment was modified.
func applyPathPolicy(entries []string, isDir func(string) bool) (final string, changed bool) {
	current := os.Getenv("PATH")
	final = composePath(current, entries, isDir)
	if final == current {
		return final, false
	}
	os.Setenv("PATH", final)
	return final, true
}
