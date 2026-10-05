// cmd/pdx/reexec.go
package main

import (
	"os"
	"runtime"
	"strings"
	"syscall"
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
		if !strings.HasPrefix(kv, pidLockFDEnv+"=") {
			kept = append(kept, kv)
		}
	}
	return &reexecPlan{
		path: path,
		argv: append([]string(nil), args...),
		env:  kept,
	}, nil
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
	if p.lock != nil {
		syscall.ForkLock.Lock()
		// As late as possible: until here the fd is close-on-exec, so a child
		// forked while the stores closed cannot carry the lock past us. On
		// failure the exec closes the fd and the new image re-acquires.
		clear := p.clearCLOEXEC
		if clear == nil {
			clear = clearCloseOnExec
		}
		if cerr := clear(p.lock); cerr != nil {
			logf("pid lock: hand-off failed (%v); the new image re-acquires it", cerr)
		}
	}
	err := execFn(p.path, p.argv, p.env)
	if p.lock != nil {
		// exec failed and this process lives on until exit(1): make the fd
		// close-on-exec again before any fork can see it, then let forks resume.
		syscall.CloseOnExec(int(p.lock.Fd()))
		syscall.ForkLock.Unlock()
	}
	runtime.KeepAlive(p.lock)
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
