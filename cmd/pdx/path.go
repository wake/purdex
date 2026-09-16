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
	"encoding/json"
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
}

// pathUsage is the grammar-rejection message (exit 2), following the
// convention in cmd/pdx/peers.go.
const pathUsage = "usage: pdx path [--json]"

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

// --- the report (spec §3.1) ----------------------------------------------

// linkState describes what is at ~/.local/bin/pdx *now*. It deliberately has
// no "created" state: the Electron status block is recomputed on every poll
// and has no memory of actions, so "created" could only ever be a lie on the
// second poll (spec §4.6).
const (
	linkOK       = "ok"       // a symlink resolving to this binary
	linkMissing  = "missing"  // nothing at that path
	linkConflict = "conflict" // something else: another target, dangling, a file, a directory
	linkError    = "error"    // the path could not be inspected
)

// pathReport is the state `link` and `add-to-shell` act on, and the shape
// `--json` emits. The Electron gate consumes it verbatim (spec §4.1/§4.6);
// isSelf is what that side calls isManagedBinary, because it runs the managed
// binary to produce this report.
type pathReport struct {
	Self     string `json:"self"`
	SelfNote string `json:"selfNote,omitempty"`

	// Resolved is the `pdx` found on PATH, as found (it may itself be a
	// symlink); null when nothing on PATH is an executable named pdx.
	Resolved *string `json:"resolved"`
	// ResolvedReal is Resolved with symlinks evaluated, omitted when equal.
	ResolvedReal string `json:"resolvedReal,omitempty"`
	// IsSelf reports whether the pdx PATH finds is this very binary,
	// compared by resolved path. A stale pdx earlier on PATH is a real
	// configuration, and silently "working" while pointing at last month's
	// build is worse than not working.
	IsSelf bool `json:"isSelf"`

	LocalBin       string `json:"localBin"`
	LocalBinExists bool   `json:"localBinExists"`
	LocalBinOnPath bool   `json:"localBinOnPath"`

	Link       string `json:"link"`
	LinkTarget string `json:"linkTarget,omitempty"`
	LinkError  string `json:"linkError,omitempty"`

	// Fixes names which of the two commands would help: "link",
	// "add-to-shell", both, or neither.
	Fixes []string `json:"fixes"`
	// OK is the exit-status predicate: pdx resolves, and to this binary.
	OK bool `json:"ok"`
}

// localBinDir is ~/.local/bin for the injected home.
func (e pathEnv) localBinDir() string { return filepath.Join(e.home, ".local", "bin") }

// buildPathReport inspects the environment. It performs no writes.
func buildPathReport(env pathEnv) pathReport {
	rep := pathReport{
		Self:     env.self,
		SelfNote: env.selfNote,
		LocalBin: env.localBinDir(),
		Fixes:    []string{},
	}

	if found, err := lookPathIn(env.path, "pdx"); err == nil {
		rep.Resolved = &found
		real := evalOrKeep(found)
		if real != found {
			rep.ResolvedReal = real
		}
		rep.IsSelf = real == evalOrKeep(env.self)
	}

	if fi, err := os.Stat(rep.LocalBin); err == nil && fi.IsDir() {
		rep.LocalBinExists = true
	}
	rep.LocalBinOnPath = pathContainsDir(env.path, rep.LocalBin)

	rep.Link, rep.LinkTarget, rep.LinkError = inspectLink(filepath.Join(rep.LocalBin, "pdx"), env.self)

	rep.OK = rep.Resolved != nil && rep.IsSelf
	if !rep.OK {
		if rep.Link != linkOK {
			rep.Fixes = append(rep.Fixes, "link")
		}
		if !rep.LocalBinOnPath {
			rep.Fixes = append(rep.Fixes, "add-to-shell")
		}
	}
	return rep
}

// inspectLink classifies ~/.local/bin/pdx. os.Lstat, not os.Stat: os.Stat
// follows the link and reports a *dangling* symlink as "not there", which
// looks identical to an empty path and leads to a create that then fails with
// EEXIST and no explanation (spec §3.2).
func inspectLink(linkPath, self string) (state, target, errMsg string) {
	fi, err := os.Lstat(linkPath)
	if err != nil {
		if os.IsNotExist(err) {
			return linkMissing, "", ""
		}
		return linkError, "", err.Error()
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		kind := "a regular file"
		if fi.IsDir() {
			kind = "a directory"
		} else if !fi.Mode().IsRegular() {
			kind = "not a regular file"
		}
		return linkConflict, kind, ""
	}
	raw, err := os.Readlink(linkPath)
	if err != nil {
		return linkError, "", err.Error()
	}
	real, err := filepath.EvalSymlinks(linkPath)
	if err != nil {
		// Dangling: the raw target is the only useful thing to print.
		return linkConflict, raw, ""
	}
	if real == evalOrKeep(self) {
		return linkOK, raw, ""
	}
	return linkConflict, raw, ""
}

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

// lookPathIn is exec.LookPath against an injected PATH rather than the
// process's own. The semantics are deliberately the shell's: an entry holding
// a `pdx` that is not a regular executable file is skipped, because a shell
// skips it too. Reporting a non-executable file as reachable would be the
// same lie this whole feature exists to stop telling (spec §3.1).
func lookPathIn(pathVar, file string) (string, error) {
	for _, dir := range filepath.SplitList(pathVar) {
		if dir == "" {
			dir = "." // unix convention: an empty PATH entry means the cwd
		}
		candidate := filepath.Join(dir, file)
		fi, err := os.Stat(candidate) // Stat, not Lstat: a dangling link is not executable
		if err != nil {
			continue
		}
		if !fi.Mode().IsRegular() || fi.Mode().Perm()&0o111 == 0 {
			continue
		}
		return candidate, nil
	}
	return "", os.ErrNotExist
}

// pathContainsDir reports whether dir is one of PATH's entries, comparing
// resolved paths so that /var/... and /private/var/... are the same place.
func pathContainsDir(pathVar, dir string) bool {
	want := evalOrKeep(dir)
	for _, entry := range filepath.SplitList(pathVar) {
		if entry == "" {
			continue
		}
		if entry == dir || evalOrKeep(entry) == want {
			return true
		}
	}
	return false
}

// runPathReport prints the report and returns the exit status: 0 when pdx
// resolves to this binary, 1 otherwise, so it is usable from a script.
func runPathReport(env pathEnv, jsonOut bool, stdout io.Writer) int {
	rep := buildPathReport(env)
	if jsonOut {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(rep); err != nil {
			return 1
		}
		return exitFor(rep)
	}
	writePathReportText(rep, env, stdout)
	return exitFor(rep)
}

func exitFor(rep pathReport) int {
	if rep.OK {
		return 0
	}
	return 1
}

func writePathReportText(rep pathReport, env pathEnv, w io.Writer) {
	fmt.Fprintf(w, "this binary:      %s\n", rep.Self)
	if rep.SelfNote != "" {
		fmt.Fprintf(w, "                  note: %s\n", rep.SelfNote)
	}

	switch {
	case rep.Resolved == nil:
		fmt.Fprintf(w, "pdx on PATH:      not found\n")
	case rep.IsSelf:
		fmt.Fprintf(w, "pdx on PATH:      %s (this binary)\n", *rep.Resolved)
	default:
		fmt.Fprintf(w, "pdx on PATH:      %s (a DIFFERENT binary)\n", *rep.Resolved)
		if rep.ResolvedReal != "" {
			fmt.Fprintf(w, "                  -> %s\n", rep.ResolvedReal)
		}
	}

	exists, onPath := "missing", "not on PATH"
	if rep.LocalBinExists {
		exists = "exists"
	}
	if rep.LocalBinOnPath {
		onPath = "on PATH"
	}
	fmt.Fprintf(w, "~/.local/bin:     %s, %s\n", exists, onPath)

	switch rep.Link {
	case linkOK:
		fmt.Fprintf(w, "~/.local/bin/pdx: ok (symlink to this binary)\n")
	case linkMissing:
		fmt.Fprintf(w, "~/.local/bin/pdx: missing\n")
	case linkError:
		fmt.Fprintf(w, "~/.local/bin/pdx: cannot be inspected: %s\n", rep.LinkError)
	default:
		fmt.Fprintf(w, "~/.local/bin/pdx: conflict — %s\n", rep.LinkTarget)
	}

	fmt.Fprintln(w)
	if rep.OK {
		fmt.Fprintln(w, "pdx is reachable.")
		return
	}

	fmt.Fprintln(w, "pdx is NOT reachable from this PATH, so agents following CLAUDE.md")
	fmt.Fprintln(w, `will get "command not found".`)
	fmt.Fprintln(w)
	if len(rep.Fixes) == 0 {
		fmt.Fprintf(w, "Neither fix applies: ~/.local/bin is on PATH and holds a correct\n")
		fmt.Fprintf(w, "symlink, but another pdx earlier on PATH wins. Remove or reorder it.\n")
		return
	}
	fmt.Fprintln(w, "Fix it with:")
	fmt.Fprintln(w)
	for _, fix := range rep.Fixes {
		switch fix {
		case "link":
			fmt.Fprintf(w, "  %s path link           create ~/.local/bin/pdx\n", env.self)
		case "add-to-shell":
			fmt.Fprintf(w, "  %s path add-to-shell   put ~/.local/bin on PATH\n", env.self)
		}
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Then open a new terminal and run `pdx path` to confirm.")
}
