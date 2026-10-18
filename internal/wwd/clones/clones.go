// Package clones is the one judgement of "this host has a prepared clone of
// that repo": the labels (repo:<name>) and the worker's workspace
// preparation both ask it, so they cannot disagree.
//
// The clones are listed in the wwd config ([repos], name = path). A listed path
// counts only when it is a directory owned by the user running wwd that holds a
// .git (a directory, or the "gitdir: <dir>" file of a linked worktree whose
// target exists). Nothing forks; a path
// that fails the check is left out without an error.
package clones

import (
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
)

func ownedByMe(path string) bool {
	fi, err := os.Stat(path)
	if err != nil {
		return false
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == os.Getuid()
}

func isCheckout(path string) bool {
	if path == "" || !filepath.IsAbs(path) {
		return false
	}
	if fi, err := os.Stat(path); err != nil || !fi.IsDir() || !ownedByMe(path) {
		return false
	}
	g, err := os.Stat(filepath.Join(path, ".git"))
	if err != nil {
		return false
	}
	return g.IsDir() || (g.Mode().IsRegular() && gitdirPointerOK(path))
}

// gitdirPointerOK reads the .git file of a linked worktree: "gitdir: <dir>",
// the dir absolute or relative to the checkout, which must exist as a directory.
func gitdirPointerOK(path string) bool {
	f, err := os.Open(filepath.Join(path, ".git"))
	if err != nil {
		return false
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 4096))
	if err != nil {
		return false
	}
	line, _, _ := strings.Cut(string(b), "\n")
	dir, ok := strings.CutPrefix(strings.TrimSpace(line), "gitdir:")
	dir = strings.TrimSpace(dir)
	if !ok || dir == "" {
		return false
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(path, dir)
	}
	fi, err := os.Stat(dir)
	return err == nil && fi.IsDir()
}

// Verified is name → clone path for the entries of repos that are prepared
// clones right now.
func Verified(repos map[string]string) map[string]string {
	out := make(map[string]string, len(repos))
	for name, path := range repos {
		if isCheckout(path) {
			out[name] = path
		}
	}
	return out
}

// Source answers for one host's configured repos, checking again at every call
// (two stats per repo: cheap enough for each facts record).
type Source struct{ repos map[string]string }

func NewSource(repos map[string]string) *Source { return &Source{repos: repos} }

// Names are the repos with a prepared clone, sorted: the daemon's RepoSource.
func (s *Source) Names() []string {
	v := Verified(s.repos)
	out := make([]string, 0, len(v))
	for n := range v {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Path is the verified clone path of name.
func (s *Source) Path(name string) (string, bool) {
	p, ok := s.repos[name]
	if !ok || !isCheckout(p) {
		return "", false
	}
	return p, true
}
