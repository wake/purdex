package agent

import (
	"context"
	"errors"
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

// paneSnapshotTimeout bounds the one batch call. A call that merely fails falls
// back to the per-pane lookups; one that runs out of time does not (tmux is
// stuck, per-pane calls have no deadline and would hold the emit slot for as
// long as tmux stays stuck): the read proceeds as if tmux knew no pane. A var
// so tests can shorten it.
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

var errPaneNotListed = errors.New("pane not in tmux listing")

// paneSnapshot is one successful batch answer. A nil *paneSnapshot means "no
// snapshot": every lookup then asks tmux about the one pane, as before.
type paneSnapshot struct {
	m     *Module
	panes map[string]tmux.PanePlacement
}

// takePaneSnapshot makes the batch call. nil when there is no tmux, or the
// call failed (logged at most once per batchFailLogEvery).
func (m *Module) takePaneSnapshot() *paneSnapshot {
	if m == nil || m.tmux == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), paneSnapshotTimeout)
	defer cancel()
	panes, err := m.tmux.ListPanePlacements(ctx)
	if err != nil {
		logBatchFailure(err)
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return &paneSnapshot{m: m, panes: map[string]tmux.PanePlacement{}}
		}
		return nil
	}
	return &paneSnapshot{m: m, panes: panes}
}

func logBatchFailure(err error) {
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
	log.Printf("[agent] pane snapshot unavailable, reading panes one by one (%d similar reads since the last report): %v",
		batchFailSuppressed.Swap(0), err)
}

// panePID is resolvePanePID from the snapshot. A pane the listing does not
// have is the pane ActivePanePID fails on: an error, so the caller keeps its
// frames unfiltered.
func (s *paneSnapshot) panePID(paneID string) (int, error) {
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
	p, ok := s.panes[paneID]
	if !ok {
		return ""
	}
	if p.Ambiguous {
		return s.m.paneSessionName(paneID)
	}
	return p.SessionName
}
