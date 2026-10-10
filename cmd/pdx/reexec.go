// cmd/pdx/reexec.go
package main

import (
	"log"
	"os"
	"runtime"
	"strings"
	"syscall"

	"github.com/wake/purdex/internal/claudeenv"
)

// reexecPlan is how this serve process was started — captured at the top of
// runServe, before locale.EnsureUTF8, tmuxenv.Prepare and nex's PATH policy
// change the process env — so a restart (POST /api/daemon/restart) execs
// the same command line in the same environment: PDX_DEV_MODE survives, and
// nex's path_prepend is applied once by the new image rather than stacked on
// the old one's. path is resolved at boot but read from disk at exec, so a
// binary swapped in since is what runs (daemon restart spec §3.1).
type reexecPlan struct {
	path string
	argv []string
	env  []string
	// lock is the pid lock handed to the new image; held only so the GC
	// cannot close its fd before exec.
	lock *os.File
	// runLock is the config file's run lock (#2562), handed over the same way; nil when this daemon holds none.
	runLock *os.File
	// clearCLOEXEC lets lock's fd survive the exec; nil means
	// clearCloseOnExec. A seam for tests.
	clearCLOEXEC func(*os.File) error
}

func captureReexecPlan(executable func() (string, error), args, env []string) (*reexecPlan, error) {
	path, err := executable()
	if err != nil {
		return nil, err
	}
	// A stale hand-off entry (this image was itself a restart) must not be
	// passed on: runServe unsets it once read, but the capture runs first.
	kept := make([]string, 0, len(env))
	for _, kv := range env {
		if !strings.HasPrefix(kv, pidLockFDEnv+"=") && !strings.HasPrefix(kv, runLockFDEnv+"=") {
			kept = append(kept, kv)
		}
	}
	return &reexecPlan{
		path: path,
		argv: append([]string(nil), args...),
		env:  kept,
	}, nil
}

// captureCleanReexecPlan removes the Claude Code session-identity variables from this process's environment
// (a daemon started, or restarted, from inside a Claude Code session carries that session's id, messaging
// socket and token, pid, ... and would hand them to every process it starts, #2122), logs their names, and
// captures the plan from the environment that is left: a restart re-execs clean too.
func captureCleanReexecPlan(executable func() (string, error), args []string) (*reexecPlan, error) {
	if removed := claudeenv.ScrubProcess(); len(removed) > 0 {
		log.Printf("env: dropped %d inherited Claude Code session variable(s): %s", len(removed), strings.Join(removed, ", "))
	}
	return captureReexecPlan(executable, args, os.Environ())
}

// reexec replaces this process with the plan. It runs after runServe has
// returned, i.e. after the stores are closed; the pid lock is not released
// but handed across the exec (clearCloseOnExec), so there is no gap in which a
// concurrent `pdx start` could take it. The pid
// stays the same, so `pdx stop/status` and the App's ownership record stay
// valid.
//
// syscall.ForkLock is held (exclusively) from just before the CLOEXEC clear
// until execFn returns: os/exec forks under it, so a straggler goroutine
// cannot fork in the window where the lock fd is inheritable and leave a
// child that keeps the data dir locked after this process is gone. exec
// itself does not take ForkLock, so holding it across the call is safe. exec
// returns only on failure: close-on-exec is then restored before the unlock,
// and we log it and exit non-zero (the SPA then sees the host stay down and
// points at this log).
func reexec(p *reexecPlan, execFn func(string, []string, []string) error, logf func(string, ...any), exit func(int)) {
	logf("restart: exec %s", p.path)
	var held []*os.File
	for _, f := range []*os.File{p.lock, p.runLock} {
		if f != nil {
			held = append(held, f)
		}
	}
	if len(held) > 0 {
		syscall.ForkLock.Lock()
		// As late as possible: until here the fds are close-on-exec, so a child
		// forked while the stores closed cannot carry a lock past us. On
		// failure the exec closes the fd and the new image re-acquires.
		clear := p.clearCLOEXEC
		if clear == nil {
			clear = clearCloseOnExec
		}
		for i, f := range held {
			if cerr := clear(f); cerr != nil {
				what := "pid lock"
				if f == p.runLock && (i > 0 || p.lock == nil) {
					what = "run lock"
				}
				logf("%s: hand-off failed (%v); the new image re-acquires it", what, cerr)
			}
		}
	}
	err := execFn(p.path, p.argv, p.env)
	if len(held) > 0 {
		// exec failed and this process lives on until exit(1): make the fds
		// close-on-exec again before any fork can see them, then let forks resume.
		for _, f := range held {
			syscall.CloseOnExec(int(f.Fd()))
		}
		syscall.ForkLock.Unlock()
	}
	runtime.KeepAlive(p.lock)
	runtime.KeepAlive(p.runLock)
	logf("restart: exec %s failed: %v", p.path, err)
	exit(1)
}

// restartStillWanted is the last gate before re-exec (spec D4). stop ends
// signal delivery to sig — from then on SIGINT/SIGTERM take their default
// action and end the process, so a `pdx stop` racing the exec still stops
// it — and then any signal that arrived after serveAndWait's watcher had
// exited is taken from the buffer: one pending means the operator asked to
// stop, so no re-exec.
func restartStillWanted(sig <-chan os.Signal, stop func(), logf func(string, ...any)) bool {
	stop()
	select {
	case s := <-sig:
		logf("received %v before restart; exiting instead of restarting", s)
		return false
	default:
		return true
	}
}
