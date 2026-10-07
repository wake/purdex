package nex

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"lab.protype.tw/wake/nexen/store"
)

// The lease an orchestration acts under (conversation entity D4/D5/D22).
// Nexen fences every write — send, permission answer, interrupt, terminate
// — behind one control lease per execution, and an open worker pane keeps
// renewing its own. The daemon mints every pdx principal, so it may act on
// a pdx holder's lease — preempt it (every ending path, D22 and ruling
// R-PC-1), or borrow it (exit's fall back when the preempt stays
// contended or runs out of its time budget, D4); it never overrides
// anyone else's.

// controlMode is what takeControlMode does with a live lease another pdx
// client holds.
type controlMode int

const (
	borrowPdx      controlMode = iota // act under a pdx holder's current lease (only as exitPreemptPdx's fall back)
	preemptPdx                        // transfers (D22): release the pdx holder's lease, acquire our own exclusive one
	exitPreemptPdx                    // exit, Q1, worker-rebuild (D22, R-PC-1): preempt within one budget; when that stays contended, runs out of time or the release fails, borrow (D4)
)

// How many times takeControlMode tries before it gives up on a lease that
// keeps changing hands: a transfer then answers lease_contended; an exit
// preempts for one pass more (all inside exitPreemptBudget), then falls
// back to borrowing, so it never fails merely because another pdx tab
// holds control (D4).
const (
	controlPasses     = 2
	exitPreemptPasses = controlPasses + 1
)

// exitPreemptBudget bounds exit's whole preempt phase (D4, D22): all its
// passes — the acquire, the re-read, the release of the holder's lease and
// our re-acquire inside each — share this one budget, measured from the
// phase's start, each call capped at what is left of it. Once it is spent
// the exit stops preempting and borrows, under the borrow's own timeouts,
// so per-call timeouts no longer stack up (three passes of 10 + 10 + 5 +
// 10 s) while the exec lock is held. A var so tests can shrink it.
var exitPreemptBudget = 20 * time.Second

// errPreemptBudget: the preempt budget was spent before a call of the phase
// or while it ran (the call may have been cut at the budget's end).
var errPreemptBudget = errors.New("preempt budget spent")

// preemptBudget is the deadline of exit's preempt phase. noBudget (the zero
// value) is no deadline: transfers, the borrow, every other caller.
type preemptBudget struct{ deadline time.Time }

var noBudget preemptBudget

func newPreemptBudget(d time.Duration) preemptBudget {
	return preemptBudget{deadline: time.Now().Add(d)}
}

func (b preemptBudget) spent() bool {
	return !b.deadline.IsZero() && !time.Now().Before(b.deadline)
}

// run makes one engine call under a context detached from parent and
// bounded by d — or, inside a budget, by what is left of it when that is
// less. A call is not made once the budget is spent, and an error it ends
// with once the budget is spent is the budget's (it may have been cut at
// the budget's end): errPreemptBudget either way, the cause kept in the
// message. A call that succeeds stands, spent or not.
func (b preemptBudget) run(parent context.Context, d time.Duration, call func(context.Context) error) error {
	if b.spent() {
		return errPreemptBudget
	}
	if !b.deadline.IsZero() {
		d = min(d, time.Until(b.deadline))
	}
	ctx, cancel := detachedContext(parent, d)
	defer cancel()
	err := call(ctx)
	if err != nil && b.spent() {
		return fmt.Errorf("%w: %v", errPreemptBudget, err)
	}
	return err
}

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

// takeControl is takeControlMode in exit's mode (exitPreemptPdx): the one
// every exit — and so Q1 and worker-rebuild with a replaced row — takes
// control with when it has no caller's or transfer's lease to act under.
func (m *Module) takeControl(parent context.Context, execID, callerLease, principal string) (control, *handoffError) {
	return m.takeControlMode(parent, execID, callerLease, principal, exitPreemptPdx)
}

// takeControlMode returns the control to act under: the caller's lease as
// is, else one acquired under principal, else — when a live lease is held —
// held_by for a non-pdx holder, and for a pdx holder per mode: its lease
// borrowed (borrowPdx), or released as the holder and replaced by an
// exclusive one of ours (preemptPdx, exitPreemptPdx; a preempted lease is
// an acquired one). An acquired lease carries a real release; a caller's or
// a borrowed one carries noRelease. A lease that changes hands under it is
// tried again, controlPasses passes in all, then lease_contended — except
// in exitPreemptPdx, which preempts for exitPreemptPasses passes and then
// falls back to borrowPdx (logged): an exit never fails merely because
// another pdx tab holds control (D4). For the same reason a release of the
// holder's lease that fails outright (not a lease-class refusal) is
// lease_error in preemptPdx but, in exitPreemptPdx, borrows that still-held
// lease (logged). exitPreemptPdx's passes also share one time budget
// (exitPreemptBudget): once it is spent — before a call, or by the end of
// one it cut — the exit falls back to borrowPdx at once (logged), whose
// own acquire re-reads the lease from scratch, so a release of ours that
// landed just before is never left with nobody's control: the freed lease
// is simply acquired.
func (m *Module) takeControlMode(parent context.Context, execID, callerLease, principal string, mode controlMode) (control, *handoffError) {
	if callerLease != "" {
		return control{LeaseID: callerLease, PrincipalID: principal, release: noRelease}, nil
	}
	passes, budget := controlPasses, noBudget
	if mode == exitPreemptPdx {
		passes, budget = exitPreemptPasses, newPreemptBudget(exitPreemptBudget)
	}
	// overBudget is exit's fall back once its budget is spent (D4).
	overBudget := func(err error) (control, *handoffError) {
		m.logf("nex: %s: the preempt ran out of its %v budget (%v); falling back to borrowing the holder's lease, or acquiring it if free (D4)", execID, exitPreemptBudget, err)
		return m.takeControlMode(parent, execID, "", principal, borrowPdx)
	}
	lastHolder := ""
	for attempt := 0; attempt < passes; attempt++ {
		lease, err := m.acquireLeaseWithin(parent, budget, execID, principal)
		if err == nil {
			return m.ownControl(parent, execID, lease.ID, principal), nil
		}
		if errors.Is(err, errPreemptBudget) {
			return overBudget(err)
		}
		if !errors.Is(err, store.ErrLeaseHeld) {
			return control{release: noRelease}, &handoffError{http.StatusInternalServerError, "lease_error", "acquiring lease: " + err.Error(), nil}
		}
		row, gerr := m.getExecutionWithin(parent, budget, execID)
		if errors.Is(gerr, errPreemptBudget) {
			return overBudget(gerr)
		}
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
		if mode == borrowPdx {
			return control{LeaseID: row.LeaseID, PrincipalID: row.LeasePrincipalID, release: noRelease}, nil
		}
		// D22: release the holder's lease as the holder (Nexen's release
		// needs its lease id and principal), then take an exclusive one.
		// The holder's next send, answer or renew fails; its re-attach sees
		// held_by until we release ours.
		if err := m.releaseLeaseWithin(parent, budget, execID, row.LeaseID, row.LeasePrincipalID); err != nil {
			if errors.Is(err, errPreemptBudget) {
				// Cut at the budget's end, the release may still have landed:
				// the borrow's acquire takes the lease if it is free now.
				return overBudget(err)
			}
			if isLeaseErr(err) {
				continue // the holder re-attached or released between our read and our release
			}
			if mode == exitPreemptPdx {
				// D4: an exit never fails because of another tab. The release
				// failed outright, so the holder's lease is still held: act
				// under it, as the contention fall back does.
				m.logf("nex: %s: releasing lease %s of %s failed (%v); borrowing the holder's lease instead (D4)", execID, row.LeaseID, row.LeasePrincipalID, err)
				return control{LeaseID: row.LeaseID, PrincipalID: row.LeasePrincipalID, release: noRelease}, nil
			}
			return control{release: noRelease}, &handoffError{http.StatusInternalServerError, "lease_error", "releasing the holder's lease: " + err.Error(), nil}
		}
		m.logf("nex: %s: preempted lease %s of %s (D22)", execID, row.LeaseID, row.LeasePrincipalID)
		lease, err = m.acquireLeaseWithin(parent, budget, execID, principal)
		if err == nil {
			return m.ownControl(parent, execID, lease.ID, principal), nil
		}
		if errors.Is(err, errPreemptBudget) {
			return overBudget(err) // the holder's lease is released: the borrow's acquire takes it if still free
		}
		if !errors.Is(err, store.ErrLeaseHeld) {
			return control{release: noRelease}, &handoffError{http.StatusInternalServerError, "lease_error", "acquiring lease: " + err.Error(), nil}
		}
		// Somebody acquired between our release and our acquire: once more.
	}
	if mode == exitPreemptPdx {
		m.logf("nex: %s: preempting the lease lost %d passes (last holder %q); borrowing the holder's lease instead (D4)", execID, passes, lastHolder)
		return m.takeControlMode(parent, execID, "", principal, borrowPdx)
	}
	var detail map[string]any
	if lastHolder != "" {
		detail = map[string]any{"principal": lastHolder}
	}
	return control{release: noRelease}, &handoffError{http.StatusConflict, "lease_contended", "execution lease kept changing hands", detail}
}

// ownControl is a lease acquired under principal: ours to release.
func (m *Module) ownControl(parent context.Context, execID, leaseID, principal string) control {
	return control{LeaseID: leaseID, PrincipalID: principal, release: func() {
		if err := m.releaseLease(parent, execID, leaseID, principal); err != nil {
			m.logf("nex: releasing lease %s on %s: %v", leaseID, execID, err)
		}
	}}
}

// renewControl is renewControlMode in exit's mode (exitPreemptPdx).
func (m *Module) renewControl(parent context.Context, execID string, ctl control, principal string) (control, *handoffError) {
	return m.renewControlMode(parent, execID, ctl, principal, exitPreemptPdx)
}

// renewControlMode renews ctl's lease under its holder. A lease-class
// refusal (gone, expired, someone else's) releases ctl and re-takes
// control in the same mode; any other error keeps ctl for the caller to
// release.
func (m *Module) renewControlMode(parent context.Context, execID string, ctl control, principal string, mode controlMode) (control, *handoffError) {
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
	return m.takeControlMode(parent, execID, "", principal, mode)
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
