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
	release              func()
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
			return control{}, &handoffError{http.StatusInternalServerError, "lease_error", "acquiring lease: " + err.Error(), nil}
		}
		row, gerr := m.getExecution(parent, execID)
		if gerr != nil {
			return control{}, &handoffError{http.StatusInternalServerError, "store_error", "re-reading execution: " + gerr.Error(), nil}
		}
		if row.LeaseID == "" || row.LeaseExpiresAt <= nowMs() {
			continue // released or expired between the two reads
		}
		if !m.isPdxPrincipal(row.LeasePrincipalID) {
			return control{}, &handoffError{http.StatusConflict, "held_by", "execution lease is held by " + row.LeasePrincipalID,
				map[string]any{"principal": row.LeasePrincipalID}}
		}
		return control{LeaseID: row.LeaseID, PrincipalID: row.LeasePrincipalID, release: noRelease}, nil
	}
	return control{}, &handoffError{http.StatusConflict, "held_by", "execution lease kept changing hands", nil}
}

func (m *Module) renewControl(parent context.Context, execID string, ctl control, principal string) (control, *handoffError) {
	ctx, cancel := detachedContext(parent, m.engineOpTimeout)
	_, err := m.sys.service.RenewLease(ctx, execID, ctl.LeaseID, ctl.PrincipalID)
	cancel()
	if err == nil {
		return ctl, nil
	}
	if !errors.Is(err, store.ErrLeaseExpired) && !errors.Is(err, store.ErrLeaseMismatch) && !errors.Is(err, store.ErrLeaseRequired) {
		return ctl, &handoffError{http.StatusInternalServerError, "lease_error", "renewing lease: " + err.Error(), nil}
	}
	ctl.release()
	return m.takeControl(parent, execID, "", principal)
}

// ctlPtr hands exitWorker the transfer's control, or nil when the transfer
// held none (an ended row): exitWorker then takes its own if it needs one.
func ctlPtr(c control) *control {
	if c.LeaseID == "" {
		return nil
	}
	return &c
}
