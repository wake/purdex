// Package tmuxenv makes the daemon's tmux invocations independent of the
// environment the daemon happened to be started from.
//
// Two things are wrong with an inherited environment, and both are invisible
// until they are not:
//
//   - `tmux` is looked up by bare name in ~35 places. A LaunchAgent gets
//     PATH=/usr/bin:/bin:/usr/sbin:/sbin, and macOS ships no /usr/bin/tmux —
//     so every one of those places fails separately, with an error that says
//     nothing about why.
//   - $TMUX, inherited from the pane `pdx start` was typed in, OVERRIDES the
//     socket path when neither -L nor -S is given (tmux.c's main). While it
//     equals the default socket this is invisible; when it does not, the
//     daemon addresses a different server and reports success for work the
//     user cannot see.
//
// Both are fixed in the process environment rather than at the call sites,
// which is what lets ~35 of them stay untouched — and what correctly leaves
// `pdx hook` alone, since that runs INSIDE a pane, in its own process, and
// genuinely needs the $TMUX it inherits.
//
// This mirrors internal/locale, which exists for the same shape of problem
// and is called from the same place in runServe.
package tmuxenv

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Action describes what Prepare did about finding tmux.
type Action int

const (
	// Found: the inherited PATH already resolves tmux. Nothing was changed.
	Found Action = iota
	// Prepended: tmux was not on PATH but was found in a probed directory,
	// which is now PATH's first element.
	Prepended
	// NotFound: tmux is on neither PATH nor any probed directory. PATH is
	// untouched and the caller is expected to say so loudly.
	NotFound
)

func (a Action) String() string {
	switch a {
	case Found:
		return "found"
	case Prepended:
		return "prepended"
	case NotFound:
		return "not-found"
	default:
		return "unknown"
	}
}

// Result is what one Prepare call did, in enough detail for a single honest
// log line.
type Result struct {
	Action Action
	// Resolved is the tmux binary that will now be executed. Empty on
	// NotFound.
	Resolved string
	// AddedDir is the directory prepended to PATH; empty unless Prepended.
	AddedDir string
	// Probed lists the directories that were looked in, so a NotFound log
	// can name them instead of leaving the user to guess.
	Probed []string
	// DroppedTMUX reports that $TMUX had actually been set — i.e. that this
	// daemon was started from inside a tmux pane. It describes what happened
	// rather than what Prepare always does, so the log line does not cry wolf
	// on every start.
	DroppedTMUX bool
}

// defaultProbeDirs names where a tmux that is missing from a service's PATH
// actually lives on macOS: Homebrew on Apple silicon, Homebrew on Intel (and
// hand-built installs), then a per-user install.
//
// A fixed list rather than a config knob: this exists to locate one specific
// binary, not to express a policy about PATH. config.NexConfig.PathPrepend is
// the knob, and it is a different question.
//
// The order deliberately differs from DefaultNexConfig().PathPrepend, which
// leads with ~/.local/bin: that list is about user-installed tooling in
// general, this one is about where tmux actually is. Leading with a per-user
// directory would let a stray shim outrank the real install.
func defaultProbeDirs() []string {
	dirs := []string{"/opt/homebrew/bin", "/usr/local/bin"}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		dirs = append(dirs, filepath.Join(home, ".local", "bin"))
	}
	return dirs
}

// Prepare fixes the process environment for every tmux exec that follows.
// Call it once, early, before anything reaches for tmux.
func Prepare() Result { return prepare(defaultProbeDirs()) }

// prepare is Prepare with the probe list injected, so tests can point it at
// temp directories instead of depending on what this machine has installed.
func prepare(probe []string) Result {
	res := Result{Probed: probe}

	// Order matters: drop $TMUX first so it cannot influence anything, even
	// if the lookup below were ever to grow a tmux call of its own.
	if v, ok := os.LookupEnv("TMUX"); ok && v != "" {
		res.DroppedTMUX = true
	}
	os.Unsetenv("TMUX")
	os.Unsetenv("TMUX_PANE")

	// An inherited PATH that already works is left exactly as it is. Rewriting
	// it would be a side effect on every other binary the daemon execs, for no
	// gain.
	if path, err := exec.LookPath("tmux"); err == nil {
		res.Action = Found
		res.Resolved = path
		return res
	}

	for _, dir := range probe {
		candidate := filepath.Join(dir, "tmux")
		if !isExecutableFile(candidate) {
			continue
		}
		// Prepend the DIRECTORY rather than remembering an absolute path: it
		// covers every call site without editing one, and the tmux server the
		// daemon spawns inherits the same PATH, so its panes can find tmux too.
		if existing := os.Getenv("PATH"); existing != "" {
			os.Setenv("PATH", dir+string(os.PathListSeparator)+existing)
		} else {
			os.Setenv("PATH", dir)
		}
		res.Action = Prepended
		res.AddedDir = dir
		res.Resolved = candidate
		return res
	}

	res.Action = NotFound
	return res
}

// isExecutableFile reports whether p is a regular file with an execute bit.
// A non-executable file named "tmux" is not a tmux: accepting one would
// prepend a directory that cannot satisfy a single call.
func isExecutableFile(p string) bool {
	fi, err := os.Stat(p)
	if err != nil || fi.IsDir() {
		return false
	}
	return fi.Mode().Perm()&0o111 != 0
}

// ProbedList renders Probed for a log line.
func (r Result) ProbedList() string { return strings.Join(r.Probed, ", ") }
