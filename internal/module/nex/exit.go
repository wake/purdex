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
	// D23: the lease (and its principal) the archive runs under, so Nexen
	// (≥ v0.18.0, #113) checks the lease in the archive's own UPDATE: the
	// control a successful terminate ran under, or that answered
	// ErrExecutionTerminal; for a row that needs no terminate, the control
	// taken for the archive (#1665). Empty — no fence — only on D4's
	// "terminate failed, archive anyway" path.
	var fenceLease, fencePrincipal string
	own := control{release: noRelease} // a control exitWorker took itself; released on return, after the archive
	defer func() { own.release() }()
	if needsTerminate(exec.State) {
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
			leaseID, leasePrincipal := c.LeaseID, c.PrincipalID // the control the terminate runs under
			err := m.terminateExecution(parent, execution.TerminateRequest{ExecutionID: exec.ID, LeaseID: leaseID, PrincipalID: leasePrincipal})
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
					leaseID, leasePrincipal = own.LeaseID, own.PrincipalID
					err = m.terminateExecution(parent, execution.TerminateRequest{ExecutionID: exec.ID, LeaseID: leaseID, PrincipalID: leasePrincipal})
				case herr.code == "held_by":
					return out, herr
				default:
					m.logf("nex: exit %s: re-taking control: %s", exec.ID, herr.msg) // err stays the terminate's
				}
			}
			switch {
			case err == nil:
				out.Terminated, out.State = true, store.StateTerminated
				fenceLease, fencePrincipal = leaseID, leasePrincipal
			case errors.Is(err, store.ErrExecutionTerminal):
				// The row ended on its own; the control is still ours to act under.
				fenceLease, fencePrincipal = leaseID, leasePrincipal
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
	} else if !out.Archived {
		// #1665: a row that needs no terminate — terminated but unarchived
		// (D16's retry), failed, rejected — is archived under a control too:
		// the caller's, else one taken as for a terminate. A non-pdx holder
		// is never overridden (D4: held_by, nothing changes), and any other
		// failure to take control archives nothing.
		c := ctl
		if c == nil {
			got, herr := m.takeControl(parent, exec.ID, "", principal)
			if herr != nil {
				m.logf("nex: exit %s: no control for the archive (%s); not archiving", exec.ID, herr.msg)
				return out, herr
			}
			// A borrowed lease may be near its end: renew it before the
			// archive, or — gone, expired, someone else's — take control again
			// (a non-pdx holder refused, D4). renewControl hands back the
			// control to release: an own lease (the one taken, or the re-take's)
			// carries its release, a borrowed one noRelease. A transfer's
			// control (ctl) is the transfer's to renew.
			renewed, herr := m.renewControl(parent, exec.ID, got, principal)
			own = renewed
			if herr != nil {
				m.logf("nex: exit %s: renewing control for the archive (%s); not archiving", exec.ID, herr.msg)
				return out, herr
			}
			c = &own
		}
		fenceLease, fencePrincipal = c.LeaseID, c.PrincipalID
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
		req := execution.ArchiveRequest{ExecutionID: exec.ID, PrincipalID: principal, Archived: true}
		if fenceLease != "" {
			// D23: archive under the control's lease — a lease that changed
			// hands since is refused (lease_* error) and nothing is written.
			req.LeaseID, req.PrincipalID = fenceLease, fencePrincipal
		}
		err := m.archiveExecution(parent, req)
		switch {
		case err == nil:
			out.Archived = true
		case out.Terminated:
			// Includes a fenced archive refused with a lease error (D23):
			// terminated, not archived — the next exit retries the archive (D16).
			m.logf("nex: exit %s: archive after terminate: %v (exited; retried on the next exit)", exec.ID, err)
		case termErr != nil:
			// Typically archive_while_running: the turn the terminate could not
			// stop is still running. The terminate's reason is the useful one.
			m.logf("nex: exit %s: archive after a failed terminate: %v", exec.ID, err)
			return out, termErr
		case isLeaseErr(err):
			// D23 on a row that is not terminated (failed, rejected — also one
			// that ended so on its own): the lease changed hands between the
			// control and the archive, and the fence wrote nothing. A race, not
			// a fault (#1665): 409 — the holder's held_by when the re-read finds
			// a live non-pdx one (D4), else lease_contended, to try again.
			m.logf("nex: exit %s: fenced archive refused: %v (not archived)", exec.ID, err)
			if held, ok := m.heldByOther(parent, exec.ID); ok && held != nil {
				return out, held
			}
			return out, &handoffError{http.StatusConflict, "lease_contended", "the lease changed hands during the exit; try again",
				map[string]any{"execution_id": exec.ID}}
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
	// stale or not the caller's. A row that needs no terminate ignores it:
	// exitWorker takes control for that archive itself (#1665).
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
