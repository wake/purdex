package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/module/session"
	"github.com/wake/purdex/internal/tmux"
)

// #1293: the agent module's HTTP handlers read a session under the request's
// own context, so a client that gives up ends a stuck tmux read.

type reqCtxKey struct{}

// ctxSessions wraps a provider with GetSessionContext and records the context
// every call got, answering from the wrapped provider's GetSession — or, like
// the production read, failing once that context has ended.
type ctxSessions struct {
	session.SessionProvider
	mu  sync.Mutex
	got []context.Context
}

func (c *ctxSessions) GetSessionContext(ctx context.Context, code string) (*session.SessionInfo, error) {
	c.mu.Lock()
	c.got = append(c.got, ctx)
	c.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return c.SessionProvider.GetSession(code)
}

func (c *ctxSessions) contexts() []context.Context {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]context.Context(nil), c.got...)
}

func withRequestValue(req *http.Request) *http.Request {
	return req.WithContext(context.WithValue(req.Context(), reqCtxKey{}, "the request"))
}

func TestHandleUpload_SessionReadUsesRequestContext(t *testing.T) {
	m, _ := newUploadTestModule(t)
	cs := &ctxSessions{SessionProvider: m.sessions}
	m.sessions = cs

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	w.WriteField("session", "my-sess")
	fw, _ := w.CreateFormFile("file", "test.png")
	fw.Write([]byte("fake image data"))
	w.Close()
	req := httptest.NewRequest("POST", "/api/agent/upload", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()

	m.handleUpload(rec, withRequestValue(req))
	assert.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	got := cs.contexts()
	require.Len(t, got, 1, "the upload did not read the session through GetSessionContext")
	assert.Equal(t, "the request", got[0].Value(reqCtxKey{}), "the session read ran on a context other than the request's")
}

// #1293 (design): a hook event is processed to completion — a sender that
// hangs up mid-request must not leave it half-applied — so the hook handler's
// session reads run on their own bounded context, never r.Context(). With the
// request already cancelled, the path-hint cwd fallback still reads the
// session and the hint still carries its cwd.
func TestHandleEvent_PathHintCwdFallbackSurvivesCancelledRequest(t *testing.T) {
	m := newTestModule(t)
	fakeTmux := tmux.NewFakeExecutor()
	fakeTmux.SetPaneSessionName("%5", "work")
	m.tmux = fakeTmux
	cs := &ctxSessions{SessionProvider: &fakeSessionProvider{
		sessions: []session.SessionInfo{{Code: "code-work", Name: "work", Cwd: "/repo"}},
	}}
	m.sessions = cs
	m.core = &core.Core{Events: core.NewEventsBroadcaster(), Tmux: fakeTmux}
	m.registry.Register(&fakeAgentProvider{
		typeName: "cc",
		derive: func(event string, _ json.RawMessage) agentpkg.DeriveResult {
			return agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusRunning}
		},
	})
	sub := m.core.Events.AddTestSubscriber()
	defer m.core.Events.RemoveTestSubscriber(sub)

	// raw_event carries no cwd, so the hint's cwd can only come from the
	// session read.
	body := `{"tmux_session":"work","tmux_pane_id":"%5","sender_pid":200,"sender_start_time":"Sun Apr 20 01:30:00 2026","purdex_name":"PdxPreToolUse","raw_event":{"tool_name":"Read","tool_input":{"file_path":"/repo/src/a.go"}},"agent_type":"cc"}`
	req := withRequestValue(httptest.NewRequest("POST", "/api/agent/event", strings.NewReader(body)))
	req.Header.Set("Content-Type", "application/json")
	ctx, cancel := context.WithCancel(req.Context())
	cancel()
	w := httptest.NewRecorder()
	m.handleEvent(w, req.WithContext(ctx))
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	got := cs.contexts()
	require.NotEmpty(t, got, "the path-hint cwd fallback did not read the session through GetSessionContext")
	for _, c := range got {
		assert.Nil(t, c.Value(reqCtxKey{}), "a hook session read ran on the request's context")
		dl, ok := c.Deadline()
		if assert.True(t, ok, "a hook session read ran without a deadline") {
			assert.LessOrEqual(t, time.Until(dl), session.ListReadTimeout, "a hook session read got more than the session-list budget")
		}
	}
	assert.True(t, awaitPathHint(t, sub, "code-work", "/repo/src", "/repo", nil),
		"no path hint carrying the session's cwd — the fallback read failed")
}
