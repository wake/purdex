package agent

import (
	"encoding/json"
	"net/http"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/module/session"
)

// fakeSessionProvider is a shared test-only SessionProvider with a
// configurable session list.  Used by handler_test.go and upload_test.go.
//
// When `sessions` is nil, ListSessions/GetSession fall back to the legacy
// single-entry default ("my-sess") so existing upload tests don't need to
// initialize the slice.  Tests that need an empty list should pass
// `sessions: []session.SessionInfo{}` explicitly.
type fakeSessionProvider struct {
	sessions []session.SessionInfo
}

func (f *fakeSessionProvider) ListSessions() ([]session.SessionInfo, error) {
	if f.sessions != nil {
		return f.sessions, nil
	}
	return []session.SessionInfo{{Code: "my-sess", Name: "my-sess"}}, nil
}

func (f *fakeSessionProvider) GetSession(code string) (*session.SessionInfo, error) {
	for _, s := range f.sessions {
		if s.Code == code {
			return &s, nil
		}
	}
	if code == "my-sess" {
		return &session.SessionInfo{Code: "my-sess", Name: "my-sess"}, nil
	}
	return nil, nil
}

func (f *fakeSessionProvider) UpdateMeta(string, session.MetaUpdate) error { return nil }
func (f *fakeSessionProvider) HandleTerminalWS(http.ResponseWriter, *http.Request, string) {
}

// fakeAgentProvider is a configurable AgentProvider for tests. The events
// slice is optional: tests exercising the metadata-driven lifecycle dispatch
// path inject a per-test catalog (typically a slice of HookEventSpec keyed by
// PurdexName + Lifecycle); tests that want the daemon to fall back to the
// pre-W2 legacy-literal path leave it nil. fakeAgentProvider implements
// HookInstaller so handler.classifyLifecycle's type-assert succeeds in both
// cases — the inject-vs-not distinction lives in Events()'s return value, not
// in the type.
type fakeAgentProvider struct {
	typeName string
	alive    bool
	derive   func(eventName string, raw json.RawMessage) agentpkg.DeriveResult
	identify func(agentpkg.ProcessInfo) bool
	events   []agentpkg.HookEventSpec
}

func (f *fakeAgentProvider) Type() string                       { return f.typeName }
func (f *fakeAgentProvider) DisplayName() string                { return f.typeName }
func (f *fakeAgentProvider) IconHint() string                   { return "" }
func (f *fakeAgentProvider) Claim(_ agentpkg.ClaimContext) bool { return false }
func (f *fakeAgentProvider) Identify(info agentpkg.ProcessInfo) bool {
	if f.identify != nil {
		return f.identify(info)
	}
	return false
}
func (f *fakeAgentProvider) IsAlive(string) bool { return f.alive }
func (f *fakeAgentProvider) DeriveStatus(eventName string, raw json.RawMessage) agentpkg.DeriveResult {
	if f.derive != nil {
		return f.derive(eventName, raw)
	}
	return agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusRunning}
}

// fakeDefaultEvents is the W2 default catalog returned by fakeAgentProvider
// when no per-test events are injected. Mirrors the cc Phase 1 lifecycle
// mapping; codex/opencode tests can rely on this since the
// PurdexName ↔ Lifecycle assignment is identical across the three providers
// once migrated.
//
// Tests that need a true catalog miss (the "codex prematurely emits
// PdxXxx before its catalog migrates" negative case) inject an explicit
// empty slice — `events: []agentpkg.HookEventSpec{}` — which is non-nil
// and so bypasses the default-fill below.
var fakeDefaultEvents = []agentpkg.HookEventSpec{
	{Name: "SessionStart", PurdexName: "PdxSessionStart", UpstreamKeys: []string{"SessionStart"}, Lifecycle: agentpkg.LifecycleSessionStart},
	{Name: "UserPromptSubmit", PurdexName: "PdxUserPromptSubmit", UpstreamKeys: []string{"UserPromptSubmit"}, Lifecycle: agentpkg.LifecycleUserPromptSubmit},
	{Name: "Stop", PurdexName: "PdxStop", UpstreamKeys: []string{"Stop"}, Lifecycle: agentpkg.LifecycleStop},
	{Name: "StopFailure", PurdexName: "PdxStopFailure", UpstreamKeys: []string{"StopFailure"}, Lifecycle: agentpkg.LifecycleStopFailure},
	{Name: "SessionEnd", PurdexName: "PdxSessionEnd", UpstreamKeys: []string{"SessionEnd"}, Lifecycle: agentpkg.LifecycleSessionEnd},
	{Name: "SubagentStart", PurdexName: "PdxSubagentStart", UpstreamKeys: []string{"SubagentStart"}, Lifecycle: agentpkg.LifecycleSubagentStart},
	{Name: "SubagentStop", PurdexName: "PdxSubagentStop", UpstreamKeys: []string{"SubagentStop"}, Lifecycle: agentpkg.LifecycleSubagentStop},
	{Name: "Notification", PurdexName: "PdxNotification", UpstreamKeys: []string{"Notification"}, Lifecycle: agentpkg.LifecycleNone},
	{Name: "PermissionRequest", PurdexName: "PdxPermissionRequest", UpstreamKeys: []string{"PermissionRequest"}, Lifecycle: agentpkg.LifecycleNone},
}

// Events / InstallHooks / RemoveHooks / CheckHooks satisfy HookInstaller so
// classifyLifecycle's provider.(agentpkg.HookInstaller) type-assert succeeds
// for tests. Events returns the injected slice when non-nil (including the
// explicit empty slice for negative tests); a nil slice falls back to
// fakeDefaultEvents. The mutation methods are not exercised by handler tests;
// they exist purely to satisfy the interface.
func (f *fakeAgentProvider) Events() []agentpkg.HookEventSpec {
	if f.events == nil {
		return fakeDefaultEvents
	}
	return f.events
}
func (f *fakeAgentProvider) InstallHooks(string) error        { return nil }
func (f *fakeAgentProvider) RemoveHooks(string) error         { return nil }
func (f *fakeAgentProvider) CheckHooks() (agentpkg.HookStatus, error) {
	return agentpkg.HookStatus{}, nil
}
