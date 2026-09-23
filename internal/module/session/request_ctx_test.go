package session

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wake/purdex/internal/tmux"
)

// A request's own context bounds the session reads it triggers (#1293): a
// client that gives up ends a stuck tmux read at once instead of leaving it to
// run out the full listReadTimeout.

// cancelledWell is how long after the cancellation a request may take to
// return — far below listReadTimeout, so a read still running on a detached
// context (the full 5 s) goes red.
const cancelledWell = time.Second

// serveCancelled serves req with a context cancelled cancelAfter into the
// request and returns the recorder and how long after the cancellation the
// handler returned. Fails the test if the handler outlives listReadTimeout.
func serveCancelled(t *testing.T, mux *http.ServeMux, req *http.Request, cancelAfter time.Duration) (*httptest.ResponseRecorder, time.Duration) {
	t.Helper()
	ctx, cancel := context.WithCancel(req.Context())
	defer cancel()
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		mux.ServeHTTP(w, req)
	}()
	time.Sleep(cancelAfter)
	cancelledAt := time.Now()
	cancel()
	select {
	case <-done:
	case <-time.After(listReadTimeout + time.Second):
		t.Fatalf("%s %s did not return after its request was cancelled", req.Method, req.URL.Path)
	}
	return w, time.Since(cancelledAt)
}

func TestHandlers_CancelledRequestEndsStuckSessionRead(t *testing.T) {
	cases := []struct {
		method, path, body string
	}{
		{http.MethodGet, "/api/sessions/%s", ""},
		{http.MethodGet, "/api/sessions/%s/home", ""},
		{http.MethodGet, "/api/sessions/%s/cwd", ""},
		{http.MethodPatch, "/api/sessions/%s", `{"name":"renamed"}`},
		{http.MethodDelete, "/api/sessions/%s", ""},
		{http.MethodPost, "/api/sessions/%s/send-keys", `{"keys":"ls"}`},
		{http.MethodGet, "/ws/terminal/%s", ""},
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			mod, _, fake := newTestModule(t)
			mod.tmuxInstanceFn = func(context.Context) string { return "1:1" }
			fake.AddSession("dev", "/tmp")
			code, err := EncodeSessionID("$0")
			require.NoError(t, err)
			never := make(chan struct{})
			fake.SetReadHook(tmux.BlockReadsUntil(never, nil))
			mux := http.NewServeMux()
			mod.RegisterRoutes(mux)

			path := strings.Replace(tc.path, "%s", code, 1)
			req := httptest.NewRequest(tc.method, path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			w, after := serveCancelled(t, mux, req, 50*time.Millisecond)

			assert.Less(t, after, cancelledWell, "handler kept reading %v after the request was cancelled", after)
			assert.Equal(t, http.StatusInternalServerError, w.Code, "body: %s", w.Body.String())
			// Nothing was acted on: the session is still there.
			assert.True(t, fake.HasSession("dev"))
		})
	}
}

// POST /api/sessions holds createMu across its tmux list. A cancelled request
// ends that read, returns, and releases the lock for the next create.
func TestHandleCreate_CancelledRequestReleasesCreateMu(t *testing.T) {
	mod, _, fake := newTestModule(t)
	mod.tmuxInstanceFn = func(context.Context) string { return "1:1" }
	never := make(chan struct{})
	fake.SetReadHook(tmux.BlockReadsUntil(never, nil))
	mux := http.NewServeMux()
	mod.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodPost, "/api/sessions",
		strings.NewReader(`{"name":"first","cwd":"`+t.TempDir()+`"}`))
	req.Header.Set("Content-Type", "application/json")
	w, after := serveCancelled(t, mux, req, 50*time.Millisecond)

	assert.Less(t, after, cancelledWell, "create kept reading %v after the request was cancelled", after)
	assert.Equal(t, http.StatusInternalServerError, w.Code, "body: %s", w.Body.String())
	require.True(t, mod.createMu.TryLock(), "createMu still held after the cancelled create returned")
	mod.createMu.Unlock()

	// The next create, with tmux answering again, goes through.
	fake.SetReadHook(nil)
	req = httptest.NewRequest(http.MethodPost, "/api/sessions",
		strings.NewReader(`{"name":"second","cwd":"`+t.TempDir()+`"}`))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, http.StatusCreated, w.Code, "body: %s", w.Body.String())
}

// Without a request of its own, a create is still bounded by listReadTimeout
// — and a caller context with a later deadline does not lift that cap.
func TestCreateSessionContext_CappedAtListReadTimeout(t *testing.T) {
	mod, _, fake := newTestModule(t)
	mod.tmuxInstanceFn = func(context.Context) string { return "1:1" }
	var gotDeadline time.Time
	fake.SetReadHook(func(ctx context.Context, op tmux.ReadOp, target string) error {
		gotDeadline, _ = ctx.Deadline()
		return nil
	})
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()
	_, err := mod.CreateSessionContext(ctx, "capped", t.TempDir())
	require.NoError(t, err)
	require.False(t, gotDeadline.IsZero(), "list read ran without a deadline")
	assert.WithinDuration(t, start.Add(listReadTimeout), gotDeadline, time.Second)
}

// The hook hot path's name lookup holds nameCacheMu across its tmux list; a
// cancelled caller ends the read and frees the lock.
func TestLookupCodeByNameContext_CancelEndsStuckRead(t *testing.T) {
	mod, _, fake := newTestModule(t)
	fake.AddSession("dev", "/tmp")
	never := make(chan struct{})
	fake.SetReadHook(tmux.BlockReadsUntil(never, nil))

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	start := time.Now()
	code, ok := mod.LookupCodeByNameContext(ctx, "dev")
	assert.False(t, ok)
	assert.Empty(t, code)
	assert.Less(t, time.Since(start), 50*time.Millisecond+cancelledWell)
	require.True(t, mod.nameCacheMu.TryLock(), "nameCacheMu still held after the cancelled lookup returned")
	mod.nameCacheMu.Unlock()

	fake.SetReadHook(nil)
	code, ok = mod.LookupCodeByNameContext(context.Background(), "dev")
	assert.True(t, ok)
	assert.NotEmpty(t, code)
}

// GetSessionContext caps a caller's later deadline at listReadTimeout.
func TestGetSessionContext_CappedAtListReadTimeout(t *testing.T) {
	mod, _, fake := newTestModule(t)
	mod.tmuxInstanceFn = func(context.Context) string { return "1:1" }
	fake.AddSession("dev", "/tmp")
	code, err := EncodeSessionID("$0")
	require.NoError(t, err)
	var gotDeadline time.Time
	fake.SetReadHook(func(ctx context.Context, op tmux.ReadOp, target string) error {
		if op == tmux.ReadListSessions {
			gotDeadline, _ = ctx.Deadline()
		}
		return nil
	})
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()
	info, err := mod.GetSessionContext(ctx, code)
	require.NoError(t, err)
	require.NotNil(t, info)
	require.False(t, gotDeadline.IsZero())
	assert.WithinDuration(t, start.Add(listReadTimeout), gotDeadline, time.Second)
}
