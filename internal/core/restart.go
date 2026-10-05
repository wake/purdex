// internal/core/restart.go
package core

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"time"
)

// newBootID returns 16 hex chars that are new on every process start: a
// restart re-execs and runs New again, so the SPA can tell "the daemon came
// back" from "the old process is still answering" (daemon restart spec §3.1).
func newBootID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand does not fail on supported platforms; time still differs per start.
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return hex.EncodeToString(b[:])
}

// SetRestartHook installs what POST /api/daemon/restart calls once its 202
// is flushed. serve installs a non-blocking trigger of the shutdown sequence
// (cmd/pdx). Must be called before the server starts; fn must not block —
// the HTTP shutdown waits for this handler to return.
func (c *Core) SetRestartHook(fn func()) { c.restartHook = fn }

// handleDaemonRestart is POST /api/daemon/restart (spec §3.1): 202 with the
// current boot id, flushed, then the hook. A second request while one is
// under way gets 409 with the same boot id so its client can follow the
// restart already in flight. No hook → 503: a 202 here would promise a
// restart nothing will perform.
func (c *Core) handleDaemonRestart(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if c.restartHook == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{"error": "restart_unavailable"})
		return
	}
	if !c.restarting.CompareAndSwap(false, true) {
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]string{"error": "restart_in_progress", "boot_id": c.BootID})
		return
	}
	log.Printf("daemon restart requested by %s", r.RemoteAddr)
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]string{"boot_id": c.BootID})
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	c.restartHook()
}
