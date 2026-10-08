package agent

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/store"
	"github.com/wake/purdex/internal/tmux"
)

const (
	guardStart = "Sun Apr 20 01:30:00 2026"
	guardOwn   = "%5" // the sender's pane (pid 200)
	guardOther = "%6" // another pane of the same tmux session (pid 300)
)

// errorGuardModule is two panes of tmux session "work", each with a frame:
// the sender's (pid 200, guardOwn) with ownStatus and the other pane's
// (pid 300, guardOther) with otherStatus. A cc PostToolUse maps to running.
func errorGuardModule(t *testing.T, ownStatus, otherStatus agentpkg.Status) *Module {
	t.Helper()
	m := newTestModule(t)
	fakeTmux := tmux.NewFakeExecutor()
	fakeTmux.SetPaneSessionName(guardOwn, "work")
	fakeTmux.SetPaneSessionName(guardOther, "work")
	m.tmux = fakeTmux
	m.registry.Register(&fakeAgentProvider{
		typeName: "cc",
		derive: func(string, json.RawMessage) agentpkg.DeriveResult {
			return agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusRunning}
		},
	})
	for _, f := range []struct {
		pane   string
		pid    int
		status agentpkg.Status
	}{{guardOwn, 200, ownStatus}, {guardOther, 300, otherStatus}} {
		if _, err := m.frames.Upsert(store.Frame{
			PaneID: f.pane, AgentType: "cc", PID: f.pid, PPID: 1, ProcessStartTime: guardStart,
			Status: f.status, StartedAt: 10, LastSeenAt: 10, Verified: true,
		}); err != nil {
			t.Fatalf("seed frame %s: %v", f.pane, err)
		}
	}
	return m
}

func guardPostToolUse(t *testing.T, m *Module) {
	t.Helper()
	body := `{"tmux_session":"work","tmux_pane_id":"` + guardOwn + `","sender_pid":200,"sender_start_time":"` + guardStart + `","purdex_name":"PdxPostToolUse","raw_event":{},"agent_type":"cc"}`
	req := httptest.NewRequest("POST", "/api/agent/event", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	m.handleEvent(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
}

func guardFrameStatus(t *testing.T, m *Module, pane string, pid int) agentpkg.Status {
	t.Helper()
	f, err := m.frames.GetByIdentity(pane, pid, guardStart)
	if err != nil || f == nil {
		t.Fatalf("frame %s/%d: %v / %v", pane, pid, f, err)
	}
	return f.Status
}

// The guard asks "is THIS pane in error", not "is any pane of the session":
// another pane's error (and the session-level status it left behind) no
// longer swallows this pane's events.
func TestErrorGuard_OtherPaneErrorDoesNotBlock(t *testing.T) {
	m := errorGuardModule(t, agentpkg.StatusIdle, agentpkg.StatusError)
	m.mu.Lock()
	m.currentStatus["work"] = agentpkg.StatusError // what the other pane's error left in the session view
	m.mu.Unlock()

	guardPostToolUse(t, m)

	if got := guardFrameStatus(t, m, guardOwn, 200); got != agentpkg.StatusRunning {
		t.Fatalf("own frame status = %q, want running (the other pane's error must not block it)", got)
	}
	if got := guardFrameStatus(t, m, guardOther, 300); got != agentpkg.StatusError {
		t.Fatalf("other pane's frame status = %q, want error untouched", got)
	}
}

// A pane in error is still held there by anything outside the whitelist.
func TestErrorGuard_OwnPaneErrorStillBlocks(t *testing.T) {
	m := errorGuardModule(t, agentpkg.StatusError, agentpkg.StatusIdle)
	// The session view does NOT say error (it could be overlaid or stale):
	// the guard must read the pane's own frame.
	m.mu.Lock()
	m.currentStatus["work"] = agentpkg.StatusIdle
	m.mu.Unlock()
	before, _ := m.frames.GetByIdentity(guardOwn, 200, guardStart)

	guardPostToolUse(t, m)

	got, _ := m.frames.GetByIdentity(guardOwn, 200, guardStart)
	if got.Status != agentpkg.StatusError {
		t.Fatalf("own frame status = %q, want error kept by the guard", got.Status)
	}
	if got.LastSeenAt != before.LastSeenAt {
		t.Fatalf("LastSeenAt %d -> %d: a blocked event must not touch the frame", before.LastSeenAt, got.LastSeenAt)
	}
}
