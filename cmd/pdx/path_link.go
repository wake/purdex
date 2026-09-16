package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// linkKind is what is actually at ~/.local/bin/pdx. The report collapses
// these into the four states above; `link` needs the full distinction,
// because what it may do differs per kind.
type linkKind int

const (
	linkKindNothing linkKind = iota
	linkKindCorrect
	linkKindOtherTarget
	linkKindDangling
	linkKindRegularFile
	linkKindDirectory
	linkKindOther // socket, fifo, device…
	linkKindUninspectable
)

// classifyLinkPath is the one place that decides what is at linkPath.
//
// os.Lstat, not os.Stat: os.Stat follows the link and reports a *dangling*
// symlink as "not there", which looks identical to an empty path and leads to
// a create that then fails with EEXIST and no explanation (spec §3.2).
//
// target is the raw symlink target for the symlink kinds, and a human phrase
// ("a regular file", "a directory") for the rest, so callers can print it
// either way.
func classifyLinkPath(linkPath, self string) (kind linkKind, target string, err error) {
	fi, err := os.Lstat(linkPath)
	if err != nil {
		if os.IsNotExist(err) {
			return linkKindNothing, "", nil
		}
		return linkKindUninspectable, "", err
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		switch {
		case fi.IsDir():
			return linkKindDirectory, "a directory", nil
		case fi.Mode().IsRegular():
			return linkKindRegularFile, "a regular file", nil
		default:
			return linkKindOther, "neither a regular file nor a symlink", nil
		}
	}
	raw, rlErr := os.Readlink(linkPath)
	if rlErr != nil {
		return linkKindUninspectable, "", rlErr
	}
	real, evalErr := filepath.EvalSymlinks(linkPath)
	if evalErr != nil {
		// Dangling: the raw target is the only useful thing to print.
		return linkKindDangling, raw, nil
	}
	if real == evalOrKeep(self) {
		return linkKindCorrect, raw, nil
	}
	return linkKindOtherTarget, raw, nil
}

// inspectLink collapses classifyLinkPath into the report's four states.
func inspectLink(linkPath, self string) (state, target, errMsg string) {
	kind, target, err := classifyLinkPath(linkPath, self)
	switch kind {
	case linkKindNothing:
		return linkMissing, "", ""
	case linkKindCorrect:
		return linkOK, target, ""
	case linkKindUninspectable:
		return linkError, "", err.Error()
	default:
		return linkConflict, target, ""
	}
}

// --- pdx path link (spec §3.2) -------------------------------------------

func runPathLink(env pathEnv, force bool, stdout, stderr io.Writer) int {
	dir := env.localBinDir()
	linkPath := filepath.Join(dir, "pdx")

	lock, err := acquirePathLock(dir, env.lock)
	if err != nil {
		fmt.Fprintf(stderr, "pdx path link: %v\n", err)
		return 1
	}
	defer lock.release()
	if env.afterLock != nil {
		env.afterLock()
	}

	// Classified inside the lock, immediately before acting (spec §3.2).
	kind, target, statErr := classifyLinkPath(linkPath, env.self)
	switch kind {
	case linkKindCorrect:
		fmt.Fprintf(stdout, "%s already points at this binary (%s)\n", linkPath, env.self)
		pathLinkHint(env, stdout)
		return 0

	case linkKindNothing:
		if err := os.Symlink(env.self, linkPath); err != nil {
			fmt.Fprintf(stderr, "pdx path link: cannot create %s: %v\n", linkPath, err)
			return 1
		}
		fmt.Fprintf(stdout, "created %s -> %s\n", linkPath, env.self)
		pathLinkHint(env, stdout)
		return 0

	case linkKindOtherTarget, linkKindDangling:
		what := "points at"
		if kind == linkKindDangling {
			what = "is a dangling symlink to"
		}
		if !force {
			fmt.Fprintf(stderr, "pdx path link: %s %s %s, not this binary (%s).\n", linkPath, what, target, env.self)
			fmt.Fprintf(stderr, "Re-run with --force to replace the symlink.\n")
			return 1
		}
		if err := replaceSymlinkAtomically(linkPath, env.self); err != nil {
			fmt.Fprintf(stderr, "pdx path link: cannot replace %s: %v\n", linkPath, err)
			return 1
		}
		fmt.Fprintf(stdout, "replaced %s (was %s) -> %s\n", linkPath, target, env.self)
		pathLinkHint(env, stdout)
		return 0

	case linkKindRegularFile, linkKindDirectory, linkKindOther:
		// --force deliberately does NOT override this. Replacing a real file
		// at that path would destroy data the user put there; replacing a
		// symlink only re-points a pointer (spec §3.2).
		fmt.Fprintf(stderr, "pdx path link: %s is %s, not a symlink. Refusing to touch it", linkPath, target)
		if force {
			fmt.Fprintf(stderr, " — --force replaces a symlink, never a regular file or a directory")
		}
		fmt.Fprintf(stderr, ".\nMove it aside yourself if you want pdx to manage that path.\n")
		return 1

	default: // linkKindUninspectable
		fmt.Fprintf(stderr, "pdx path link: cannot inspect %s: %v\n", linkPath, statErr)
		return 1
	}
}

// pathLinkHint says the other half of the job is still undone. A symlink in a
// directory that is not on PATH fixes nothing.
func pathLinkHint(env pathEnv, stdout io.Writer) {
	if pathContainsDir(env.path, env.localBinDir()) {
		return
	}
	fmt.Fprintf(stdout, "\nnote: %s is not on this PATH yet — run\n  %s path add-to-shell\n", env.localBinDir(), env.self)
}

// replaceSymlinkAtomically re-points linkPath at self without ever leaving
// the path empty: symlink to a temp name in the same directory, then rename
// over the destination. `Remove` followed by `Symlink` was rejected — it has
// the same TOCTOU and additionally leaves a window in which `pdx` does not
// exist at all, which is the state this whole feature exists to prevent.
//
// The honest limit (spec §3.2): rename(2) cannot be made conditional on the
// destination still being a symlink, so a non-pdx process racing us between
// the Lstat above and this rename is out of scope. Concurrent runs of pdx
// itself are serialised by the lockfile.
func replaceSymlinkAtomically(linkPath, self string) error {
	tmp := filepath.Join(filepath.Dir(linkPath), fmt.Sprintf(".pdx-link-%d-%d.tmp", os.Getpid(), time.Now().UnixNano()))
	if err := os.Symlink(self, tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, linkPath); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
