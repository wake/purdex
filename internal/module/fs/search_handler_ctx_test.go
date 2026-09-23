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
