package nex

// POST /api/nex/worker-rebuild (conversation entity spec D11): start a new
// worker stint for a Claude session S, optionally exiting the stint it
// replaces first. The new stint is a resume of S under a fresh execution.

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"

	"lab.protype.tw/wake/nexen/execution"
	"lab.protype.tw/wake/nexen/sandbox"
	"lab.protype.tw/wake/nexen/store"
)

// rebuildOfLabel names the execution a rebuilt stint replaced.
const rebuildOfLabel = "rebuild_of"

type workerRebuildRequest struct {
	SessionID          string `json:"session_id"`
	Cwd                string `json:"cwd"`
	Profile            string `json:"profile,omitempty"`
	ReplaceExecutionID string `json:"replace_execution_id,omitempty"`
}

// handleWorkerRebuild runs, in order: body; engine and handoff profile; cwd;
// the sid:<S> lock; with a replaced row, its exec:<id> lock, the row (404
// execution_not_found) and its ownership (409 replace_mismatch); the owner
// check (the replaced row does not count); the exit of the replaced row if
// still live (detail.step = "exit_replaced"); the delegate. Both locks are
// TryLock only and held until return (released in reverse order).
func (m *Module) handleWorkerRebuild(w http.ResponseWriter, r *http.Request) {
	var body workerRebuildRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeHandoffError(w, http.StatusBadRequest, "malformed_body", "invalid request body: "+err.Error(), nil)
		return
	}
	if body.SessionID == "" {
		writeHandoffError(w, http.StatusBadRequest, "missing_session_id", "session_id is required", nil)
		return
	}
	if err := store.ValidateResumeSessionID(body.SessionID); err != nil {
		writeHandoffError(w, http.StatusBadRequest, "invalid_session_id", "invalid session_id: "+err.Error(), nil)
		return
	}
	// Nexen lower-cases a resume id; so must every comparison here (sid lock,
	// owner checks, executionIsFor, the D17 label).
	sid := store.NormalizeResumeSessionID(body.SessionID)
	if body.Cwd == "" {
		writeHandoffError(w, http.StatusBadRequest, "missing_cwd", "cwd is required", nil)
		return
	}

	if m.sys.service == nil || m.sys.store == nil || m.opts.Config == nil {
		msg := "nex engine unavailable"
		if m.initErr != nil {
			msg = m.initErr.Error()
		}
		writeHandoffError(w, http.StatusServiceUnavailable, "nex_unavailable", msg, nil)
		return
	}
	profile := body.Profile
	if profile == "" {
		profile = handoffProfile
	}
	// The host's handoff opt-in, waived for a permission-channel profile as
	// in the handoff (handoffProfileAllowed): an asking row rebuilds on a
	// host whose max_profile is handoff_ask.
	if !hasPermissionChannel(profile) && !slices.Contains(sandbox.UsableProfiles(m.opts.Config.Sandbox), handoffProfile) {
		writeHandoffError(w, http.StatusConflict, "handoff_unsupported",
			"host sandbox policy does not allow the handoff profile", nil)
		return
	}
	if !slices.Contains(sandbox.UsableProfiles(m.opts.Config.Sandbox), profile) {
		writeHandoffError(w, http.StatusBadRequest, "invalid_profile", "sandbox profile is not usable under the host policy",
			map[string]any{"profile": profile})
		return
	}
	principal, err := m.principal(r)
	if err != nil {
		writeHandoffError(w, http.StatusInternalServerError, "principal_unresolved", err.Error(), nil)
		return
	}

	if err := m.sessions.ValidateCwd(body.Cwd); err != nil {
		writeHandoffError(w, http.StatusConflict, "cwd_missing", "cwd is not usable: "+err.Error(),
			map[string]any{"cwd": body.Cwd})
		return
	}

	// The conversation's lock first, then the replaced row's. Take-to-terminal
	// and take-back go exec -> sid; the order differs but every lock is a
	// TryLock, so concurrent ones cannot deadlock, they answer 409.
	sidKey := sidLockKey(sid)
	if !m.locks.TryLock(sidKey) {
		writeHandoffError(w, http.StatusConflict, "transfer_in_progress", "this conversation is being moved already",
			map[string]any{"session_id": sid})
		return
	}
	defer m.locks.Unlock(sidKey)

	parent := r.Context()
	rid := body.ReplaceExecutionID
	var replaced store.Execution
	if rid != "" {
		repKey := takeToTerminalLockKey(rid)
		if !m.locks.TryLock(repKey) {
			writeHandoffError(w, http.StatusConflict, "transfer_in_progress", "the replaced execution is being moved already",
				map[string]any{"execution_id": rid})
			return
		}
		defer m.locks.Unlock(repKey)

		replaced, err = m.getExecution(parent, rid)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeHandoffError(w, http.StatusNotFound, "execution_not_found", "execution not found", nil)
				return
			}
			writeHandoffError(w, http.StatusInternalServerError, "store_error", "reading execution: "+err.Error(), nil)
			return
		}
		if !executionIsFor(replaced, sid) {
			writeHandoffError(w, http.StatusConflict, "replace_mismatch", "the replaced execution belongs to another conversation",
				map[string]any{"execution_id": rid, "session_id": sid})
			return
		}
	}

	// The replaced row is the one being swapped out: it does not count as S's owner.
	if herr := m.checkOwners(parent, sid, rid, ""); herr != nil {
		herr.write(w)
		return
	}

	exited := false
	if rid != "" && isLiveExecution(replaced) {
		// An exit, not a transfer (D22 does not apply): it borrows a pdx
		// holder's lease, and a non-pdx holder answers held_by (D4).
		if _, herr := m.exitWorker(parent, replaced, nil, m.internalPrincipal()); herr != nil {
			if herr.detail == nil {
				herr.detail = map[string]any{}
			}
			herr.detail["step"] = "exit_replaced"
			herr.write(w)
			return
		}
		exited = true
		// Look at the owners once more right before the delegate (as
		// take-to-terminal's step 10b): the exit is a window in which a
		// terminal can resume S.
		if herr := m.checkOwners(parent, sid, "", ""); herr != nil {
			if herr.detail == nil {
				herr.detail = map[string]any{}
			}
			herr.detail["replaced_exited"] = true
			herr.write(w)
			return
		}
	}

	labels := map[string]string{"source": "purdex", purdexSessionLabel: sid}
	if rid != "" {
		labels[rebuildOfLabel] = rid
	}
	req := execution.Request{
		PrincipalID:     principal,
		Provider:        "claude",
		Brief:           "",
		StartIdle:       true, // idle row, no turn (spec §8)
		SandboxProfile:  profile,
		Mounts:          []execution.Mount{{Path: body.Cwd, Role: "cwd", Writable: true}},
		Origin:          "purdex://host/" + m.opts.Config.HostID + "/rebuild",
		Labels:          labels,
		ResumeSessionID: sid,
	}
	ctx, cancel := detachedContext(parent, m.delegateTimeout)
	result, err := m.sys.service.Delegate(ctx, req)
	cancel()
	if err != nil {
		if exited {
			// Accepted: the old stint stays exited; a retry has nothing to replace.
			m.logf("nex: worker-rebuild %s: delegate failed after exiting %s (it stays exited): %v", sid, rid, err)
		}
		writeHandoffError(w, http.StatusInternalServerError, "delegate_failed", "starting the worker: "+err.Error(),
			map[string]any{"session_id": sid, "replaced_exited": exited})
		return
	}

	m.logf("nex: worker-rebuild %s → execution %s (%s, profile=%s, replaced=%q)", sid, result.ID, result.State, result.EffectiveProfile, rid)
	resp := map[string]any{
		"execution_id":      result.ID,
		"state":             string(result.State),
		"effective_profile": result.EffectiveProfile,
	}
	if result.RejectReason != "" {
		resp["reject_reason"] = result.RejectReason
	}
	writeJSON(w, http.StatusOK, resp)
}
