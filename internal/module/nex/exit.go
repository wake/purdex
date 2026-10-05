// internal/module/nex/exit.go
package nex

import (
	"context"
	"errors"
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
		c := ctl
		if c == nil {
			own, herr := m.takeControl(parent, exec.ID, "", principal)
			if herr != nil {
				if herr.code == "held_by" {
					return out, herr // D4: a non-pdx holder is never overridden — nothing changes
				}
				termErr = herr
				m.logf("nex: exit %s: no control (%s); archiving only", exec.ID, herr.msg)
			} else {
				defer own.release()
				c = &own
			}
		}
		if c != nil {
			err := m.terminateExecution(parent, execution.TerminateRequest{ExecutionID: exec.ID, LeaseID: c.LeaseID, PrincipalID: c.PrincipalID})
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
