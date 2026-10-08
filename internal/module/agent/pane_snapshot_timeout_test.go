package agent

import (
	"context"
	"net/http"
	"testing"
	"time"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/tmux"
)

// stuckFake is a tmux whose batch call hangs until its context ends while
// stuck is set, like a tmux server that stopped answering.
type stuckFake struct {
	*tmux.FakeExecutor
	stuck bool
}

func (s *stuckFake) ListPanePlacements(ctx context.Context) (map[string]tmux.PanePlacement, error) {
	if s.stuck {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return s.FakeExecutor.ListPanePlacements(ctx)
}

// A projection read that times out on tmux is a FAILED read, not an empty one:
// an empty one would resolve no pane to a session, find no projection and have
// the hook path broadcast a clear (#717) for a session that is merely
// unreadable. Every path that reads must put nothing on the wire and leave the
// baseline alone; the worker keeps the sid dirty; once tmux answers again the
// next emit carries the right light.
func TestBatchTimeout_NoPathBroadcastsAndRecoveryRestoresState(t *testing.T) {
	r := newWorkerRig(t)
	r.registerIdleHooks()
	orig := paneSnapshotTimeout
	paneSnapshotTimeout = 30 * time.Millisecond
	t.Cleanup(func() { paneSnapshotTimeout = orig })
	seedIdentityFrame(t, r.m, "%5", "cc", 200, "Sun Apr 20 01:30:00 2026", 10, modSID1, "/w")
	st := &stuckFake{FakeExecutor: tmux.NewFakeExecutor()}
	st.SetPaneSessionName("%5", "work")
	r.m.tmux = st

	// Baseline: the hook's idle light is on the wire.
	if w := postEvent(r.m, hookStopBody); w.Code != http.StatusOK {
		t.Fatalf("baseline hook: status %d body=%s", w.Code, w.Body.String())
	}
	wantOneEmit(t, "baseline", r.drain(t), "code-work", "idle", "hook")
	r.m.mu.Lock()
	before := r.m.lastEmittedLights["work"]
	r.m.mu.Unlock()

	st.stuck = true

	// 1. hook handler: the hook is refused for a retry, nothing is broadcast.
	if w := postEvent(r.m, hookStopBody); w.Code == http.StatusOK {
		t.Fatalf("hook during a stuck tmux answered %d, want a failure the hook retries", w.Code)
	}
	if got := r.drain(t); len(got) != 0 {
		t.Fatalf("hook path broadcast during a stuck tmux: %+v", got)
	}

	// 2. worker: nothing is sent, the sid is dirty again.
	r.m.modMu.Lock()
	r.m.modDirty[modSID1] = "heartbeat"
	r.m.modMu.Unlock()
	r.round()
	if got := r.drain(t); len(got) != 0 {
		t.Fatalf("worker broadcast during a stuck tmux: %+v", got)
	}
	r.m.modMu.Lock()
	_, redirtied := r.m.modDirty[modSID1]
	r.m.modMu.Unlock()
	if !redirtied {
		t.Fatal("worker dropped the sid after a failed read; the next round would never retry it")
	}

	// 3. snapshot to a subscriber: no frame.
	r.m.sendSnapshot(r.sub)
	if got := r.drain(t); len(got) != 0 {
		t.Fatalf("snapshot sent during a stuck tmux: %+v", got)
	}

	r.m.mu.Lock()
	after := r.m.lastEmittedLights["work"]
	r.m.mu.Unlock()
	if after != before {
		t.Fatalf("baseline changed by failed reads: %+v -> %+v", before, after)
	}

	// Recovery: the retried sid now carries the mod's light, and a hook emit is
	// right again.
	st.stuck = false
	feedMod(r.m, modStrm, modStart, modTurnStart)
	r.round()
	wantOneEmit(t, "worker after recovery", r.drain(t), "code-work", "running", "mod")
	if w := postEvent(r.m, hookStopBody); w.Code != http.StatusOK {
		t.Fatalf("hook after recovery: status %d", w.Code)
	}
	for _, e := range r.drain(t) {
		if e.Ev.Status == string(agentpkg.StatusClear) {
			t.Fatalf("a clear went out after recovery: %+v", e)
		}
	}
}
