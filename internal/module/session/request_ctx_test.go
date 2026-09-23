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

// POST /api/sessions holds createMu across its post-create tmux list. Once
// `tmux new-session` has succeeded the session exists, so the rest of the
// create (list, meta write) runs to completion on its own bounded context
// even if the client goes away: abandoning it would leave a tmux session with
// no meta row. The cancelled request therefore does not end the stuck read;
// the create finishes as soon as tmux answers, records its meta and releases
// createMu for the next create. (Before this, the test asserted the opposite
// — that cancelling ended the read with a 500 — which is exactly the
// half-created state this change removes.)
func TestHandleCreate_CancelledRequestReleasesCreateMu(t *testing.T) {
	mod, meta, fake := newTestModule(t)
	mod.tmuxInstanceFn = func(context.Context) string { return "1:1" }
	release := make(chan struct{})
	entered := make(chan struct{}, 1)
	block := tmux.BlockReadsUntil(release, nil)
	fake.SetReadHook(func(ctx context.Context, op tmux.ReadOp, target string) error {
		select {
		case entered <- struct{}{}:
		default:
		}
		return block(ctx, op, target)
	})
	mux := http.NewServeMux()
	mod.RegisterRoutes(mux)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest(http.MethodPost, "/api/sessions",
		strings.NewReader(`{"name":"first","cwd":"`+t.TempDir()+`"}`)).WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		mux.ServeHTTP(w, req)
	}()

	<-entered // new-session done, the post-create list is stuck
	cancel()
	select {
	case <-done:
		t.Fatalf("create returned after its request was cancelled mid-list (status %d): %s", w.Code, w.Body.String())
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	select {
	case <-done:
	case <-time.After(listReadTimeout + time.Second):
		t.Fatal("create did not finish once tmux answered")
	}

	assert.Equal(t, http.StatusCreated, w.Code, "body: %s", w.Body.String())
	metas, err := meta.ListMeta()
	require.NoError(t, err)
	assert.Len(t, metas, 1, "the created session has no meta row")
	require.True(t, mod.createMu.TryLock(), "createMu still held after the create returned")
	mod.createMu.Unlock()

	// The next create goes through.
	fake.SetReadHook(nil)
	req = httptest.NewRequest(http.MethodPost, "/api/sessions",
		strings.NewReader(`{"name":"second","cwd":"`+t.TempDir()+`"}`))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, http.StatusCreated, w.Code, "body: %s", w.Body.String())
}

// A caller that cancels after `tmux new-session` succeeded still gets the
// whole create: the session, its meta row, and createMu released.
func TestCreateSessionContext_CancelAfterNewSessionCompletes(t *testing.T) {
	mod, meta, fake := newTestModule(t)
	mod.tmuxInstanceFn = func(context.Context) string { return "1:1" }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Cancel the caller at the moment the post-create read starts, then let
	// the read answer a little later — on a context that is still live only
	// if the create detached from the caller.
	fake.SetReadHook(func(rctx context.Context, op tmux.ReadOp, target string) error {
		cancel()
		select {
		case <-time.After(50 * time.Millisecond):
			return nil
		case <-rctx.Done():
			return rctx.Err()
		}
	})

	info, err := mod.CreateSessionContext(ctx, "late", t.TempDir())
	require.NoError(t, err)
	require.NotNil(t, info)
	assert.True(t, fake.HasSession("late"))
	m, err := meta.GetMeta(info.TmuxID)
	require.NoError(t, err)
	require.NotNil(t, m, "created session has no meta row")
	assert.Equal(t, "terminal", m.Mode)
	require.True(t, mod.createMu.TryLock(), "createMu still held after the create returned")
	mod.createMu.Unlock()
}

// A caller that cancels while waiting for createMu returns at once and never
// reaches `tmux new-session`.
func TestCreateSessionContext_CancelWhileWaitingForCreateMu(t *testing.T) {
	mod, meta, fake := newTestModule(t)
	mod.tmuxInstanceFn = func(context.Context) string { return "1:1" }
	require.True(t, mod.createMu.TryLock())
	defer mod.createMu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	start := time.Now()
	info, err := mod.CreateSessionContext(ctx, "waiting", t.TempDir())
	assert.Less(t, time.Since(start), 50*time.Millisecond+cancelledWell, "create kept waiting for createMu after its caller cancelled")
	require.Error(t, err)
	assert.Nil(t, info)
	var ce *CreateError
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, CreateStageCancelled, ce.Stage)
	assert.False(t, ce.SessionAlive())
	assert.ErrorIs(t, err, context.Canceled)
	assert.False(t, fake.HasSession("waiting"), "a cancelled create reached tmux new-session")
	metas, err := meta.ListMeta()
	require.NoError(t, err)
	assert.Empty(t, metas)
}

// An already-cancelled caller that finds createMu free still does not create.
func TestCreateSessionContext_AlreadyCancelledCreatesNothing(t *testing.T) {
	mod, _, fake := newTestModule(t)
	mod.tmuxInstanceFn = func(context.Context) string { return "1:1" }
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := mod.CreateSessionContext(ctx, "never", t.TempDir())
	var ce *CreateError
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, CreateStageCancelled, ce.Stage)
	assert.False(t, fake.HasSession("never"))
	require.True(t, mod.createMu.TryLock(), "createMu held after a cancelled create")
	mod.createMu.Unlock()
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

// GetSession, CreateSession (its post-create read) and the name lookup take
// their budget from m.readTimeout(), so a test override applies to them like
// to every other session-list read.
func TestReadTimeoutOverride_GetCreateLookup(t *testing.T) {
	const short = 150 * time.Millisecond
	cases := map[string]func(t *testing.T, mod *SessionModule){
		"GetSession": func(t *testing.T, mod *SessionModule) {
			code, err := EncodeSessionID("$0")
			require.NoError(t, err)
			_, _ = mod.GetSessionContext(context.Background(), code)
		},
		"CreateSession": func(t *testing.T, mod *SessionModule) {
			_, _ = mod.CreateSessionContext(context.Background(), "fresh", t.TempDir())
		},
		"LookupCodeByName": func(t *testing.T, mod *SessionModule) {
			_, _ = mod.LookupCodeByNameContext(context.Background(), "dev")
		},
	}
	for name, run := range cases {
		t.Run(name, func(t *testing.T) {
			mod, _, fake := newTestModule(t)
			mod.tmuxInstanceFn = func(context.Context) string { return "1:1" }
			mod.listTimeout = short
			fake.AddSession("dev", "/tmp")
			var gotDeadline time.Time
			fake.SetReadHook(func(ctx context.Context, op tmux.ReadOp, target string) error {
				if op == tmux.ReadListSessions && gotDeadline.IsZero() {
					gotDeadline, _ = ctx.Deadline()
				}
				return nil
			})
			start := time.Now()
			run(t, mod)
			require.False(t, gotDeadline.IsZero(), "list read ran without a deadline")
			assert.WithinDuration(t, start.Add(short), gotDeadline, 100*time.Millisecond)
		})
	}
}
