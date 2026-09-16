package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// pathEnv is everything `pdx path` is allowed to look at (spec §3).
//
// Injecting it is not ceremony: the tests for this command create symlinks,
// rewrite rc files and place lockfiles, and none of that may ever happen
// against the developer's real $HOME.
type pathEnv struct {
	self  string // os.Executable() + filepath.EvalSymlinks, done by the caller
	home  string
	path  string // raw $PATH
	shell string // raw $SHELL
	goos  string // runtime.GOOS; bash's rc file differs by OS

	// selfNote is set when EvalSymlinks failed for the executable path: the
	// unresolved path is kept and the report says it is unresolved (spec §3,
	// matching cmd/pdx/setup.go's handling). An EvalSymlinks error is not
	// fatal; only an os.Executable() error is.
	selfNote string

	// lock tunes the lockfile acquisition policy; the zero value is the
	// documented default (retry every 50ms up to 5s, stale after 30s).
	lock lockPolicy
	// afterLock, when set, runs immediately after the lock is acquired. It
	// is a test seam: it is how a test proves two subcommands contend for
	// the same lock. Never set outside tests.
	afterLock func()
	// beforeLock, when set, runs after add-to-shell has resolved the rc file
	// but before it takes the lock. It is a test seam: it is how a test
	// proves the content written is built from the read taken *inside* the
	// lock (spec §7.3's stale-read case). Never set outside tests.
	beforeLock func()
}

// pathUsage is the grammar-rejection message (exit 2), following the
// convention in cmd/pdx/peers.go.
const pathUsage = "usage: pdx path [--json]\n" +
	"       pdx path link [--force]\n" +
	"       pdx path add-to-shell [--dry-run]"

// runPath is the `pdx path` switch target.
func runPath(args []string) {
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "pdx path: cannot determine this binary's path: %v\n", err)
		os.Exit(1)
	}
	self, note := resolveSelfPath(exe)
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "pdx path: cannot determine home directory: %v\n", err)
		os.Exit(1)
	}
	env := pathEnv{
		self:     self,
		home:     home,
		path:     os.Getenv("PATH"),
		shell:    os.Getenv("SHELL"),
		goos:     runtime.GOOS,
		selfNote: note,
	}
	os.Exit(runPathCmd(env, args, os.Stdout, os.Stderr))
}

// resolveSelfPath applies the os.Executable()+EvalSymlinks pairing this
// command needs (spec §3): every comparison `pdx path` makes is about which
// *binary* is which, and os.Executable() alone may report the symlink the
// user invoked. A failed EvalSymlinks keeps the unresolved path and returns a
// note for the report rather than failing the command — the same handling
// cmd/pdx/setup.go's localSetup uses.
func resolveSelfPath(exe string) (self string, note string) {
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return exe, fmt.Sprintf("could not be resolved (%v); the unresolved path is used", err)
	}
	return resolved, ""
}

// runPathCmd implements the full `pdx path` grammar and returns the process
// exit code, so tests drive it without os.Exit. Every grammar rejection
// returns 2.
func runPathCmd(env pathEnv, args []string, stdout, stderr io.Writer) int {
	sub := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub, args = args[0], args[1:]
	}

	switch sub {
	case "":
		jsonOut := false
		for _, a := range args {
			switch a {
			case "--json":
				jsonOut = true
			default:
				return pathFlagError(stderr, a)
			}
		}
		return runPathReport(env, jsonOut, stdout)
	case "link":
		force := false
		for _, a := range args {
			switch a {
			case "--force":
				force = true
			default:
				return pathFlagError(stderr, a)
			}
		}
		return runPathLink(env, force, stdout, stderr)
	case "add-to-shell":
		dryRun := false
		for _, a := range args {
			switch a {
			case "--dry-run":
				dryRun = true
			default:
				return pathFlagError(stderr, a)
			}
		}
		return runPathAddToShell(env, dryRun, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "pdx path: unknown subcommand %q\n", sub)
		fmt.Fprintln(stderr, pathUsage)
		return 2
	}
}

// pathFlagError rejects an unrecognized argument. A non-flag positional gets
// the generic usage; a flag gets the more specific message, as `pdx peers`
// does.
func pathFlagError(stderr io.Writer, arg string) int {
	if strings.HasPrefix(arg, "-") {
		fmt.Fprintf(stderr, "pdx path: unknown flag %s\n", arg)
	} else {
		fmt.Fprintf(stderr, "pdx path: unexpected argument %q\n", arg)
	}
	fmt.Fprintln(stderr, pathUsage)
	return 2
}

// localBinDir is ~/.local/bin for the injected home.
func (e pathEnv) localBinDir() string { return filepath.Join(e.home, ".local", "bin") }
