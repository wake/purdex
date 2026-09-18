package session

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"regexp"
	"time"

	"github.com/wake/purdex/internal/tmux"
)

var nameRegex = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// --- HTTP Handlers ---

func (m *SessionModule) handleList(w http.ResponseWriter, r *http.Request) {
	sessions, err := m.cachedListSessions()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Return empty array, not null
	if sessions == nil {
		sessions = []SessionInfo{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(sessions)
}

func (m *SessionModule) cachedListSessions() ([]SessionInfo, error) {
	m.listCacheMu.Lock()
	defer m.listCacheMu.Unlock()
	if time.Since(m.listCacheAt) < listCacheTTL && m.listCacheData != nil {
		return m.listCacheData, nil
	}
	sessions, err := m.ListSessions()
	if err != nil {
		return nil, err
	}
	m.listCacheData = sessions
	m.listCacheAt = time.Now()
	return sessions, nil
}

func (m *SessionModule) handleGet(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	info, err := m.GetSession(code)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if info == nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(info)
}

type createRequest struct {
	Name string `json:"name"`
	Cwd  string `json:"cwd"`
	Mode string `json:"mode"`
}

func (m *SessionModule) handleCreate(w http.ResponseWriter, r *http.Request) {
	var req createRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	// Validate and normalise mode. Since P-D.2 the only mode is `terminal`;
	// the legacy `stream` value is still accepted (old workspace snapshots
	// and device-state backups may carry it) and coerced, never rejected.
	// (CreateSession records `terminal`; the coercion is kept here so the
	// legacy value is answered the way it always was.)
	switch req.Mode {
	case "", "terminal", "stream":
	default:
		http.Error(w, "invalid mode: must be terminal", http.StatusBadRequest)
		return
	}

	// Name rule, cwd resolution and the HasSession→NewSession→SetMeta
	// critical section live in CreateSession (shared with the nex module);
	// this handler only maps its stages onto the HTTP codes and texts the
	// SPA has always seen.
	info, err := m.CreateSession(req.Name, req.Cwd)
	if err != nil {
		var ce *CreateError
		if !errors.As(err, &ce) {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		switch ce.Stage {
		case CreateStageInvalidName:
			http.Error(w, "invalid session name: must match ^[a-zA-Z0-9_-]+$", http.StatusBadRequest)
		case CreateStageInvalidCwd:
			http.Error(w, "invalid cwd: "+ce.Err.Error(), http.StatusBadRequest)
		case CreateStageExists:
			http.Error(w, "session already exists: "+ce.Name, http.StatusConflict)
		default:
			http.Error(w, ce.Err.Error(), http.StatusInternalServerError)
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(info)
}

// sameDirectory reports whether two paths name one and the same directory.
//
// Two spellings of the same directory are the normal case, not the exception:
// getcwd() — which is where tmux's `#{session_path}` comes from — resolves
// symlinks and filesystem case, so the path that comes back out of tmux is
// frequently not the path that went in. os.SameFile compares what the two
// paths actually resolve to, which is the only comparison that tells a rename
// of the spelling apart from a change of directory.
//
// A path that cannot be stat'd is not the same directory as anything: the
// interesting divergence is precisely the one where the requested directory no
// longer exists.
func sameDirectory(a, b string) bool {
	ai, err := os.Stat(a)
	if err != nil {
		return false
	}
	bi, err := os.Stat(b)
	if err != nil {
		return false
	}
	return os.SameFile(ai, bi)
}

type renameRequest struct {
	Name string `json:"name"`
}

func (m *SessionModule) handleRename(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")

	var req renameRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.Name == "" || !nameRegex.MatchString(req.Name) {
		http.Error(w, "invalid session name: must match ^[a-zA-Z0-9_-]+$", http.StatusBadRequest)
		return
	}

	info, err := m.GetSession(code)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if info == nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	// Check for duplicate target name
	if req.Name != info.Name && m.tmux.HasSession(req.Name) {
		http.Error(w, "session already exists: "+req.Name, http.StatusConflict)
		return
	}

	if err := m.renameSessionAtomic(info.Name, req.Name); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	m.invalidateNameCache()

	// Return updated info with new name
	info.Name = req.Name
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(info)
}

func (m *SessionModule) handleDelete(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")

	info, err := m.GetSession(code)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if info == nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	// Kill tmux session by name
	if err := m.tmux.KillSession(info.Name); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Delete meta
	_ = m.meta.DeleteMeta(info.TmuxID)

	m.invalidateNameCache()

	w.WriteHeader(http.StatusNoContent)
}

type sendKeysRequest struct {
	Keys string `json:"keys"`

	// ExpectedTmuxInstance is the tmux generation the caller believes this
	// session code belongs to (spec §4.6.2). Optional: absent or "" means the
	// caller states no expectation and the keys go to whatever the code
	// resolves to now — which is what Quick Commands and `executeCommand`
	// do, and their behaviour is unchanged.
	//
	// When it IS stated, the daemon checks it. Codes are a reversible encoding
	// of the tmux id `$N`, so after a tmux server restart `$0` mints the same
	// code and a caller holding a recorded code can address a session it has
	// never seen. Only the daemon knows the current generation at the moment
	// it acts, so this is the only place the check can be authoritative — a
	// client-side cache comparison is a hint, not a precondition.
	ExpectedTmuxInstance string `json:"expected_tmux_instance"`
}

func (m *SessionModule) handleSendKeys(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")

	var req sendKeysRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.Keys == "" {
		http.Error(w, "keys must not be empty", http.StatusBadRequest)
		return
	}

	info, err := m.GetSession(code)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if info == nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	// A stated expectation is checked BY THE SERVER THAT RECEIVES THE KEYS, in
	// one tmux invocation (`tmux.SendKeysIfInstance`).
	//
	// Comparing `info.TmuxInstance` here would not do it, however freshly it
	// were re-sampled. That value was read by an earlier, separate tmux
	// invocation; between it and the send sit `ActivePaneMetadata`'s
	// subprocesses, a DB read, and then a NEW tmux connection resolving the
	// target. A server restart inside that window passes the check and
	// delivers the keys to the new server — and because a session code is a
	// reversible encoding of `$N`, the new server has the same id. Any check
	// that is a separate invocation from the send has that window; only one
	// that shares the send's connection does not.
	//
	// The daemon's own sample survives for one thing only: `info.TmuxID` is
	// the target, an id rather than a name, so a rename cannot re-point it.
	if req.ExpectedTmuxInstance != "" {
		// An expectation that cannot be compared at all is a bad request, not
		// a verdict about the session — and it must not reach a tmux format.
		if !tmux.ValidInstance(req.ExpectedTmuxInstance) {
			http.Error(w, "invalid expected_tmux_instance", http.StatusBadRequest)
			return
		}
		sent, err := m.tmux.SendKeysIfInstance(info.TmuxID, req.ExpectedTmuxInstance, req.Keys)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if !sent {
			// Unknown authorises nothing either: a server that could not
			// report a generation cannot satisfy an expectation, and declines.
			http.Error(w, "session "+code+" belongs to another tmux generation", http.StatusConflict)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// No expectation stated — Quick Commands and `executeCommand`. Unchanged.
	if err := m.tmux.SendKeysRaw("="+info.Name+":", req.Keys); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (m *SessionModule) handleTerminalWS(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	m.HandleTerminalWS(w, r, code)
}

// agentEventsRenamer is the optional interface implemented by the agent
// events store, used to rename stored events when a tmux session is renamed.
type agentEventsRenamer interface {
	Rename(oldName, newName string) error
}

// atomicRenamer is the optional interface implemented by the agent module,
// used to perform tmux + DB + in-memory rename atomically under the agent
// module's lock.
type atomicRenamer interface {
	RenameSessionAtomic(oldName, newName string, doRename func() error) error
}

// renameSessionAtomic runs the complete rename flow (tmux + agent events DB
// + agent module in-memory state) atomically under the agent module's lock.
//
// Ordering: DB rename first, then tmux rename.  This ordering allows tmux
// rename failure (the most likely failure mode — e.g. tmux server unavailable)
// to trigger a best-effort DB rollback, since the DB UPDATE is trivially
// reversible.  If the initial DB rename fails, no tmux state is mutated.
//
// The agent module MUST be registered at "agent.module" and implement
// atomicRenamer — this is enforced at daemon startup, not a runtime fallback.
func (m *SessionModule) renameSessionAtomic(oldName, newName string) error {
	// Hard assert: agent module must be registered with the atomic rename API.
	// Silent fallback would mask module initialization bugs.
	svc, ok := m.core.Registry.Get("agent.module")
	if !ok {
		return errors.New("rename: agent.module not registered in service registry")
	}
	renamer, ok := svc.(atomicRenamer)
	if !ok {
		return errors.New("rename: agent.module does not implement atomicRenamer")
	}

	// Look up the optional DB renamer once, before entering the critical section.
	var dbRenamer agentEventsRenamer
	if svc, ok := m.core.Registry.Get("agent.events"); ok {
		if r, ok := svc.(agentEventsRenamer); ok {
			dbRenamer = r
		}
	}

	doRename := func() error {
		// DB first — reversible via UPDATE back to oldName.
		if dbRenamer != nil {
			if err := dbRenamer.Rename(oldName, newName); err != nil {
				return err
			}
		}
		// Tmux rename — if this fails, best-effort roll back the DB
		// so the DB + in-memory + tmux state stay consistent.
		if err := m.tmux.RenameSession(oldName, newName); err != nil {
			if dbRenamer != nil {
				_ = dbRenamer.Rename(newName, oldName)
			}
			return err
		}
		return nil
	}
	return renamer.RenameSessionAtomic(oldName, newName, doRename)
}
