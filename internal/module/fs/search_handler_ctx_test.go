package fs

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/module/session"
	"github.com/wake/purdex/internal/store"
	"github.com/wake/purdex/internal/tmux"
)

// #1293: the session-cwd root is resolved under the search request's own
// context. Wired to the real session module over a tmux whose reads hang, a
// client that gives up ends the read at once — before, the lookup ran on a
// detached budget and held the handler for the full session-list timeout.
func TestHandleSearch_CancelledRequestEndsStuckSessionRead(t *testing.T) {
	meta, err := store.OpenMeta(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { meta.Close() })
	fake := tmux.NewFakeExecutor()
	fake.AddSession("dev", t.TempDir())
	sessions := session.NewSessionModule(meta)
	require.NoError(t, sessions.Init(core.New(core.CoreDeps{Tmux: fake, Registry: core.NewServiceRegistry()})))
	never := make(chan struct{})
	fake.SetReadHook(tmux.BlockReadsUntil(never, nil))

	m := newTestFsModule(t, sessions)
	code, err := session.EncodeSessionID("$0")
	require.NoError(t, err)
	body := httpSearchBodyT{
		Mode:  "basename",
		Query: map[string]string{"basename": "foo"},
		Roots: []map[string]any{{"kind": "session-cwd", "sessionCode": code}},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := newHTTPReq(t, body).WithContext(ctx)
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		m.handleSearch(w, req)
	}()

	time.Sleep(50 * time.Millisecond)
	cancelledAt := time.Now()
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("search did not return after its request was cancelled")
	}
	assert.Less(t, time.Since(cancelledAt), time.Second, "the session read kept running after the request was cancelled")
	assert.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
	assert.Contains(t, w.Body.String(), "session lookup failed")
}

// #1293: limits.timeoutMs is the whole search's budget — it bounds the
// session-cwd root lookup too, not only the walk. With the lookup stuck in
// tmux, a 50ms search answers in about 50ms (400 "session lookup failed", the
// handler's existing answer to a root it could not resolve) instead of waiting
// out the session-list timeout (5s) before its own deadline even starts.
func TestHandleSearch_TimeoutMsBoundsStuckSessionRead(t *testing.T) {
	meta, err := store.OpenMeta(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { meta.Close() })
	fake := tmux.NewFakeExecutor()
	fake.AddSession("dev", t.TempDir())
	sessions := session.NewSessionModule(meta)
	require.NoError(t, sessions.Init(core.New(core.CoreDeps{Tmux: fake, Registry: core.NewServiceRegistry()})))
	never := make(chan struct{})
	fake.SetReadHook(tmux.BlockReadsUntil(never, nil))

	m := newTestFsModule(t, sessions)
	code, err := session.EncodeSessionID("$0")
	require.NoError(t, err)
	const timeoutMs = 50
	body := httpSearchBodyT{
		Mode:   "basename",
		Query:  map[string]string{"basename": "foo"},
		Roots:  []map[string]any{{"kind": "session-cwd", "sessionCode": code}},
		Limits: map[string]int{"timeoutMs": timeoutMs},
	}
	req := newHTTPReq(t, body) // request context never ends on its own
	w := httptest.NewRecorder()
	done := make(chan struct{})
	start := time.Now()
	go func() {
		defer close(done)
		m.handleSearch(w, req)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("search did not return")
	}
	assert.Less(t, time.Since(start), timeoutMs*time.Millisecond+500*time.Millisecond,
		"the session-cwd root lookup ran outside limits.timeoutMs")
	assert.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
	assert.Contains(t, w.Body.String(), "session lookup failed")
}
