package session

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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

// --- Hook changes serialised with Stop (#1473 spec D3.1, review A2) ---

// An install already in flight when Stop arrives must finish before Stop's
// remove, and nothing may be set after Stop returns: the daemon must not
// leave hooks behind that point at a wait-for nobody listens to.
func TestEnsureHooks_ConcurrentStop_NoSetAfterRemove(t *testing.T) {
	mod, fake, _ := newHookTestModule(t, true)
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	fake.SetHookGlobalGate(entered, release)

	installDone := make(chan struct{})
	go func() {
		defer close(installDone)
		mod.ensureHooks("")
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("install never reached set-hook")
	}

	stopDone := make(chan struct{})
	go func() {
		defer close(stopDone)
		_ = mod.Stop(context.Background())
	}()
	// Give Stop the chance to run ahead of the blocked install.
	time.Sleep(50 * time.Millisecond)
	close(release)
	<-installDone
	select {
	case <-stopDone:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop never returned")
	}

	calls := fake.HookCalls()
	require.NotEmpty(t, calls)
	firstRemove := -1
	for i, c := range calls {
		if strings.HasPrefix(c, "remove ") {
			firstRemove = i
			break
		}
	}
	require.GreaterOrEqual(t, firstRemove, 0, "Stop must remove the hooks: %v", calls)
	for _, c := range calls[firstRemove:] {
		assert.False(t, strings.HasPrefix(c, "set "), "set-hook after Stop's remove: %v", calls)
	}
}

func TestEnsureHooks_AfterStop_IsNoop(t *testing.T) {
	mod, fake, _ := newHookTestModule(t, true)
	require.NoError(t, mod.Stop(context.Background()))
	fake.ResetHookSets()

	mod.wstate.clearHooksOK()
	mod.ensureHooks("")
	mod.ensureHooks("111:1000")
	assert.Empty(t, fake.HookSets(), "no hook may be installed after Stop")
}

// --- Manual setup API vs the watcher (#1473 spec D3.1, review A3) ---

func postHookSetup(t *testing.T, mod *SessionModule, action string) int {
	t.Helper()
	req := httptest.NewRequest("POST", "/api/hooks/tmux/setup", strings.NewReader(`{"action":"`+action+`"}`))
	w := httptest.NewRecorder()
	mod.handleTmuxHookSetup(w, req)
	return w.Code
}

// A manual remove is an opt-out: the watcher must not put the hooks back,
// not even for a new server.
func TestHookSetup_RemoveDisablesWatcherReinstall(t *testing.T) {
	mod, fake, _ := newHookTestModule(t, true)
	fake.AddSession("dev", "/w")
	setInstance(mod, "111:1000")
	mod.tickNormal()
	require.Equal(t, allHookEvents, fake.HookSets())

	require.Equal(t, 200, postHookSetup(t, mod, "remove"))
	fake.ResetHookSets()
	setInstance(mod, "222:2000")
	mod.tickNormal()
	assert.Empty(t, fake.HookSets(), "a manual remove must not be undone by the watcher")
}

// A manual install after a remove re-enables the watcher and counts as an
// install: a tick with nothing new to compare does not install again.
func TestHookSetup_InstallAfterRemoveRecordsOutcome(t *testing.T) {
	mod, fake, _ := newHookTestModule(t, true)
	require.Equal(t, 200, postHookSetup(t, mod, "remove"))
	require.Equal(t, 200, postHookSetup(t, mod, "install"))
	require.Equal(t, allHookEvents, fake.HookSets())

	fake.ResetHookSets()
	mod.tickNormal() // no sessions: payload instance ""
	assert.Empty(t, fake.HookSets(), "a successful manual install must not be repeated")

	// Re-enabled: a new server gets its hooks again.
	fake.AddSession("dev", "/w")
	setInstance(mod, "222:2000")
	mod.tickNormal()
	assert.Equal(t, allHookEvents, fake.HookSets())
}

// A failed manual install leaves the hooks unknown, so the watcher retries.
func TestHookSetup_InstallFailureRetriedByTick(t *testing.T) {
	mod, fake, _ := newHookTestModule(t, true)
	fake.AddSession("dev", "/w")
	setInstance(mod, "111:1000")
	mod.tickNormal()
	require.Equal(t, allHookEvents, fake.HookSets())

	fake.SetHookGlobalError(errors.New("no server running"))
	require.Equal(t, 500, postHookSetup(t, mod, "install"))

	fake.SetHookGlobalError(nil)
	fake.ResetHookSets()
	mod.tickNormal()
	assert.Equal(t, allHookEvents, fake.HookSets(), "the watcher must retry a failed manual install")
}
