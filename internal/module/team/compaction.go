package teammod

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/wake/purdex/internal/module/hostconfig"
	"github.com/wake/purdex/internal/team"
)

// compactionLogEvery is how often the same unreadable-row error is logged again.
const compactionLogEvery = 10 * time.Minute

// compactionDefaults reads the host's compaction row; one that cannot be read is today's behaviour (DefaultCompaction).
func (m *Module) compactionDefaults() hostconfig.CompactionDefaults {
	d, err := m.compaction.Compaction()
	if err == nil {
		return d
	}
	msg, now := err.Error(), time.Now()
	m.compactionLog.mu.Lock()
	quiet := msg == m.compactionLog.msg && now.Sub(m.compactionLog.at) < compactionLogEvery
	if !quiet {
		m.compactionLog.msg, m.compactionLog.at = msg, now
	}
	m.compactionLog.mu.Unlock()
	if !quiet {
		m.logf("[team] compaction defaults unreadable, using the built-in ones: %v", err)
	}
	return hostconfig.DefaultCompaction
}

// compactionView is a session's override and the settings that apply: override ?? host default of its role. Self relay
// on, or a member while member_auto is on, means relay owns compaction (managed false); the values are filled anyway.
func (m *Module) compactionView(sessionID string) (team.CompactionView, error) {
	role, state, _, err := m.selfRelayState(sessionID)
	if err != nil {
		return team.CompactionView{}, err
	}
	o, err := m.store.CompactionOverride(sessionID)
	if err != nil {
		return team.CompactionView{}, err
	}
	d := m.compactionDefaults()
	set := d.Team
	if role == roleNone {
		set = d.Solo
	}
	e := team.CompactionEffective{Managed: true, Auto: set.Auto, Threshold: set.Threshold, Idle: set.Idle}
	if o.Auto != nil {
		e.Auto = *o.Auto
	}
	if o.Threshold != nil {
		e.Threshold = *o.Threshold
	}
	if o.Idle != nil {
		e.Idle = *o.Idle
	}
	switch {
	case role == roleMember && m.memberAutoOn():
		e.Managed, e.Reason = false, team.CompactionReasonMemberAuto
	case state == "on":
		e.Managed, e.Reason = false, team.CompactionReasonSelfRelay
	}
	return team.CompactionView{Role: role, Override: o, Effective: e}, nil
}

// checkCompactionOverride: a threshold, when set, is an integer 1-100 (the type already keeps it an integer).
func checkCompactionOverride(o team.CompactionOverride) error {
	if o.Threshold != nil && (*o.Threshold < 1 || *o.Threshold > 100) {
		return errors.New("threshold must be an integer from 1 to 100")
	}
	return nil
}

// isJSONObject: the top-level value is an object (not null, an array, a scalar or empty).
func isJSONObject(b []byte) bool {
	t, err := json.NewDecoder(bytes.NewReader(b)).Token()
	return err == nil && t == json.Delim('{')
}

// duplicateTopKey finds a repeated key of the top-level object; the values are all scalars here, so a flat walk is enough.
func duplicateTopKey(b []byte) (string, bool) {
	dec := json.NewDecoder(bytes.NewReader(b))
	_, _ = dec.Token()
	seen := map[string]bool{}
	for dec.More() {
		k, err := dec.Token()
		if err != nil {
			return "", false
		}
		key, _ := k.(string)
		if seen[key] {
			return key, true
		}
		seen[key] = true
		var skip json.RawMessage
		if dec.Decode(&skip) != nil {
			return "", false
		}
	}
	return "", false
}

func (m *Module) writeCompactionView(w http.ResponseWriter, sid string) {
	v, err := m.compactionView(sid)
	if err != nil {
		m.logf("[team] compaction %s: %v", sid, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "role, switches or team.db unreadable; see the daemon log", nil)
		return
	}
	m.writeJSON(w, http.StatusOK, v)
}

// handleCompactionGet is GET /api/team/compaction/{session_id}, the App's.
func (m *Module) handleCompactionGet(w http.ResponseWriter, r *http.Request) {
	m.writeCompactionView(w, r.PathValue("session_id"))
}

// handleCompactionPut is PUT /api/team/compaction/{session_id}: the whole override is replaced; a missing key is null.
func (m *Module) handleCompactionPut(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, bodyCap+1))
	var fields map[string]json.RawMessage
	if err != nil || len(body) > bodyCap || !isJSONObject(body) || json.Unmarshal(body, &fields) != nil {
		m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, "body must be a JSON object", nil)
		return
	}
	if dup, ok := duplicateTopKey(body); ok {
		m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, "duplicate field "+dup, nil)
		return
	}
	var o team.CompactionOverride
	targets := map[string]any{"auto": &o.Auto, "threshold": &o.Threshold, "idle": &o.Idle}
	for k, raw := range fields {
		t, known := targets[k]
		if !known {
			m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, "unknown field "+k, nil)
			return
		}
		if err := json.Unmarshal(raw, t); err != nil {
			m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, k+" has the wrong type", nil)
			return
		}
	}
	if err := checkCompactionOverride(o); err != nil {
		m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, err.Error(), nil)
		return
	}
	sid := r.PathValue("session_id")
	if err := m.store.SetCompactionOverride(sid, o, m.now()); err != nil {
		m.logf("[team] compaction PUT %s: %v", sid, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return
	}
	m.writeCompactionView(w, sid)
}
