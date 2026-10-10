package agent

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync/atomic"
	"time"

	"github.com/wake/purdex/internal/tmux"
)

// A projection read needs two facts per pane that has a frame: the pid of the
// pane's process (to tell which frames the pane owns) and the name of the tmux
// session the pane is in (to pick the session's representative pane). Asked
// pane by pane that is two tmux children each, ~27 ms apiece, inside the emit
// slot: 23 panes held it for 1.2 s and 610 had to be rolled back. A
// paneSnapshot is ONE `list-panes -a` taken at the start of a read that
// answers both for every pane.

// paneSnapshotTimeout bounds the one batch call, and every per-pane lookup that
// stands in for it (#2039). A call that merely fails falls back to the per-pane
// lookups, which share ONE deadline of this length for the whole read (a stuck
// tmux costs the read this once, not once per pane); one that runs out of time
// does not fall back (tmux is stuck): the read FAILS (errPaneSnapshotTimeout). It must
// not carry on with an empty snapshot: no pane would resolve to a session, the
// projection would be nil and the caller would broadcast a clear (#717) for a
// session that is only unreadable. A failed read broadcasts nothing and leaves
// the baseline alone. A var so tests can shorten it.
var paneSnapshotTimeout = 2 * time.Second

// batchFailLogEvery is how often a failing batch call is logged: every read
// would otherwise log once, and reads run hundreds of times a minute.
const batchFailLogEvery = time.Minute

// batchFailLastLog is the unix-nano time of the last "snapshot unavailable"
// line; batchFailSuppressed counts the reads that stayed quiet since.
var (
	batchFailLastLog    atomic.Int64
	batchFailSuppressed atomic.Int64
)

var (
	errPaneNotListed       = errors.New("pane not in tmux listing")
	errPaneSnapshotTimeout = errors.New("pane snapshot timed out")
)

// paneSnapshot is one successful batch answer, or (fallback) the stand-in for a
// batch that failed without timing out: every lookup then asks tmux about the
// one pane, all of them before deadline. A nil *paneSnapshot means there is no
// tmux at all.
type paneSnapshot struct {
	m        *Module
	panes    map[string]tmux.PanePlacement
	fallback bool
	deadline time.Time // fallback only: the read's one budget for per-pane lookups
}

// lookupCtx is the context of one per-pane lookup: the read's deadline.
func (s *paneSnapshot) lookupCtx() (context.Context, context.CancelFunc) {
	return context.WithDeadline(context.Background(), s.deadline)
}

// takePaneSnapshot makes the batch call. (nil, nil) when there is no tmux; a
// fallback snapshot when the call failed without timing out (logged at most once
// per batchFailLogEvery).
func (m *Module) takePaneSnapshot() (*paneSnapshot, error) {
	if m == nil || m.tmux == nil {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), paneSnapshotTimeout)
	defer cancel()
	panes, err := m.tmux.ListPanePlacements(ctx)
	if err != nil {
		timedOut := errors.Is(ctx.Err(), context.DeadlineExceeded)
		logBatchFailure(err, timedOut)
		if timedOut {
			return nil, fmt.Errorf("%w: %v", errPaneSnapshotTimeout, err)
		}
		return &paneSnapshot{m: m, fallback: true, deadline: time.Now().Add(paneSnapshotTimeout)}, nil
	}
	return &paneSnapshot{m: m, panes: panes}, nil
}

// lookupDeadlineLastLog / lookupDeadlineSuppressed are logBatchFailure's for a per-pane lookup that ran out of time: one line
// a minute, not one per pane.
var (
	lookupDeadlineLastLog    atomic.Int64
	lookupDeadlineSuppressed atomic.Int64
)

// logLookupDeadline notes a per-pane lookup that the read's deadline cut off (other failures are tmux's own and were never
// logged here). The lookup counts as "not found" for the caller: the existing failure path.
func logLookupDeadline(err error) {
	if !errors.Is(err, context.DeadlineExceeded) {
		return
	}
	now := time.Now().UnixNano()
	last := lookupDeadlineLastLog.Load()
	if last != 0 && now-last < int64(batchFailLogEvery) {
		lookupDeadlineSuppressed.Add(1)
		return
	}
	if !lookupDeadlineLastLog.CompareAndSwap(last, now) {
		lookupDeadlineSuppressed.Add(1)
		return
	}
	log.Printf("[agent] a per-pane tmux lookup ran out of time, treated as not found (%d similar since the last report): %v",
		lookupDeadlineSuppressed.Swap(0), err)
}

// boundedPaneLookup is the context of a per-pane lookup that has no read to share a deadline with.
func boundedPaneLookup() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), paneSnapshotTimeout)
}

func logBatchFailure(err error, timedOut bool) {
	now := time.Now().UnixNano()
	last := batchFailLastLog.Load()
	if last != 0 && now-last < int64(batchFailLogEvery) {
		batchFailSuppressed.Add(1)
		return
	}
	if !batchFailLastLog.CompareAndSwap(last, now) {
		batchFailSuppressed.Add(1)
		return
	}
	action := "reading panes one by one"
	if timedOut {
		action = "failing the read (no per-pane retry)"
	}
	log.Printf("[agent] pane snapshot unavailable, %s (%d similar reads since the last report): %v",
		action, batchFailSuppressed.Swap(0), err)
}

// panePID is resolvePanePID from the snapshot. A pane the listing does not
// have is the pane ActivePanePID fails on: an error, so the caller keeps its
// frames unfiltered.
func (s *paneSnapshot) panePID(paneID string) (int, error) {
	if s.fallback {
		ctx, cancel := s.lookupCtx()
		defer cancel()
		pid, err := s.m.tmux.ActivePanePIDCtx(ctx, paneID)
		if err != nil {
			logLookupDeadline(err)
			return 0, err
		}
		return parsePanePID(pid)
	}
	p, ok := s.panes[paneID]
	if !ok {
		return 0, errPaneNotListed
	}
	return parsePanePID(p.PID)
}

// sessionName is Module.paneSessionName from the snapshot: "" for a pane tmux
// does not list. A pane linked into sessions the listing cannot tell apart
// (tmux.PanePlacement.Ambiguous) is the one case that still asks tmux, about
// that pane alone.
func (s *paneSnapshot) sessionName(paneID string) string {
	if s.fallback {
		ctx, cancel := s.lookupCtx()
		defer cancel()
		name, err := s.m.tmux.PaneSessionNameCtx(ctx, paneID)
		if err != nil {
			logLookupDeadline(err)
			return ""
		}
		return name
	}
	p, ok := s.panes[paneID]
	if !ok {
		return ""
	}
	if p.Ambiguous {
		return s.m.paneSessionName(paneID)
	}
	return p.SessionName
}
