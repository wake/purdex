// internal/module/nex/control.go
package nex

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"lab.protype.tw/wake/nexen/store"
)

// The lease an orchestration acts under (conversation entity D4/D5).
// Nexen fences every write — send, interrupt, terminate — behind one
// control lease per execution, and an open worker pane keeps renewing its
// own. The daemon mints every pdx principal, so it may act under a pdx
// holder's lease; it never overrides anyone else's.

type control struct {
	LeaseID, PrincipalID string
	release              func() // never nil: noRelease when there is nothing to release, also on error returns
}

func noRelease() {}

var nowMs = func() int64 { return time.Now().UnixMilli() }

func (m *Module) isPdxPrincipal(p string) bool {
	base := "pdx:" + m.opts.Config.HostID
	return p == base || strings.HasPrefix(p, base+"/")
}

func (m *Module) takeControl(parent context.Context, execID, callerLease, principal string) (control, *handoffError) {
	if callerLease != "" {
		return control{LeaseID: callerLease, PrincipalID: principal, release: noRelease}, nil
	}
	lastHolder := ""
	for attempt := 0; attempt < 2; attempt++ {
		lease, err := m.acquireLease(parent, execID, principal)
		if err == nil {
			id := lease.ID
			return control{LeaseID: id, PrincipalID: principal, release: func() {
				if err := m.releaseLease(parent, execID, id, principal); err != nil {
					m.logf("nex: releasing lease %s on %s: %v", id, execID, err)
				}
			}}, nil
		}
		if !errors.Is(err, store.ErrLeaseHeld) {
			return control{release: noRelease}, &handoffError{http.StatusInternalServerError, "lease_error", "acquiring lease: " + err.Error(), nil}
		}
		row, gerr := m.getExecution(parent, execID)
		if gerr != nil {
			return control{release: noRelease}, &handoffError{http.StatusInternalServerError, "store_error", "re-reading execution: " + gerr.Error(), nil}
		}
		if row.LeaseID != "" {
			lastHolder = row.LeasePrincipalID
		}
		if row.LeaseID == "" || row.LeaseExpiresAt <= nowMs() {
			continue // released or expired between the two reads
		}
		if !m.isPdxPrincipal(row.LeasePrincipalID) {
			return control{release: noRelease}, heldByError(row.LeasePrincipalID)
		}
		return control{LeaseID: row.LeaseID, PrincipalID: row.LeasePrincipalID, release: noRelease}, nil
	}
	var detail map[string]any
	if lastHolder != "" {
		detail = map[string]any{"principal": lastHolder}
	}
	return control{release: noRelease}, &handoffError{http.StatusConflict, "lease_contended", "execution lease kept changing hands", detail}
}

func (m *Module) renewControl(parent context.Context, execID string, ctl control, principal string) (control, *handoffError) {
	ctx, cancel := detachedContext(parent, m.engineOpTimeout)
	_, err := m.sys.service.RenewLease(ctx, execID, ctl.LeaseID, ctl.PrincipalID)
	cancel()
	if err == nil {
		return ctl, nil
	}
	if !isLeaseErr(err) {
		return ctl, &handoffError{http.StatusInternalServerError, "lease_error", "renewing lease: " + err.Error(), nil}
	}
	ctl.release()
	return m.takeControl(parent, execID, "", principal)
}

// isLeaseErr reports Nexen's CheckLease refusals: the control a caller acts
// under is gone (released, expired, or now someone else's).
func isLeaseErr(err error) bool {
	return errors.Is(err, store.ErrLeaseRequired) || errors.Is(err, store.ErrLeaseExpired) || errors.Is(err, store.ErrLeaseMismatch)
}

// heldByError is D4's refusal: a non-pdx principal holds the lease.
func heldByError(principal string) *handoffError {
	return &handoffError{http.StatusConflict, "held_by", "execution lease is held by " + principal,
		map[string]any{"principal": principal}}
}

// heldByOther re-reads the row and reports a live lease held by a non-pdx
// principal (D4: nothing of theirs is changed). ok is false when the
// re-read itself failed, so the caller cannot tell and must not act.
func (m *Module) heldByOther(parent context.Context, execID string) (herr *handoffError, ok bool) {
	row, err := m.getExecution(parent, execID)
	if err != nil {
		m.logf("nex: re-reading %s for its lease holder: %v", execID, err)
		return nil, false
	}
	if row.LeaseID != "" && row.LeaseExpiresAt > nowMs() && !m.isPdxPrincipal(row.LeasePrincipalID) {
		return heldByError(row.LeasePrincipalID), true
	}
	return nil, true
}

// ctlPtr hands exitWorker the transfer's control, or nil when the transfer
// held none (an ended row): exitWorker then takes its own if it needs one.
func ctlPtr(c control) *control {
	if c.LeaseID == "" {
		return nil
	}
	return &c
}
