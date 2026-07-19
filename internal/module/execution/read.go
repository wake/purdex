package execution

import (
	"context"
	"net/http"
)

// This file exposes the M0 read path (spec §9 + Task P.12): a read-only
// projection of an execution row, consumed by the SPA execution detail page and
// the deeplink resolver. It never mutates state and never touches stdin — the
// deeplink-opened view is observe-only, so the daemon side stays strictly a
// read. Auth is the global TokenAuth middleware's job (this mux is wrapped by it
// in main).

// executionView is the JSON projection of one execution row served by
// GET /api/execution/{id}. It carries only the immutable admission facts plus
// the current status — enough for the detail page to render state, decide the
// observe-only focus fallback, and show a diff summary. session_code is nullable
// (null until launch) so the resolver can tell "has a tab handle" from "not yet
// launched"; artifacts is best-effort and present only for terminal executions.
type executionView struct {
	SchemaVersion  int            `json:"schema_version"`
	ExecutionID    string         `json:"execution_id"`
	DispatchID     string         `json:"dispatch_id"`
	Status         string         `json:"status"`
	LaunchState    string         `json:"launch_state"`
	SessionCode    *string        `json:"session_code"`
	SessionName    string         `json:"session_name"`
	HostID         string         `json:"host_id,omitempty"`
	Provider       string         `json:"provider"`
	AttemptNo      int            `json:"attempt_no"`
	RepoLocation   string         `json:"repo_location"`
	HeadAtStart    string         `json:"head_at_start"`
	DirtyAtStart   bool           `json:"dirty_at_start"`
	SandboxProfile string         `json:"sandbox_profile"`
	OutcomeSource  *string        `json:"outcome_source,omitempty"`
	Artifacts      []artifactView `json:"artifacts,omitempty"`
	CreatedAt      int64          `json:"created_at"`
	UpdatedAt      int64          `json:"updated_at"`
}

// artifactView is the pointer-first artifact projection (spec §6): an opaque
// daemon-scoped pointer plus a summary meta ({files,add,del} for a diff). The
// blob is never inlined.
type artifactView struct {
	Kind    string         `json:"kind"`
	Pointer string         `json:"pointer"`
	Meta    map[string]any `json:"meta,omitempty"`
}

// handleGetExecution serves GET /api/execution/{id}. Not found → 404; any store
// error → 500. Artifacts are computed best-effort (terminal only) and never fail
// the request.
func (m *ExecutionModule) handleGetExecution(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "missing execution id", http.StatusBadRequest)
		return
	}
	exec, ok, err := m.store.GetByID(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, "execution not found", http.StatusNotFound)
		return
	}
	writeJSON(w, m.projectExecution(r.Context(), exec))
}

// projectExecution maps a durable row onto its read-only wire view, computing a
// best-effort diff summary for terminal executions.
func (m *ExecutionModule) projectExecution(ctx context.Context, e *Execution) executionView {
	view := executionView{
		SchemaVersion:  1,
		ExecutionID:    e.ExecutionID,
		DispatchID:     e.DispatchID,
		Status:         string(e.Status),
		LaunchState:    string(e.LaunchState),
		SessionName:    e.SessionName,
		HostID:         m.daemonID(),
		Provider:       e.Provider,
		AttemptNo:      e.AttemptNo,
		RepoLocation:   e.RepoLocation,
		HeadAtStart:    e.HeadAtStart,
		DirtyAtStart:   e.DirtyAtStart,
		SandboxProfile: e.SandboxProfile,
		CreatedAt:      e.CreatedAt,
		UpdatedAt:      e.UpdatedAt,
	}
	if e.SessionCode.Valid {
		code := e.SessionCode.String
		view.SessionCode = &code
	}
	if e.OutcomeSource.Valid {
		src := e.OutcomeSource.String
		view.OutcomeSource = &src
	}
	view.Artifacts = m.artifactsFor(ctx, e)
	return view
}

// artifactsFor returns a best-effort diff summary for a terminal execution. The
// daemon does not persist artifacts (they are enqueued to Ploom at terminal
// time), so the detail page's summary is recomputed against head_at_start on
// read. Non-terminal executions, rows without a diff base or repo, and any diff
// error all yield nil — the read must never fail on the artifact best-effort.
func (m *ExecutionModule) artifactsFor(ctx context.Context, e *Execution) []artifactView {
	if !e.Status.IsTerminal() || e.HeadAtStart == "" || e.RepoLocation == "" || m.diff == nil {
		return nil
	}
	art, err := m.diff(ctx, m.daemonID(), e.ExecutionID, e.RepoLocation, e.HeadAtStart)
	if err != nil {
		return nil
	}
	return []artifactView{{Kind: art.Kind, Pointer: art.Pointer, Meta: art.Meta}}
}

// daemonID returns the daemon's host id for scoping artifact pointers, falling
// back to a stable placeholder when unset (mirrors the dispatch module helper).
func (m *ExecutionModule) daemonID() string {
	if m.hostID != "" {
		return m.hostID
	}
	if m.core == nil {
		return "daemon"
	}
	m.core.CfgMu.RLock()
	id := m.core.Cfg.HostID
	m.core.CfgMu.RUnlock()
	if id == "" {
		return "daemon"
	}
	return id
}
