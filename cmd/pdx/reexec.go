// cmd/pdx/reexec.go
package main

import "os"

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
}

func captureReexecPlan(executable func() (string, error), args, env []string) (*reexecPlan, error) {
	path, err := executable()
	if err != nil {
		return nil, err
	}
	return &reexecPlan{
		path: path,
		argv: append([]string(nil), args...),
		env:  append([]string(nil), env...),
	}, nil
}

// reexec replaces this process with the plan. It runs after runServe has
// returned, i.e. after the stores are closed and the pid lock released; the
// new image re-takes the lock through mustAcquirePidLock's retry. The pid
// stays the same, so `pdx stop/status` and the App's ownership record stay
// valid. exec returns only on failure: log it and exit non-zero (the SPA
// then sees the host stay down and points at this log).
func reexec(p *reexecPlan, execFn func(string, []string, []string) error, logf func(string, ...any), exit func(int)) {
	logf("restart: exec %s", p.path)
	err := execFn(p.path, p.argv, p.env)
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
