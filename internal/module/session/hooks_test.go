package session

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/tmux"
)

func newHooksTestModule(hooksOutput string) *SessionModule {
	fake := tmux.NewFakeExecutor()
	fake.HooksOutput = hooksOutput
	return &SessionModule{tmux: fake}
}

func TestHandleTmuxHookStatus_AllInstalled(t *testing.T) {
	mod := newHooksTestModule(
		"session-created[0] -> run-shell -b 'tmux wait-for -S purdex_sess_evt'\nsession-closed[0] -> run-shell -b 'tmux wait-for -S purdex_sess_evt'\nsession-renamed[0] -> run-shell -b 'tmux wait-for -S purdex_sess_evt'\n",
	)

	req := httptest.NewRequest("GET", "/api/hooks/tmux/status", nil)
	w := httptest.NewRecorder()
	mod.handleTmuxHookStatus(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp struct {
		Installed bool                       `json:"installed"`
		Events    map[string]json.RawMessage `json:"events"`
		Issues    []string                   `json:"issues"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Installed {
		t.Error("expected installed=true when all hooks present")
	}
	if len(resp.Events) != 3 {
		t.Errorf("expected 3 events, got %d", len(resp.Events))
	}
	if len(resp.Issues) != 0 {
		t.Errorf("expected 0 issues, got %v", resp.Issues)
	}
}

func TestHandleTmuxHookStatus_NoneInstalled(t *testing.T) {
	mod := newHooksTestModule("")

	req := httptest.NewRequest("GET", "/api/hooks/tmux/status", nil)
	w := httptest.NewRecorder()
	mod.handleTmuxHookStatus(w, req)

	var resp struct {
		Installed bool `json:"installed"`
	}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Installed {
		t.Error("expected installed=false when no hooks present")
	}
}

func TestHandleTmuxHookSetup_Install(t *testing.T) {
	mod := newHooksTestModule(
		"session-created[0] -> run-shell -b 'tmux wait-for -S purdex_sess_evt'\nsession-closed[0] -> run-shell -b 'tmux wait-for -S purdex_sess_evt'\nsession-renamed[0] -> run-shell -b 'tmux wait-for -S purdex_sess_evt'\n",
	)

	body := strings.NewReader(`{"action":"install"}`)
	req := httptest.NewRequest("POST", "/api/hooks/tmux/setup", body)
	w := httptest.NewRecorder()
	mod.handleTmuxHookSetup(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp struct {
		Installed bool `json:"installed"`
	}
	json.NewDecoder(w.Body).Decode(&resp)
	if !resp.Installed {
		t.Error("expected installed=true after install")
	}
}

func TestHandleTmuxHookSetup_Remove(t *testing.T) {
	mod := newHooksTestModule("")

	body := strings.NewReader(`{"action":"remove"}`)
	req := httptest.NewRequest("POST", "/api/hooks/tmux/setup", body)
	w := httptest.NewRecorder()
	mod.handleTmuxHookSetup(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHandleTmuxHookSetup_InvalidAction(t *testing.T) {
	mod := newHooksTestModule("")

	body := strings.NewReader(`{"action":"restart"}`)
	req := httptest.NewRequest("POST", "/api/hooks/tmux/setup", body)
	w := httptest.NewRecorder()
	mod.handleTmuxHookSetup(w, req)

	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}
