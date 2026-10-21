package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

// shimTools are the names an old `pdx setup` wrote a shim for.
var shimTools = []string{"go", "pnpm", "npx", "vitest", "tsc", "composer", "xcodebuild", "swift", "swift-build", "swift-test"}

// shimDirIn is the directory the shims of the old gate were written to, under home.
func shimDirIn(home string) string { return filepath.Join(home, ".config", "pdx", "shims") }

// thisPdx is the path of the running pdx, symlinks followed, as localSetup finds it.
func thisPdx() (string, error) {
	p, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("cannot find pdx binary: %w", err)
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		p = r
	}
	return p, nil
}

// removeGateShimsForUser is the gate half of `pdx setup` since WWD-9 9a-1: the gate is a pass-through and its shims are gone, so the ones an
// old setup left in ~/.config/pdx/shims are removed. It never fails the setup: the agent hooks are installed already, and what could not be
// done is a line on w.
func removeGateShimsForUser(w io.Writer) {
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(w, "setup: could not remove the pdx gate shims: %v\n", err)
		return
	}
	removeGateShims(home, w)
}

// removeGateShims removes, from the shim directory under home, the files an old setup wrote: a regular file named like one of shimTools that
// isShimOf that tool (the header, and the hand-over to `pdx gate` that the script is for). The directory goes when that leaves it empty; anything else in it is left, with a line saying so. A shim
// directory that is a symbolic link is not followed. Nothing to remove is not worth a word.
func removeGateShims(home string, w io.Writer) {
	dir := shimDirIn(home)
	if fi, err := os.Lstat(dir); err != nil || !fi.IsDir() {
		return // none, or a link or a file: not ours to touch
	}
	removed := 0
	for _, tool := range shimTools {
		p := filepath.Join(dir, tool)
		fi, err := os.Lstat(p)
		if err != nil || !fi.Mode().IsRegular() || !isShimOf(p, tool) {
			continue
		}
		if err := os.Remove(p); err != nil {
			fmt.Fprintf(w, "setup: could not remove the pdx gate shim %s: %v\n", p, err)
			continue
		}
		removed++
	}
	if removed > 0 {
		fmt.Fprintf(w, "pdx gate shims removed from %s (a shell started before may need `rehash`, or a new shell)\n", dir)
	}
	switch err := os.Remove(dir); {
	case err == nil:
	case errors.Is(err, syscall.ENOTEMPTY) || errors.Is(err, syscall.EEXIST):
		fmt.Fprintf(w, "setup: other files are left in %s; remove the directory by hand when you no longer need them\n", dir)
	default:
		fmt.Fprintf(w, "setup: could not remove %s: %v\n", dir, err)
	}
}
