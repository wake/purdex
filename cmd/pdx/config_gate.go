package main

import (
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
// sees the daemon's run lock — it cannot slip in between. When the gate cannot be entered (the lock stays held, the directory is
// not writable) the daemon still starts, from the file, without a run lock, and says so: availability over the gate.
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
	if werr != nil && !entered {
		logf("config lock: %v; reading the config without the gate", werr)
		cfg, err = config.Load(cfgPath)
		return cfg, nil, err
	}
	return cfg, run, werr
}
