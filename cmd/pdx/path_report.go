package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

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
