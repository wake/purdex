package agent

import (
	"context"
	"errors"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/wake/purdex/internal/store"
	"github.com/wake/purdex/internal/tmux"
)

// noBatchTmux is a real executor whose batch read always fails: the read
// takes the per-pane path, which is what every projection read cost before
// the snapshot.
type noBatchTmux struct{ tmux.Executor }

func (noBatchTmux) ListPanePlacements(context.Context) (map[string]tmux.PanePlacement, error) {
	return nil, errors.New("batch disabled for the comparison")
}

// TestRealTmuxProjectionReadBench times a whole projection read
// (liveSessionProjections: frame filter + pane -> session name) against the
// tmux server on the default socket, once with the batch snapshot and once
// per pane, over one synthetic live frame per pane tmux lists. It only reads
// (list-panes, display-message) and is skipped unless PDX_REAL_TMUX_BENCH=1,
// so it never slows a normal run:
//
//	PDX_REAL_TMUX_BENCH=1 go test ./internal/module/agent/ -run TestRealTmuxProjectionReadBench -v -count=1
func TestRealTmuxProjectionReadBench(t *testing.T) {
	if os.Getenv("PDX_REAL_TMUX_BENCH") != "1" {
		t.Skip("set PDX_REAL_TMUX_BENCH=1 to time projection reads against the real tmux server")
	}
	real := &tmux.RealExecutor{}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	listed, err := real.ListPanePlacements(ctx)
	if err != nil || len(listed) == 0 {
		t.Skipf("no tmux server to read (%d panes, err %v)", len(listed), err)
	}
	paneIDs := make([]string, 0, len(listed))
	ambiguous := 0
	for id, p := range listed {
		paneIDs = append(paneIDs, id)
		if p.Ambiguous {
			ambiguous++
		}
	}
	sort.Strings(paneIDs)

	// The listing must agree with the per-pane answers it replaces (a pane the
	// listing calls Ambiguous is allowed to disagree: that is what it says).
	for _, id := range paneIDs {
		p := listed[id]
		if pid, err := real.ActivePanePID(id); err != nil || pid != p.PID {
			t.Errorf("pane %s: ActivePanePID = %q, %v; listing says %q", id, pid, err, p.PID)
		}
		if name, err := real.PaneSessionName(id); !p.Ambiguous && (err != nil || name != p.SessionName) {
			t.Errorf("pane %s: PaneSessionName = %q, %v; listing says %q", id, name, err, p.SessionName)
		}
	}

	m := newTestModule(t)
	m.tmux = real
	origAncestor := pidAncestorIncludesFn
	pidAncestorIncludesFn = func(int, int) bool { return true }
	t.Cleanup(func() { pidAncestorIncludesFn = origAncestor })
	m.listFramesFn = func() ([]store.Frame, error) {
		frames := make([]store.Frame, 0, len(paneIDs))
		for i, id := range paneIDs {
			frames = append(frames, store.Frame{
				FrameID: "bench-" + id, PaneID: id, AgentType: "cc", PID: 100000 + i,
				ProcessStartTime: "Sun Apr 20 01:30:00 2026", Status: "idle",
				StartedAt: int64(i + 1), LastSeenAt: int64(i + 1), Verified: true,
			})
		}
		return frames, nil
	}

	timeRead := func() (time.Duration, int) {
		t0 := time.Now()
		named, err := m.liveSessionProjections()
		d := time.Since(t0)
		if err != nil {
			t.Fatal(err)
		}
		return d, len(named)
	}
	t.Logf("panes=%d (ambiguous linked: %d)", len(paneIDs), ambiguous)
	for run := 1; run <= 5; run++ {
		m.tmux = real
		batch, sessions := timeRead()
		m.tmux = noBatchTmux{real}
		perPane, sessions2 := timeRead()
		t.Logf("run %d: batch %v | per-pane %v (sessions %d / %d)", run, batch.Round(time.Microsecond), perPane.Round(time.Microsecond), sessions, sessions2)
		if sessions != sessions2 {
			t.Errorf("run %d: the two paths found %d and %d sessions", run, sessions, sessions2)
		}
	}
}
