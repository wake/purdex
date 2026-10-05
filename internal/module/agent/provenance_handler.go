package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// provenanceTimeout bounds one provenance request, checked between process
// reads (it cannot interrupt one — readProcessInfoPlatform has no context).
// It is a var only so tests can expire it; production never changes it.
var provenanceTimeout = 5 * time.Second

// provenanceResponse is the wire shape of GET /api/sessions/{code}/provenance.
//
// TmuxInstance is deliberately NOT omitempty, for the same reason as the cwd
// handler's: "" is a transmitted value meaning "the generation is unknown", and
// the caller must be able to tell it apart from a daemon that never sends the
// field. Everything else is omitted when there is no answer, so found:false is
// the two-field object the spec shows.
type provenanceResponse struct {
	Found          bool   `json:"found"`
	AgentType      string `json:"agent_type,omitempty"`
	SessionID      string `json:"session_id,omitempty"`
	Cwd            string `json:"cwd,omitempty"`
	TranscriptPath string `json:"transcript_path,omitempty"`
	TmuxPaneID     string `json:"tmux_pane_id,omitempty"`
	TmuxInstance   string `json:"tmux_instance"`
	LastSeenAt     int64  `json:"last_seen_at,omitempty"`
	// StartedAt is the answering frame's start, in the same unit as LastSeenAt.
	// Unlike LastSeenAt — which moves on every hook event — it is fixed for the
	// life of the run, so every SPA client that backfills the same run stamps
	// its record with the same time and their synced payloads hash alike
	// (sync-conflict-fixes spec D1).
	StartedAt int64 `json:"started_at,omitempty"`
	// FrameID names the answering run, so a backfill that confirms a live
	// agent can adopt it and a later exit envelope can match it.
	FrameID string `json:"frame_id,omitempty"`
}

// handleSessionProvenance answers which agent owns the tmux session behind
// {code}: the root agent frame of one of its panes, with the session id and cwd
// that agent reported for itself, so the SPA can compose a resume command
// (spec §5.3).
//
// The generation is sampled on BOTH sides of the frame work and reported only
// when the two samples agree, exactly as the cwd handler does. A tmux server
// that restarted mid-request would otherwise hand back the new server's agent
// stamped with the old server's generation, and the probe — which can only
// compare that stamp against the binding it asked with — would accept it.
// Disagreement reports "", and "" authorises nothing on the SPA side.
//
// An unknown session code is answered with found:false and a 200, not a 404:
// the SPA treats "no answer" uniformly and a code that just died is a normal
// race, not a client error.
func (m *Module) handleSessionProvenance(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")

	instance := m.tmuxInstance()
	owner, found := m.resolveSessionOwner(r.Context(), code)
	if after := m.tmuxInstance(); after != instance {
		instance = ""
	}

	resp := provenanceResponse{TmuxInstance: instance}
	if found {
		resp.Found = true
		resp.AgentType = owner.AgentType
		resp.SessionID = owner.SessionID
		resp.Cwd = owner.Cwd
		resp.TranscriptPath = owner.TranscriptPath
		resp.TmuxPaneID = owner.TmuxPaneID
		resp.LastSeenAt = owner.LastSeenAt
		resp.StartedAt = owner.StartedAt
		resp.FrameID = owner.FrameID
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// tmuxInstance samples the tmux server generation, or "" when there is nobody
// to ask. "" means unknown and is never treated as a match.
func (m *Module) tmuxInstance() string {
	if m.sessions == nil {
		return ""
	}
	return m.sessions.TmuxInstance()
}

// resolveSessionOwner picks the one root agent frame that answers for a tmux
// session, or reports that there is none. It is a thin wrapper over
// resolveSessionOwnerErr that drops the error, for callers (handleSessionProvenance
// and its tests) that only ever treated "no answer" as one outcome regardless
// of cause.
func (m *Module) resolveSessionOwner(ctx context.Context, code string) (PaneOwner, bool) {
	owner, found, _ := m.resolveSessionOwnerErr(ctx, code)
	return owner, found
}

// resolveSessionOwnerErr is resolveSessionOwner's error-preserving form: it
// tells a genuine "no root agent frame answers for this session"
// (found=false, err=nil) apart from "the walk could not be completed"
// (err != nil — a pane listing that failed, a frames-store or process-view
// failure, or a context that expired or was cancelled mid-walk). The peers
// module (Item 1, #988) needs that distinction: reporting a lookup failure the
// same way as "no agent" would tell the SPA a live session has none.
//
// It is an owner pass over one session (owner_pass.go): one process snapshot
// of its own, the listing that finds the session's panes, and the listing
// that re-checks them, all under ONE deadline that bounds the whole request.
// The answer is the pass's, rules and all: the membership re-check against a
// `join-pane` mid-walk, the clock read after it, and "a partial walk is not
// an answer".
func (m *Module) resolveSessionOwnerErr(ctx context.Context, code string) (PaneOwner, bool, error) {
	if m.frames == nil || m.tmux == nil || code == "" {
		return PaneOwner{}, false, nil
	}
	ctx, cancel := context.WithTimeout(ctx, provenanceTimeout)
	defer cancel()

	pass := m.NewOwnerPass(nil)
	pass.Resolve(ctx, code)
	res := pass.Confirm(ctx)[code]
	if res.Err != nil {
		return PaneOwner{}, false, res.Err
	}
	return res.Owner, res.Found, nil
}

// betterOwner is the multi-root tie-break: most recently seen wins, and equal
// last_seen_at is broken by ASCENDING frame id so the answer is deterministic
// and testable rather than dependent on store ordering.
func betterOwner(candidate, incumbent PaneOwner) bool {
	if candidate.LastSeenAt != incumbent.LastSeenAt {
		return candidate.LastSeenAt > incumbent.LastSeenAt
	}
	return candidate.FrameID < incumbent.FrameID
}
