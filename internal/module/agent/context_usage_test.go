package agent

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	agentpkg "github.com/wake/purdex/internal/agent"
	agentcc "github.com/wake/purdex/internal/agent/cc"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/module/session"
	"github.com/wake/purdex/internal/tmux"
)

func postStatus(t *testing.T, m *Module, tmuxName, raw string) {
	t.Helper()
	body := `{"tmux_session":"` + tmuxName + `","agent_type":"cc","raw_status":` + raw + `}`
	m.handleAgentStatus(httptest.NewRecorder(), httptest.NewRequest("POST", "/api/agent/status", strings.NewReader(body)))
}

func usageModule(t *testing.T) *Module {
	m := newTestModule(t)
	m.sessions = &fakeSessionProvider{sessions: []session.SessionInfo{{Name: "sess1", Code: "code-1"}}}
	m.core = &core.Core{Events: core.NewEventsBroadcaster(), Tmux: tmux.NewFakeExecutor()}
	return m
}

func TestContextUsage_RecordedPerSessionID(t *testing.T) {
	m := usageModule(t)
	postStatus(t, m, "sess1", `{"session_id":"A","context_window":{"used_percentage":72.4,"context_window_size":1000000}}`)
	postStatus(t, m, "sess1", `{"session_id":"B","context_window":{"used_percentage":10,"context_window_size":200000}}`)
	a, ok := m.ContextUsage("A")
	if !ok || a.UsedPercentage == nil || *a.UsedPercentage != 72.4 || a.WindowSize != 1000000 {
		t.Fatalf("A = %+v ok=%v", a, ok)
	}
	b, ok := m.ContextUsage("B")
	if !ok || *b.UsedPercentage != 10 || b.WindowSize != 200000 {
		t.Fatalf("B = %+v ok=%v (two panes in one tmux session must not overwrite each other)", b, ok)
	}
}

func TestContextUsage_NullPercentageAndMissingSessionID(t *testing.T) {
	m := usageModule(t)
	postStatus(t, m, "sess1", `{"session_id":"A","context_window":{"used_percentage":null,"context_window_size":200000}}`)
	a, ok := m.ContextUsage("A")
	if !ok || a.UsedPercentage != nil || a.WindowSize != 200000 {
		t.Fatalf("null percentage: got %+v ok=%v", a, ok)
	}
	postStatus(t, m, "sess1", `{"context_window":{"used_percentage":50}}`)
	postStatus(t, m, "sess1", `"not an object"`)
	if _, ok := m.ContextUsage(""); ok {
		t.Fatal("a payload without session_id must not be recorded")
	}
}

func TestContextUsage_RecordedEvenForUnknownTmuxSession(t *testing.T) {
	m := usageModule(t)
	postStatus(t, m, "no-such-tmux", `{"session_id":"Z","context_window":{"used_percentage":5,"context_window_size":200000}}`)
	if _, ok := m.ContextUsage("Z"); !ok {
		t.Fatal("usage is keyed by CC session id and must not depend on resolving the tmux name")
	}
}

func TestContextUsage_BoundedMapEvictsOldest(t *testing.T) {
	m := usageModule(t)
	for i := 0; i < contextUsageCap+1; i++ {
		postStatus(t, m, "sess1", fmt.Sprintf(`{"session_id":"s%d","context_window":{"used_percentage":1,"context_window_size":1}}`, i))
	}
	if _, ok := m.ContextUsage("s0"); ok {
		t.Fatal("the oldest entry must be evicted past the cap")
	}
	if _, ok := m.ContextUsage(fmt.Sprintf("s%d", contextUsageCap)); !ok {
		t.Fatal("the newest entry must be kept")
	}
}

// U18 (b) / M21: the statusline payload is the only place model.id and
// effort.level are reported, so the parser records them beside the usage.
// Both are "" when absent; effort is absent on some models.
func TestContextUsage_RecordsModelAndEffort(t *testing.T) {
	m := usageModule(t)
	postStatus(t, m, "sess1", `{"session_id":"A","model":{"id":"claude-sonnet-5-5"},"effort":{"level":"low"},"context_window":{"used_percentage":12,"context_window_size":200000}}`)
	a, ok := m.ContextUsage("A")
	if !ok || a.ModelID != "claude-sonnet-5-5" || a.Effort != "low" {
		t.Fatalf("A = %+v ok=%v, want model claude-sonnet-5-5 effort low", a, ok)
	}
	postStatus(t, m, "sess1", `{"session_id":"B","context_window":{"used_percentage":12,"context_window_size":200000}}`)
	b, ok := m.ContextUsage("B")
	if !ok || b.ModelID != "" || b.Effort != "" {
		t.Fatalf("B = %+v ok=%v, want empty model and effort when the payload has neither", b, ok)
	}
}

// codex R2 (2026-10-07): a successful statusline remove rebuilds
// statusSnapshots; the per-session context usage must go with it, or
// /api/peers and `pdx peers` keep showing a stale CTX after the statusline
// is gone. Drives the remove exactly like
// TestHandleStatuslineSetup_RemoveBroadcastsCleared does.
func TestContextUsage_ClearedByStatuslineRemove(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude", "settings.json"),
		[]byte(`{"statusLine":{"type":"command","command":"/opt/bin/pdx statusline-proxy"}}`), 0644); err != nil {
		t.Fatal(err)
	}

	m := usageModule(t)
	m.registry.Register(agentcc.NewProvider(nil, nil, nil, nil))
	postStatus(t, m, "sess1", `{"session_id":"A","context_window":{"used_percentage":90,"context_window_size":200000}}`)
	if _, ok := m.ContextUsage("A"); !ok {
		t.Fatal("precondition: usage for A must be recorded before the remove")
	}

	req := httptest.NewRequest("POST", "/api/agent/cc/statusline/setup", strings.NewReader(`{"action":"remove"}`))
	req.SetPathValue("agent", "cc")
	w := httptest.NewRecorder()
	m.handleStatuslineSetup(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("remove status %d, body: %s", w.Code, w.Body.String())
	}

	if u, ok := m.ContextUsage("A"); ok {
		t.Fatalf("usage for A = %+v still present after statusline remove; want cleared", u)
	}
}

var _ ContextUsageReader = (*Module)(nil)

// P7-1: the team module's 70% idle notice reads a tmux session's status through AgentStatus.
func TestAgentStatus_ByTmuxSession(t *testing.T) {
	m := usageModule(t)
	if st, ok := m.AgentStatus("sess1"); ok || st != "" {
		t.Fatalf("an unknown session = %q ok=%v, want none", st, ok)
	}
	m.mu.Lock()
	m.currentStatus["sess1"] = agentpkg.StatusIdle
	m.currentStatus["sess2"] = agentpkg.StatusRunning
	m.mu.Unlock()
	if st, ok := m.AgentStatus("sess1"); !ok || st != "idle" {
		t.Fatalf("sess1 = %q ok=%v, want idle", st, ok)
	}
	if st, ok := m.AgentStatus("sess2"); !ok || st != "running" {
		t.Fatalf("sess2 = %q ok=%v, want running (keyed by tmux session, not shared)", st, ok)
	}
}
