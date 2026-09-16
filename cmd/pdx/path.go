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
	"time"
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

	// "not reachable" and "not me" are different failures and must not be
	// reported with the same sentence: on a machine whose PATH finds a
	// different pdx, `pdx` works — saying agents will get "command not
	// found" would be plainly false, and a false diagnosis sends the reader
	// to fix something that is not broken.
	if rep.Resolved != nil {
		fmt.Fprintln(w, "pdx IS reachable, but it is a different binary than this one.")
		fmt.Fprintln(w, "Commands will run that other build, not this one.")
	} else {
		fmt.Fprintln(w, "pdx is NOT reachable from this PATH, so agents following CLAUDE.md")
		fmt.Fprintln(w, `will get "command not found".`)
	}
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

// --- the lockfile (spec §3.2) --------------------------------------------

// pathLockName is the single lock guarding every mutation `pdx path` makes,
// held in ~/.local/bin. One lock, not two: `link` and `add-to-shell` change
// the same thing — whether `pdx` is reachable — and a second lock beside the
// rc file would only ever protect pdx from pdx, which this one already does.
// It cannot protect the rc file from the user's editor; no lockfile can,
// because editors do not take it (spec §3.2).
const pathLockName = ".pdx-path.lock"

// lockPolicy is the acquisition policy. The zero value means the defaults;
// tests shorten the durations so a contention test does not take five
// seconds.
type lockPolicy struct {
	retryEvery time.Duration
	timeout    time.Duration
	staleAfter time.Duration
}

func (p lockPolicy) withDefaults() lockPolicy {
	if p.retryEvery <= 0 {
		p.retryEvery = 50 * time.Millisecond
	}
	if p.timeout <= 0 {
		p.timeout = 5 * time.Second
	}
	if p.staleAfter <= 0 {
		p.staleAfter = 30 * time.Second
	}
	return p
}

// pathLock is a held lockfile.
type pathLock struct{ path string }

// acquirePathLock takes the lock in dir, creating dir if needed — both
// commands need ~/.local/bin to exist anyway, and the lock must live
// somewhere. It retries on contention and breaks a lockfile left behind by a
// process that died holding it, judged by mtime.
func acquirePathLock(dir string, pol lockPolicy) (*pathLock, error) {
	pol = pol.withDefaults()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("cannot create %s: %w", dir, err)
	}
	lockPath := filepath.Join(dir, pathLockName)
	deadline := time.Now().Add(pol.timeout)

	for {
		f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			fmt.Fprintf(f, "%d\n", os.Getpid())
			f.Close()
			return &pathLock{path: lockPath}, nil
		}
		if !os.IsExist(err) {
			return nil, fmt.Errorf("cannot create %s: %w", lockPath, err)
		}

		// A lockfile older than staleAfter belonged to a process that is no
		// longer going to release it. Break it rather than deadlock.
		broke := false
		if fi, statErr := os.Stat(lockPath); statErr == nil && time.Since(fi.ModTime()) > pol.staleAfter {
			if rmErr := os.Remove(lockPath); rmErr == nil {
				broke = true
			}
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("could not acquire the lock %s within %s; another pdx path command is running (delete the file if it is not)", lockPath, pol.timeout)
		}
		if !broke {
			time.Sleep(pol.retryEvery)
		}
	}
}

func (l *pathLock) release() error {
	if l == nil {
		return nil
	}
	if err := os.Remove(l.path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
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

// --- pdx path add-to-shell (spec §3.3) -----------------------------------

// The block is written with markers so a later run recognises its own work.
const (
	pathBlockStart   = "# >>> pdx path >>>"
	pathBlockEnd     = "# <<< pdx path <<<"
	pathExportLine   = `export PATH="$HOME/.local/bin:$PATH"`
	pathBackupSuffix = ".pdx-backup"
)

// pathBlock is the exact text written to the rc file, newline-terminated.
func pathBlock() string {
	return pathBlockStart + "\n" + pathExportLine + "\n" + pathBlockEnd + "\n"
}

// rcFileFor picks the rc file from $SHELL's basename. Anything else — fish,
// an unset $SHELL in a packaged app — is refused rather than guessed at:
// guessing a config format for a shell we did not recognise is how a config
// file gets corrupted, and fish does not even use `export` (spec §3.3).
func rcFileFor(env pathEnv) (string, bool) {
	switch filepath.Base(env.shell) {
	case "zsh":
		return filepath.Join(env.home, ".zshrc"), true
	case "bash":
		// The usual login-vs-interactive split.
		if env.goos == "darwin" {
			return filepath.Join(env.home, ".bash_profile"), true
		}
		return filepath.Join(env.home, ".bashrc"), true
	}
	return "", false
}

func runPathAddToShell(env pathEnv, dryRun bool, stdout, stderr io.Writer) int {
	rcPath, ok := rcFileFor(env)
	if !ok {
		shown := env.shell
		if shown == "" {
			shown = "(unset)"
		}
		fmt.Fprintf(stderr, "pdx path add-to-shell: unrecognised shell %s — refusing to guess its config format.\n", shown)
		printManualBlock(stderr)
		return 1
	}

	// Resolve the rc file *before* anything else: when it is a symlink — the
	// normal shape under any dotfiles manager — the temp-file-plus-rename
	// below would replace the symlink with a regular file, quietly detaching
	// the user's shell config from the repository that manages it (spec §3.3).
	writePath, err := resolveRcTarget(env, rcPath)
	if err != nil {
		fmt.Fprintf(stderr, "pdx path add-to-shell: %v\n", err)
		printManualBlock(stderr)
		return 1
	}

	if dryRun {
		// No lock, and no ~/.local/bin created: a dry run writes nothing at
		// all, directories included.
		fmt.Fprintf(stdout, "would edit %s\n", writePath)
		if writePath != rcPath {
			fmt.Fprintf(stdout, "(%s is a symlink; its target is what would be edited)\n", rcPath)
		}
		if body, err := os.ReadFile(writePath); err == nil && strings.Contains(string(body), pathBlockStart) {
			fmt.Fprintf(stdout, "(the block is already present; a real run would write nothing)\n")
		}
		fmt.Fprintf(stdout, "\n%s\n", pathBlock())
		fmt.Fprintln(stdout, "Nothing was written (--dry-run).")
		return 0
	}

	if env.beforeLock != nil {
		env.beforeLock()
	}

	// One lock, shared with `link` (spec §3.2). Every decision below is made
	// inside it against a fresh read, so two concurrent runs produce one
	// block and a run whose earlier read went stale cannot write a version
	// that drops someone else's edit — it never uses an earlier read.
	lock, err := acquirePathLock(env.localBinDir(), env.lock)
	if err != nil {
		fmt.Fprintf(stderr, "pdx path add-to-shell: %v\n", err)
		return 1
	}
	defer lock.release()
	if env.afterLock != nil {
		env.afterLock()
	}

	content, mode, existed, err := readRcFile(writePath)
	if err != nil {
		fmt.Fprintf(stderr, "pdx path add-to-shell: cannot read %s: %v\n", writePath, err)
		return 1
	}

	if strings.Contains(content, pathBlockStart) {
		fmt.Fprintf(stdout, "%s already contains the pdx path block; nothing to do.\n", writePath)
		fmt.Fprintln(stdout, "If pdx is still not found, open a new terminal, then run `pdx path`.")
		return 0
	}
	// The goal is a working PATH, not a block in a file.
	if pathContainsDir(env.path, env.localBinDir()) {
		fmt.Fprintf(stdout, "%s is already on PATH; nothing to do.\n", env.localBinDir())
		return 0
	}

	if existed {
		if err := backupOnce(writePath, content, mode); err != nil {
			fmt.Fprintf(stderr, "pdx path add-to-shell: cannot back up %s: %v\n", writePath, err)
			return 1
		}
	}

	next := content
	// Appending straight onto a file whose last line has no newline would
	// splice the marker comment onto the end of a live shell command.
	if len(next) > 0 && !strings.HasSuffix(next, "\n") {
		next += "\n"
	}
	next += pathBlock()

	if err := writeFileAtomically(writePath, []byte(next), mode); err != nil {
		fmt.Fprintf(stderr, "pdx path add-to-shell: cannot write %s: %v\n", writePath, err)
		return 1
	}

	fmt.Fprintf(stdout, "added the pdx path block to %s\n", writePath)
	if existed {
		fmt.Fprintf(stdout, "a copy of the previous file is at %s\n", writePath+pathBackupSuffix)
	}
	// A child process cannot change its parent's environment, so saying
	// anything else here would be a lie.
	fmt.Fprintln(stdout, "Open a new terminal, then run `pdx path` to confirm.")
	return 0
}

func printManualBlock(w io.Writer) {
	fmt.Fprintln(w, "Add this to your shell's startup file yourself:")
	fmt.Fprintf(w, "\n%s\n", pathBlock())
}

// resolveRcTarget returns the file that should actually be written: the rc
// path itself, or — when that is a symlink — its target, so the link survives
// (spec §3.3). A symlink that does not resolve, or resolves outside $HOME, is
// refused: editing a file somewhere else in the filesystem on the strength of
// a link is not something this command should decide to do on its own.
func resolveRcTarget(env pathEnv, rcPath string) (string, error) {
	fi, err := os.Lstat(rcPath)
	if err != nil {
		if os.IsNotExist(err) {
			return rcPath, nil
		}
		return "", fmt.Errorf("cannot inspect %s: %w", rcPath, err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		if fi.IsDir() {
			return "", fmt.Errorf("%s is a directory", rcPath)
		}
		if !fi.Mode().IsRegular() {
			return "", fmt.Errorf("%s is not a regular file", rcPath)
		}
		return rcPath, nil
	}
	target, evalErr := filepath.EvalSymlinks(rcPath)
	if evalErr != nil {
		raw, _ := os.Readlink(rcPath)
		return "", fmt.Errorf("%s is a symlink to %s, which does not resolve", rcPath, raw)
	}
	if !underDir(env.home, target) {
		return "", fmt.Errorf("%s is a symlink to %s, which is outside %s", rcPath, target, env.home)
	}
	return target, nil
}

// underDir reports whether target sits inside dir, comparing both the given
// and the symlink-resolved form of dir: on macOS a $HOME under /var is really
// under /private/var, and comparing a resolved path against an unresolved one
// would refuse a perfectly ordinary dotfiles layout.
func underDir(dir, target string) bool {
	for _, base := range []string{dir, evalOrKeep(dir)} {
		if base == "" {
			continue
		}
		if target == base || strings.HasPrefix(target, base+string(os.PathSeparator)) {
			return true
		}
	}
	return false
}

// readRcFile reads the file to be edited. A missing file is not an error: it
// will be created 0644.
func readRcFile(p string) (content string, mode os.FileMode, existed bool, err error) {
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return "", 0o644, false, nil
		}
		return "", 0, false, err
	}
	mode = os.FileMode(0o644)
	if fi, statErr := os.Stat(p); statErr == nil {
		mode = fi.Mode().Perm()
	}
	return string(b), mode, true, nil
}

// backupOnce copies the file to <file>.pdx-backup. An existing backup is
// never overwritten, so the copy always represents the state before pdx ever
// touched the file.
func backupOnce(p, content string, mode os.FileMode) error {
	backup := p + pathBackupSuffix
	if _, err := os.Lstat(backup); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	return os.WriteFile(backup, []byte(content), mode)
}

// writeFileAtomically writes via a temp file in the same directory plus a
// rename, so an interrupted run cannot truncate a shell config.
func writeFileAtomically(p string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(p)
	f, err := os.CreateTemp(dir, ".pdx-path-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp) // no-op once the rename has succeeded

	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp, mode); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}
