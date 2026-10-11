// Package gitbranch reads the current git branch of a directory without
// spawning a git process: it walks up to the nearest .git, follows a
// worktree/submodule "gitdir:" pointer, and parses HEAD.
//
// Everything it reads is attacker-influenced (a repo the user cd'd into), so
// reads are bounded, only regular files are opened (a FIFO or device would
// block forever) and the result is validated before it is returned. There is
// deliberately no location cache: every call walks up from the cwd again, so
// a nearer repo or a redirected gitdir is noticed at once.
package gitbranch

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	headPrefix   = "ref: refs/heads/"
	maxFileBytes = 1024
	maxNameBytes = 200
)

// readSmall reads a regular file of at most maxFileBytes. Anything else
// (missing, non-regular, oversized, multi-line) yields ok=false. The returned
// text has at most one trailing "\n" removed and contains no other newline.
func readSmall(path string) (string, bool) {
	fi, err := os.Stat(path)
	if err != nil || !fi.Mode().IsRegular() {
		return "", false
	}
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxFileBytes+1))
	if err != nil || len(b) > maxFileBytes || !utf8.Valid(b) {
		return "", false
	}
	s := strings.TrimSuffix(string(b), "\n")
	if strings.ContainsRune(s, '\n') {
		return "", false
	}
	return s, true
}

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
	s, ok := readSmall(dot)
	if !ok {
		return ""
	}
	rest, ok := strings.CutPrefix(s, "gitdir:")
	if !ok {
		return ""
	}
	gd := strings.TrimSpace(rest)
	if gd == "" || strings.ContainsFunc(gd, unicode.IsControl) {
		return ""
	}
	if !filepath.IsAbs(gd) {
		gd = filepath.Join(dir, gd)
	}
	return filepath.Join(gd, "HEAD")
}

// ReadHead parses a HEAD file: branch name, "@"+7 sha chars when detached,
// "" when unreadable, not a regular file, oversized or unrecognised.
func ReadHead(headPath string) string {
	s, ok := readSmall(headPath)
	if !ok {
		return ""
	}
	if name, ok := strings.CutPrefix(s, headPrefix); ok {
		if validBranchName(name) {
			return name
		}
		return ""
	}
	if (len(s) == 40 || len(s) == 64) && isHex(s) {
		return "@" + s[:7]
	}
	return ""
}

// validBranchName is a simplified git check-ref-format plus a refusal of
// anything that could spoof the UI (bidi/format characters).
func validBranchName(n string) bool {
	if n == "" || len(n) > maxNameBytes || !utf8.ValidString(n) {
		return false
	}
	if strings.Contains(n, "..") || strings.HasPrefix(n, "/") || strings.HasSuffix(n, "/") ||
		strings.HasPrefix(n, ".") || strings.HasSuffix(n, ".") || strings.HasSuffix(n, ".lock") {
		return false
	}
	for _, r := range n {
		switch {
		case r < 0x20 || r == 0x7f, unicode.IsControl(r), unicode.IsSpace(r):
			return false
		case strings.ContainsRune("~^:?*[\\", r):
			return false
		case r >= 0x202a && r <= 0x202e, r >= 0x2066 && r <= 0x2069, r == 0x200e, r == 0x200f:
			return false
		}
	}
	return true
}

func isHex(s string) bool {
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}

// Branch looks the branch up from scratch (no caching). Callers on a hot path
// must still run it off the request path: a hung filesystem can stall it.
func Branch(cwd, stopAt string) string {
	head := FindHead(cwd, stopAt)
	if head == "" {
		return ""
	}
	return ReadHead(head)
}
