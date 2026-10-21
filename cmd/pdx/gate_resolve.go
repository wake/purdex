package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The shims of the retired host-load gate (spec 2026-10-15 §4) are gone, but a shell that started before `pdx setup` removed them still has
// their directory first on PATH, so the pass-through gate must keep telling a shim from the real tool behind it.

// shimTools are the names an old `pdx setup` wrote a shim for.
var shimTools = []string{"go", "pnpm", "npx", "vitest", "tsc", "composer", "xcodebuild", "swift", "swift-build", "swift-test"}

// shimDirIn is the shim directory under home.
func shimDirIn(home string) string { return filepath.Join(home, ".config", "pdx", "shims") }

// errNoRealBinary is resolveRealTool's answer when PATH has nothing but the shim directory to offer.
var errNoRealBinary = errors.New("no real binary on PATH besides the pdx shims")

// resolveRealTool is the real binary behind a shim: the first executable regular file named tool on pathEnv, skipping the shim
// directory and any entry that reaches it by a symlink (a candidate whose real path lies in the shim directory is refused, so
// no alias can send the gate back into its own shim). It is called on every run, from the PATH of that run, so an `nvm use`
// is followed. The returned path is the entry as PATH names it (not symlink-resolved: a program that finds its own files
// through the name it was started by keeps working).
func resolveRealTool(tool, pathEnv, shimDir string) (string, error) {
	realShim := realPath(shimDir)
	for _, d := range filepath.SplitList(pathEnv) {
		if d == "" {
			continue // an empty entry means the current directory: never a place a shim-bypassing binary may come from
		}
		if inside(realPath(d), realShim) {
			continue
		}
		cand := filepath.Join(d, tool)
		if !isExecutable(cand) {
			continue
		}
		if isShim(cand) {
			continue // told by its header, not by where it lies: $HOME may have been swapped (#3671)
		}
		if inside(realPath(cand), realShim) {
			continue
		}
		return cand, nil
	}
	return "", fmt.Errorf("%s: %w", tool, errNoRealBinary)
}

// shimMarker is the fixed start of the second line of every shim an old setup wrote.
const shimMarker = "# Written by `pdx setup` (pdx gate shim for"

// isShim reports whether the file at p (symlinks followed) is a pdx gate shim: its second line is the header an old setup wrote.
// Only that line counts, so a wrapper that merely mentions the marker in a comment further down is not taken for a shim.
func isShim(p string) bool {
	f, err := os.Open(p)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, 256)
	n, _ := f.Read(buf)
	lines := strings.SplitN(string(buf[:n]), "\n", 3)
	return len(lines) >= 2 && lines[0] == "#!/bin/sh" && strings.HasPrefix(lines[1], shimMarker)
}

// isShimOf is isShim, and more: the header names tool, and the script hands the call to `pdx gate --tool <tool>`. It is what lets setup delete a
// file: a wrapper of the user's that only copied the header is not removed.
func isShimOf(p, tool string) bool {
	if !isShim(p) {
		return false
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return false
	}
	lines := strings.SplitN(string(b), "\n", 3)
	return strings.HasPrefix(lines[1], shimMarker+" "+tool+")") && strings.Contains(string(b), " gate --auto --tool "+tool+` -- "$@"`)
}

func realPath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		p = r
	}
	if a, err := filepath.Abs(p); err == nil {
		p = a
	}
	return filepath.Clean(p)
}

// inside reports whether p is dir or lies under it.
func inside(p, dir string) bool {
	if p == dir {
		return true
	}
	return strings.HasPrefix(p, dir+string(filepath.Separator))
}

func isExecutable(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular() && fi.Mode().Perm()&0o111 != 0
}
