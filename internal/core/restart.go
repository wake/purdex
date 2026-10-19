// internal/core/restart.go
package core

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"
)

// newBootID returns 16 hex chars that are new on every process start: a
// restart re-execs and runs New again, so the SPA can tell "the daemon came
// back" from "the old process is still answering" (daemon restart spec §3.1).
func newBootID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand does not fail on supported platforms; time still differs per start.
		return timeBootID(time.Now())
	}
	return hex.EncodeToString(b[:])
}

// timeBootID is newBootID's fallback, zero-padded so it keeps the 16-char shape.
func timeBootID(now time.Time) string {
	return fmt.Sprintf("%016x", uint64(now.UnixNano()))
}

// Core lifecycle as the restart endpoint sees it (spec §3.1).
const (
	lifeRunning    int32 = iota
	lifeRestarting       // a restart was accepted (202 sent)
	lifeStopping         // the shutdown sequence started for another reason
)

// BeginShutdown is called once by serve's shutdown sequence the moment it
// starts, whatever started it. From then on the restart endpoint answers
// 503 shutting_down rather than accept a restart nothing would perform. It
// reports whether a restart had already been accepted (202 sent): the
// sequence honours that one unless a signal started it (spec D4 — a stop
// request wins).
func (c *Core) BeginShutdown() (restartAccepted bool) {
	if c.life.CompareAndSwap(lifeRunning, lifeStopping) {
		return false
	}
	return c.life.Load() == lifeRestarting
}

// SetRestartHook installs what POST /api/daemon/restart calls once its 202
// is flushed. serve installs a non-blocking trigger of the shutdown sequence
// (cmd/pdx). Must be called before the server starts; fn must not block —
// the HTTP shutdown waits for this handler to return.
func (c *Core) SetRestartHook(fn func()) { c.restartHook = fn }

// Restart refusals, in the order RequestRestart checks them.
var (
	ErrRestartUnavailable = errors.New("restart_unavailable")
	ErrRestartInProgress  = errors.New("restart_in_progress")
	ErrShuttingDown       = errors.New("shutting_down")
)

// RequestRestart accepts a restart: CAS to lifeRestarting, log who asked, then
// fire the hook (serve's normal shutdown, then re-exec). execPath, when not
// empty, is the executable the re-exec runs instead of the one this process
// booted from (dev rebuild swaps in a freshly built binary elsewhere); it is
// read back with RestartExecPath. afterAccept, when not nil, runs once the
// restart is accepted and before the hook (the HTTP handler flushes its 202
// there). On refusal nothing runs and the error says why.
func (c *Core) RequestRestart(reason, execPath string, afterAccept func()) error {
	if c.restartHook == nil {
		return ErrRestartUnavailable
	}
	// Held across the CAS and the path store: shutdown may observe
	// lifeRestarting (BeginShutdown) and read the path (RestartExecPath)
	// before this returns, e.g. when Serve fails in between.
	c.restartMu.Lock()
	if !c.life.CompareAndSwap(lifeRunning, lifeRestarting) {
		c.restartMu.Unlock()
		if c.life.Load() == lifeRestarting {
			return ErrRestartInProgress
		}
		return ErrShuttingDown
	}
	if execPath != "" {
		c.restartExecPath = execPath
	}
	c.restartMu.Unlock()
	log.Printf("daemon restart requested by %s", reason)
	if afterAccept != nil {
		afterAccept()
	}
	c.restartHook()
	return nil
}

// RestartExecPath is the executable an accepted restart asked to exec, or ""
// for the one this process booted from.
func (c *Core) RestartExecPath() string {
	c.restartMu.Lock()
	defer c.restartMu.Unlock()
	return c.restartExecPath
}

// handleDaemonRestart is POST /api/daemon/restart (spec §3.1): 202 with the
// current boot id, flushed, then the hook. A second request while one is
// under way gets 409 with the same boot id so its client can follow the
// restart already in flight; once the shutdown has begun for any other
// reason it gets 503 shutting_down. No hook → 503: a 202 here would promise a
// restart nothing will perform.
func (c *Core) handleDaemonRestart(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	err := c.RequestRestart(r.RemoteAddr, "", func() {
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(map[string]string{"boot_id": c.BootID})
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	})
	switch {
	case err == nil:
	case errors.Is(err, ErrRestartInProgress):
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]string{"error": "restart_in_progress", "boot_id": c.BootID})
	case errors.Is(err, ErrShuttingDown):
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{"error": "shutting_down"})
	default:
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{"error": "restart_unavailable"})
	}
}
