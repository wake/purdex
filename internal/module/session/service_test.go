package session

import (
	"context"
	"errors"
	"net/http/httptest"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/store"
	"github.com/wake/purdex/internal/tmux"
)

func TestBuildTerminalRelayArgs_Auto(t *testing.T) {
	args := buildTerminalRelayArgs("dev", "auto")
	assert.Equal(t, []string{"attach-session", "-t", "dev"}, args)
}

func TestBuildTerminalRelayArgs_TerminalFirst(t *testing.T) {
	args := buildTerminalRelayArgs("dev", "terminal-first")
	assert.Equal(t, []string{"attach-session", "-t", "dev", "-f", "ignore-size"}, args)
}

func TestBuildTerminalRelayArgs_MinimalFirst(t *testing.T) {
	args := buildTerminalRelayArgs("dev", "minimal-first")
	// minimal-first does NOT add ignore-size — sizing is handled via OnStart callback
	assert.Equal(t, []string{"attach-session", "-t", "dev"}, args)
}

func TestWindowSizeForMode(t *testing.T) {
	assert.Equal(t, "latest", windowSizeForMode("auto"))
	assert.Equal(t, "smallest", windowSizeForMode("minimal-first"))
	assert.Equal(t, "latest", windowSizeForMode("terminal-first"))
	assert.Equal(t, "latest", windowSizeForMode(""))
}

// TestHandleTerminalWS_NoConfigRace is a race regression test for issue #26.
// HandleTerminalWS used to read m.core.Cfg.Terminal.SizingMode without holding
// CfgMu, while handlePutConfig writes that field under CfgMu.Lock. Running this
// test with `go test -race` must not report a data race.
func TestHandleTerminalWS_NoConfigRace(t *testing.T) {
	meta, err := store.OpenMeta(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { meta.Close() })

	fake := tmux.NewFakeExecutor()
	fake.AddSession("test-session", "/tmp") // auto-assigns $0

	mod := NewSessionModule(meta)
	c := core.New(core.CoreDeps{
		Config: &config.Config{
			Terminal: config.TerminalConfig{SizingMode: "auto"},
		},
		Tmux:     fake,
		Registry: core.NewServiceRegistry(),
	})
	require.NoError(t, mod.Init(c))

	code, err := EncodeSessionID("$0")
	require.NoError(t, err)

	stop := make(chan struct{})
	var stopOnce sync.Once
	closeStop := func() { stopOnce.Do(func() { close(stop) }) }

	// Writer goroutine: continuously flips SizingMode under CfgMu.
	var writerWg sync.WaitGroup
	writerWg.Add(1)
	go func() {
		defer writerWg.Done()
		modes := []string{"auto", "terminal-first", "minimal-first"}
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			c.CfgMu.Lock()
			c.Cfg.Terminal.SizingMode = modes[i%len(modes)]
			c.CfgMu.Unlock()
			runtime.Gosched()
		}
	}()

	// Cleanup must run even if the test body panics, to prevent the writer
	// goroutine from leaking into subsequent tests in the same package run.
	t.Cleanup(func() {
		closeStop()
		writerWg.Wait()
	})

	// Reader goroutines: concurrently invoke HandleTerminalWS. The WS upgrade
	// will fail because httptest.ResponseRecorder is not a Hijacker, but the
	// read of Cfg.Terminal.SizingMode (the field protected by the fix) runs
	// before the upgrade attempt. Each goroutine loops several times so total
	// reader activity is large enough to keep race-detector firing reliable
	// on busy CI hardware.
	var readerWg sync.WaitGroup
	for i := 0; i < 50; i++ {
		readerWg.Add(1)
		go func() {
			defer readerWg.Done()
			for j := 0; j < 20; j++ {
				req := httptest.NewRequest("GET", "/ws/terminal/"+code, nil)
				rec := httptest.NewRecorder()
				mod.HandleTerminalWS(rec, req, code)
			}
		}()
	}

	readerWg.Wait()
	closeStop()
	writerWg.Wait()
}

// instanceOf adapts a context-free generation reader (FakeExecutor.Instance)
// to the tmuxInstanceFn seam.
func instanceOf(f func() string) func(context.Context) string {
	return func(context.Context) string { return f() }
}

// #1293 §3.2: a pane-metadata read ended by the list's context aborts the
// whole list with that error — it is not "skip the fields" — and no later
// session is read. The error names the step, so a regression that swallows
// it (and fails later, at the meta DB) is told apart.
func TestListSessionsContext_MetadataTimeoutAbortsList(t *testing.T) {
	mod, _, fake := newTestModule(t)
	mod.tmuxInstanceFn = func(context.Context) string { return "1:1" }
	for _, n := range []string{"a", "b", "c"} {
		fake.AddSession(n, "/tmp")
		fake.SetActivePaneMetadata(n, tmux.TmuxPaneMetadata{PaneTitle: n})
	}
	var mu sync.Mutex
	var asked []string
	never := make(chan struct{})
	block := tmux.BlockReadsUntil(never, func(op tmux.ReadOp, target string) bool {
		return op == tmux.ReadPaneMetadata && target == "b"
	})
	fake.SetReadHook(func(ctx context.Context, op tmux.ReadOp, target string) error {
		if op == tmux.ReadPaneMetadata {
			mu.Lock()
			asked = append(asked, target)
			mu.Unlock()
		}
		return block(ctx, op, target)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	list, err := mod.ListSessionsContext(ctx)

	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Nil(t, list, "a timed-out read never returns a partial list")
	assert.Contains(t, err.Error(), "pane metadata", "the error must come from the metadata step")
	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, []string{"a", "b"}, asked, "no session after the timed-out one may be read")
}

// A metadata error that is NOT the context ending keeps today's behaviour:
// the fields are skipped and the list succeeds.
func TestListSessionsContext_OtherMetadataErrorStillSkipsFields(t *testing.T) {
	mod, _, fake := newTestModule(t)
	mod.tmuxInstanceFn = func(context.Context) string { return "1:1" }
	fake.AddSession("a", "/tmp")
	fake.SetActivePaneMetadataError("a", errors.New("display-message failed"))

	list, err := mod.ListSessionsContext(context.Background())
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Empty(t, list[0].PaneTitle)
}

// The tmux-instance probe runs under the list's budget: it sees a deadline
// no later than the caller's, and never later than listReadTimeout.
func TestListSessionsContext_InstanceProbeRunsUnderListDeadline(t *testing.T) {
	mod, _, fake := newTestModule(t)
	fake.AddSession("a", "/tmp")
	fake.SetActivePaneMetadata("a", tmux.TmuxPaneMetadata{})
	var probeDeadline time.Time
	var hasDeadline bool
	mod.tmuxInstanceFn = func(ctx context.Context) string {
		probeDeadline, hasDeadline = ctx.Deadline()
		return "1:1"
	}

	before := time.Now()
	_, err := mod.ListSessionsContext(context.Background())
	require.NoError(t, err)
	require.True(t, hasDeadline, "an uncapped caller context must still be capped at listReadTimeout")
	assert.False(t, probeDeadline.After(before.Add(listReadTimeout).Add(50*time.Millisecond)))

	callerDeadline := time.Now().Add(time.Second)
	ctx, cancel := context.WithDeadline(context.Background(), callerDeadline)
	defer cancel()
	_, err = mod.ListSessionsContext(ctx)
	require.NoError(t, err)
	assert.False(t, probeDeadline.After(callerDeadline), "the probe must not outlive the caller's deadline")
}

// The budget covers the whole chain: a deadline hit in the instance probe
// (which reports failure as "" rather than an error) still fails the list,
// even when there is nothing after it to notice — an empty session list.
func TestListSessionsContext_TimeoutInInstanceProbeIsError(t *testing.T) {
	mod, _, _ := newTestModule(t)
	mod.tmuxInstanceFn = func(ctx context.Context) string {
		<-ctx.Done()
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	list, err := mod.ListSessionsContext(ctx)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Nil(t, list)
}

// The tmux list itself runs with the caller's context (a cancelled request
// ends the read).
func TestListSessionsContext_CancelledCallerEndsTmuxList(t *testing.T) {
	mod, _, fake := newTestModule(t)
	mod.tmuxInstanceFn = func(context.Context) string { return "1:1" }
	never := make(chan struct{})
	fake.SetReadHook(tmux.BlockReadsUntil(never, nil))
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)

	done := make(chan error, 1)
	go func() {
		_, err := mod.ListSessionsContext(ctx)
		done <- err
	}()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(2 * time.Second):
		t.Fatal("ListSessionsContext did not return after its context was cancelled")
	}
}

// listReturnsAtDeadline is a ReadHook whose tmux list only comes back once
// its context has ended — and then succeeds anyway, as a list-sessions that
// finished right at the deadline would.
func listReturnsAtDeadline(ctx context.Context, op tmux.ReadOp, _ string) error {
	if op == tmux.ReadListSessions {
		<-ctx.Done()
	}
	return nil
}

// #1293: a GetSession whose list came back past its deadline without the
// target must answer the deadline, not a reliable "not found" (nil, nil) —
// and must not act on that list by deleting the target's meta row.
func TestGetSession_ListAtDeadlineWithoutTargetIsCtxError(t *testing.T) {
	mod, meta, fake := newTestModule(t)
	mod.tmuxInstanceFn = func(context.Context) string { return "1:1" }
	fake.AddSessionWithID("$1", "other", "/tmp")
	require.NoError(t, meta.SetMeta("$9", store.SessionMeta{Mode: "terminal"}))
	code, err := EncodeSessionID("$9")
	require.NoError(t, err)
	fake.SetReadHook(listReturnsAtDeadline)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	info, err := mod.getSession(ctx, code)

	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Nil(t, info)
	got, err := meta.GetMeta("$9")
	require.NoError(t, err)
	assert.NotNil(t, got, "a list read past the deadline must not drive the orphan delete")
}

// The found path: a list that came back past the deadline drives no further
// reads (instance probe, pane metadata) and answers the deadline.
func TestGetSession_ListAtDeadlineWithTargetIsCtxError(t *testing.T) {
	mod, _, fake := newTestModule(t)
	var probes int
	mod.tmuxInstanceFn = func(context.Context) string { probes++; return "1:1" }
	fake.AddSessionWithID("$1", "a", "/tmp")
	fake.SetActivePaneMetadata("a", tmux.TmuxPaneMetadata{PaneTitle: "a"})
	code, err := EncodeSessionID("$1")
	require.NoError(t, err)
	var paneReads int
	fake.SetReadHook(func(ctx context.Context, op tmux.ReadOp, target string) error {
		if op == tmux.ReadPaneMetadata {
			paneReads++
		}
		return listReturnsAtDeadline(ctx, op, target)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	info, err := mod.getSession(ctx, code)

	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Nil(t, info)
	assert.Zero(t, probes, "no instance probe after the deadline")
	assert.Zero(t, paneReads, "no pane read after the deadline")
}

// A not-found answer whose orphan delete failed reports the failure.
func TestGetSession_NotFoundDeleteFailureIsError(t *testing.T) {
	mod, meta, fake := newTestModule(t)
	mod.tmuxInstanceFn = func(context.Context) string { return "1:1" }
	fake.AddSessionWithID("$1", "other", "/tmp")
	code, err := EncodeSessionID("$9")
	require.NoError(t, err)
	require.NoError(t, meta.Close())

	info, err := mod.GetSession(code)
	require.Error(t, err)
	assert.Nil(t, info)
}

// TmuxInstanceContext probes under the caller's context (the peers
// inventory's budget, #1293).
func TestTmuxInstanceContext_ProbesUnderCallerContext(t *testing.T) {
	mod, _, _ := newTestModule(t)
	type key struct{}
	var got any
	mod.tmuxInstanceFn = func(ctx context.Context) string { got = ctx.Value(key{}); return "1:1" }
	ctx := context.WithValue(context.Background(), key{}, "caller")
	assert.Equal(t, "1:1", mod.TmuxInstanceContext(ctx))
	assert.Equal(t, "caller", got)
}
