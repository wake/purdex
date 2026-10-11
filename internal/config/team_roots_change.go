// internal/config/team_roots_change.go
package config

import (
	"os"
	"path/filepath"
)

// The one implementation of a change to a peer entry's TeamRoots, shared by PUT /api/peers/hosts/{alias} (the admin's) and
// POST /api/peers/team/roots-change (a lead host's, cross-host roots spec 2026-10-12 §3.1): neither copies it.

// RemovalCandidates are the spellings of a root to revoke: the cleaned path and, when the directory still exists, the real
// path the grant was stored as. A directory that is gone can still be revoked by the spelling it was granted under.
func RemovalCandidates(in []string) ([]string, error) {
	out := make([]string, 0, 2*len(in))
	for _, p := range in {
		if !filepath.IsAbs(p) {
			return nil, &RootError{Root: p, Reason: "not_absolute"}
		}
		c := filepath.Clean(p)
		out = append(out, c)
		if real, err := filepath.EvalSymlinks(c); err == nil {
			out = append(out, real)
		} else if lex, ok := followLinkLexically(c); ok {
			out = append(out, lex) // a dangling link: the real directory it pointed at when it was granted
		}
	}
	return out, nil
}

// followLinkLexically follows p's last component through symlinks without needing the final target to exist (EvalSymlinks
// fails on a dangling link), after resolving the parent directory the normal way. ok is false when the parent cannot be
// resolved or the chain is too long.
func followLinkLexically(p string) (string, bool) {
	for range 40 {
		parent, err := filepath.EvalSymlinks(filepath.Dir(p))
		if err != nil {
			return "", false
		}
		p = filepath.Join(parent, filepath.Base(p))
		fi, err := os.Lstat(p)
		if err != nil || fi.Mode()&os.ModeSymlink == 0 {
			return p, true // gone, or a plain entry: the last spelling
		}
		target, err := os.Readlink(p)
		if err != nil {
			return p, true
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(p), target)
		}
		p = filepath.Clean(target)
	}
	return "", false
}

// ApplyRootsChange computes the new set from the current one: the whole set, or the removals then the additions.
func ApplyRootsChange(cur []string, whole *[]string, wholeRoots, remove, add []string) ([]string, error) {
	if whole != nil {
		return wholeRoots, nil
	}
	drop := make(map[string]bool, len(remove))
	for _, r := range remove {
		drop[r] = true
	}
	next := make([]string, 0, len(cur)+len(add))
	have := make(map[string]bool, len(cur)+len(add))
	for _, r := range cur {
		if !drop[r] {
			next = append(next, r)
			have[r] = true
		}
	}
	for _, r := range add {
		if !have[r] {
			next = append(next, r)
			have[r] = true
		}
	}
	if len(next) > MaxTeamRoots {
		return nil, &RootError{Reason: "too_many"}
	}
	return next, nil
}
