package nex

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"lab.protype.tw/wake/nexen/execution"
	"lab.protype.tw/wake/nexen/store"
)

// Exit — the one worker action (conversation entity spec §5, D4, D16).

func needsTerminate(s store.State) bool {
	return s == store.StateQueued || s == store.StateRunning || s == store.StateIdle
}

func (m *Module) exitWorker(parent context.Context, exec store.Execution, ctl *control, principal string) (exitOutcome, *handoffError) {
	out := exitOutcome{Terminated: exec.State == store.StateTerminated, Archived: exec.ArchivedAt != 0, State: exec.State}
	var termErr *handoffError // a failed terminate; reported only if the archive cannot stand in for it
	if needsTerminate(exec.State) {
		own := control{release: noRelease} // a control exitWorker took itself; released on return
		defer func() { own.release() }()
		c := ctl
		if c == nil {
			got, herr := m.takeControl(parent, exec.ID, "", principal)
			if herr != nil {
				if herr.code == "held_by" {
					return out, herr // D4: a non-pdx holder is never overridden — nothing changes
				}
				termErr = herr
				m.logf("nex: exit %s: no control (%s); archiving only", exec.ID, herr.msg)
			} else {
				own, c = got, &own
			}
		}
		if c != nil {
			err := m.terminateExecution(parent, execution.TerminateRequest{ExecutionID: exec.ID, LeaseID: c.LeaseID, PrincipalID: c.PrincipalID})
			if isLeaseErr(err) && ctl == nil {
				// The lease changed hands between takeControl's read and the
				// terminate. Take control once more, as of now: a non-pdx
				// holder is refused here like on the first take (D4). A
				// transfer's control (ctl != nil) is the transfer's to keep.
				m.logf("nex: exit %s: lease lost before terminate (%v); re-taking control once", exec.ID, err)
				own.release()
				own = control{release: noRelease}
				got, herr := m.takeControl(parent, exec.ID, "", principal)
				switch {
				case herr == nil:
					own = got
					err = m.terminateExecution(parent, execution.TerminateRequest{ExecutionID: exec.ID, LeaseID: own.LeaseID, PrincipalID: own.PrincipalID})
				case herr.code == "held_by":
					return out, herr
				default:
					m.logf("nex: exit %s: re-taking control: %s", exec.ID, herr.msg) // err stays the terminate's
				}
			}
			switch {
			case err == nil:
				out.Terminated, out.State = true, store.StateTerminated
			case errors.Is(err, store.ErrExecutionTerminal):
				if fresh, gerr := m.getExecution(parent, exec.ID); gerr == nil {
					out.State = fresh.State
					out.Terminated = fresh.State == store.StateTerminated
					out.Archived = out.Archived || fresh.ArchivedAt != 0
				}
			default:
				termErr = terminateError(err)
				if out.Archived {
					m.logf("nex: exit %s: terminate failed on an already archived row: %v (still exited — archived)", exec.ID, err)
				} else {
					m.logf("nex: exit %s: terminate: %v; archiving anyway (D4)", exec.ID, err)
				}
			}
		}
	}
	if !out.Archived {
		if termErr != nil {
			// D4: the terminate did not happen, possibly because the lease
			// changed hands meanwhile. Never archive a non-pdx holder's
			// execution, and never archive blind when the holder is unknown.
			held, ok := m.heldByOther(parent, exec.ID)
			if !ok {
				m.logf("nex: exit %s: lease holder unknown after a failed terminate; not archiving", exec.ID)
				return out, termErr
			}
			if held != nil {
				m.logf("nex: exit %s: terminate failed and %s now holds the lease; not archiving (D4)", exec.ID, held.detail["principal"])
				return out, held
			}
		}
		err := m.archiveExecution(parent, execution.ArchiveRequest{ExecutionID: exec.ID, PrincipalID: principal, Archived: true})
		switch {
		case err == nil:
			out.Archived = true
		case out.Terminated:
			m.logf("nex: exit %s: archive after terminate: %v (exited; retried on the next exit)", exec.ID, err)
		case termErr != nil:
			// Typically archive_while_running: the turn the terminate could not
			// stop is still running. The terminate's reason is the useful one.
			m.logf("nex: exit %s: archive after a failed terminate: %v", exec.ID, err)
			return out, termErr
		default:
			return out, &handoffError{http.StatusInternalServerError, "archive_failed", "archiving execution: " + err.Error(),
				map[string]any{"execution_id": exec.ID}}
		}
	}
	return out, nil
}

func terminateError(err error) *handoffError {
	switch {
	case errors.Is(err, execution.ErrInterruptUnconfirmed):
		return &handoffError{http.StatusGatewayTimeout, "interrupt_unconfirmed", "interrupt not confirmed: " + err.Error(), nil}
	case errors.Is(err, execution.ErrTerminateContended):
		return &handoffError{http.StatusConflict, "terminate_contended", err.Error(), nil}
	default:
		return &handoffError{http.StatusInternalServerError, "terminate_failed", "terminating execution: " + err.Error(), nil}
	}
}

type exitOutcome struct {
	Terminated, Archived bool
	State                store.State
}

// Exited reports whether the worker is gone: terminated, or archived (writes blocked).
func (o exitOutcome) Exited() bool { return o.Terminated || o.Archived }

type exitRequest struct {
	LeaseID string `json:"lease_id,omitempty"`
}

// handleExitWorker: POST /api/nex/executions/{id}/exit (spec §5, D4).
func (m *Module) handleExitWorker(w http.ResponseWriter, r *http.Request) {
	execID := r.PathValue("id")
	if m.sys.service == nil || m.sys.store == nil || m.opts.Config == nil {
		msg := "nex engine unavailable"
		if m.initErr != nil {
			msg = m.initErr.Error()
		}
		writeHandoffError(w, http.StatusServiceUnavailable, "nex_unavailable", msg, nil)
		return
	}
	principal, err := m.principal(r)
	if err != nil {
		writeHandoffError(w, http.StatusInternalServerError, "principal_unresolved", err.Error(), nil)
		return
	}
	var body exitRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		writeHandoffError(w, http.StatusBadRequest, "malformed_body", "invalid request body: "+err.Error(), nil)
		return
	}
	lockKey := takeToTerminalLockKey(execID)
	if !m.locks.TryLock(lockKey) {
		writeHandoffError(w, http.StatusConflict, "transfer_in_progress", "this worker is being moved or exited already", nil)
		return
	}
	defer m.locks.Unlock(lockKey)

	exec, err := m.getExecution(r.Context(), execID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeHandoffError(w, http.StatusNotFound, "execution_not_found", "execution not found", nil)
			return
		}
		writeHandoffError(w, http.StatusInternalServerError, "store_error", "reading execution: "+err.Error(), nil)
		return
	}
	// A caller's lease is validated before it is acted under: renewControl
	// renews it, and re-takes control (refusing a non-pdx holder) when it is
	// stale or not the caller's. A row that needs no terminate ignores it.
	var ctl *control
	if body.LeaseID != "" && needsTerminate(exec.State) {
		c, herr := m.renewControl(r.Context(), execID, control{LeaseID: body.LeaseID, PrincipalID: principal, release: noRelease}, principal)
		defer c.release() // an own lease the re-take acquired; noRelease otherwise
		if herr != nil {
			herr.write(w)
			return
		}
		ctl = &c
	}
	out, herr := m.exitWorker(r.Context(), exec, ctl, principal)
	if herr != nil {
		herr.write(w)
		return
	}
	m.logf("nex: exit %s -> terminated=%v archived=%v (%s)", execID, out.Terminated, out.Archived, out.State)
	writeJSON(w, http.StatusOK, map[string]any{
		"exited": out.Exited(), "terminated": out.Terminated, "archived": out.Archived, "state": string(out.State),
	})
}
