package agent

import (
	"encoding/json"
	"net/http"

	agentpkg "github.com/wake/tmux-box/internal/agent"
	"github.com/wake/tmux-box/internal/module/session"
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

// fakeAgentProvider is a configurable AgentProvider for tests.
type fakeAgentProvider struct {
	typeName string
	alive    bool
	derive   func(eventName string, raw json.RawMessage) agentpkg.DeriveResult
}

func (f *fakeAgentProvider) Type() string                          { return f.typeName }
func (f *fakeAgentProvider) DisplayName() string                   { return f.typeName }
func (f *fakeAgentProvider) IconHint() string                      { return "" }
func (f *fakeAgentProvider) Claim(_ agentpkg.ClaimContext) bool    { return false }
func (f *fakeAgentProvider) IsAlive(string) bool                   { return f.alive }
func (f *fakeAgentProvider) DeriveStatus(eventName string, raw json.RawMessage) agentpkg.DeriveResult {
	if f.derive != nil {
		return f.derive(eventName, raw)
	}
	return agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusRunning}
}
