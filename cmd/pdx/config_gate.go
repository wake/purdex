package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/wake/purdex/internal/config"
)

// runLockFDEnv names the run-lock fd a restart hands to its new image (the pid lock's twin: pidLockFDEnv).
const runLockFDEnv = "PDX_RUNLOCK_FD"

// runLockEnvEntry is the env entry that names f's fd for the new image; like pidLockEnvEntry it has no side effect.
func runLockEnvEntry(f *os.File) string { return fmt.Sprintf("%s=%d", runLockFDEnv, f.Fd()) }

// openConfigGate is the daemon's start-up gate (#2562): inside "<config>.lock" it reads the config and takes (or adopts, after
// a restart) the run lock of that file, which it then holds for as long as it runs. A writer that wants to change the file while
// a daemon uses it (`pdx token generate`) takes the same lock first, so it either finishes before the daemon reads the file or
// sees the daemon's run lock — it cannot slip in between. A lock that stays held past the wait refuses a cold start (pdx start
// and the App report the failure to a person); a restart, which inherits the run lock and needs no gate, starts on it; a gate that cannot be entered for another reason (the directory is not writable — `token generate` cannot work
// there either) lets the daemon start from the file without a run lock, and says so.
func openConfigGate(cfgPath string, logf func(string, ...any)) (cfg config.Config, run *os.File, err error) {
	gate := cfgPath
	if gate == "" {
		gate = config.DefaultPath()
	}
	handed, haveHanded := os.LookupEnv(runLockFDEnv)
	os.Unsetenv(runLockFDEnv) // children (tmux, nex turns) must not see a stale fd number
	entered := false
	var loadErr error
	werr := config.WithLock(gate, func(locked string) error {
		entered = true
		if cfg, loadErr = config.Load(locked); loadErr != nil { // the path that is locked, not the name it was reached by
			return loadErr
		}
		if haveHanded {
			f, aerr := config.AdoptRunLock(handed, locked)
			if aerr == nil {
				logf("run lock: adopted from the previous image")
				run = f
				return nil
			}
			logf("run lock: inherited fd not adopted (%v); taking it", aerr)
		}
		f, herr := config.HoldRunLock(locked)
		if herr != nil {
			logf("run lock: %v; starting without it (a writer will not see this daemon through the config file)", herr)
			return nil
		}
		run = f
		return nil
	})
	if loadErr != nil {
		return cfg, nil, loadErr
	}
	if werr != nil && !entered && errors.Is(werr, config.ErrLockTimeout) && haveHanded {
		// A restart has no launcher to try again, and it does not need the gate: the previous image's run lock is inherited, held
		// without a gap, so `pdx token generate` sees it and cannot change the file under this daemon. Read the file where it is
		// and take the inherited lock over; only a hand-over that cannot be adopted falls through to the refusal below.
		locked := config.CanonicalPath(gate)
		if f, aerr := config.AdoptRunLock(handed, locked); aerr == nil {
			logf("config lock: %v; restart: reading the config without the gate, on the inherited run lock", werr)
			cfg, err = config.Load(locked)
			return cfg, f, err
		} else {
			logf("run lock: inherited fd not adopted (%v)", aerr)
		}
	}
	if werr != nil && !entered && errors.Is(werr, config.ErrLockTimeout) {
		// Another writer has held the lock for the whole wait. Starting anyway would read the file outside the gate and leave a
		// window before the run lock in which `pdx token generate` sees no daemon and changes the token under it: refuse, the
		// launcher starts it again.
		return cfg, nil, fmt.Errorf("the config is being written by another process: %w", werr)
	}
	if werr != nil && !entered {
		logf("config lock: %v; reading the config without the gate", werr)
		cfg, err = config.Load(cfgPath)
		return cfg, nil, err
	}
	return cfg, run, werr
}
