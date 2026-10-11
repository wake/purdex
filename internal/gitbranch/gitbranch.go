// Package gitbranch reads the current git branch of a directory without
// spawning a git process: it walks up to the nearest .git, follows a
// worktree/submodule "gitdir:" pointer, and parses HEAD.
package gitbranch

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const headPrefix = "ref: refs/heads/"

// FindHead locates the HEAD file for the repository containing dir, walking
// up until stopAt (inclusive) or the filesystem root. "" when none.
func FindHead(dir, stopAt string) string {
	if dir == "" || !filepath.IsAbs(dir) {
		return ""
	}
	dir = filepath.Clean(dir)
	for {
		if head := headIn(dir); head != "" {
			return head
		}
		if dir == stopAt {
			return ""
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func headIn(dir string) string {
	dot := filepath.Join(dir, ".git")
	fi, err := os.Stat(dot)
	if err != nil {
		return ""
	}
	if fi.IsDir() {
		return filepath.Join(dot, "HEAD")
	}
	b, err := os.ReadFile(dot)
	if err != nil {
		return ""
	}
	rest, ok := strings.CutPrefix(strings.TrimSpace(string(b)), "gitdir:")
	if !ok {
		return ""
	}
	gd := strings.TrimSpace(rest)
	if gd == "" {
		return ""
	}
	if !filepath.IsAbs(gd) {
		gd = filepath.Join(dir, gd)
	}
	return filepath.Join(gd, "HEAD")
}

// ReadHead parses a HEAD file: branch name, "@"+7 sha chars when detached,
// "" when unreadable or unrecognised.
func ReadHead(headPath string) string {
	b, err := os.ReadFile(headPath)
	if err != nil {
		return ""
	}
	s := strings.TrimSpace(string(b))
	if name, ok := strings.CutPrefix(s, headPrefix); ok {
		return name
	}
	if len(s) >= 7 && isHex(s) {
		return "@" + s[:7]
	}
	return ""
}

func isHex(s string) bool {
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}

// Branch is the uncached one-shot lookup.
func Branch(cwd, stopAt string) string {
	head := FindHead(cwd, stopAt)
	if head == "" {
		return ""
	}
	return ReadHead(head)
}

// Resolver caches cwd -> HEAD path (where .git lives); HEAD content is
// re-read on every call. A cached location that no longer exists is
// rediscovered. Non-repo cwds are not cached (a repo may appear later).
type Resolver struct {
	stopAt string
	mu     sync.Mutex
	heads  map[string]string
}

// NewResolver walks no higher than stopAt (typically $HOME).
func NewResolver(stopAt string) *Resolver {
	return &Resolver{stopAt: stopAt, heads: map[string]string{}}
}

// Branch returns the branch for cwd, "" when not in a repo.
func (r *Resolver) Branch(cwd string) string {
	if cwd == "" {
		return ""
	}
	r.mu.Lock()
	head, ok := r.heads[cwd]
	r.mu.Unlock()
	if ok {
		if _, err := os.Stat(head); err == nil {
			return ReadHead(head)
		}
	}
	head = FindHead(cwd, r.stopAt)
	r.mu.Lock()
	if head == "" {
		delete(r.heads, cwd)
	} else {
		if len(r.heads) > 512 {
			r.heads = map[string]string{}
		}
		r.heads[cwd] = head
	}
	r.mu.Unlock()
	if head == "" {
		return ""
	}
	return ReadHead(head)
}
