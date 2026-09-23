package session

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wake/purdex/internal/tmux"
)

// #1293 codex R1-P1: every tmux call CreateSessionContext makes is bounded.
// The pre-create reads (has-session, the generation probe) follow the caller;
// new-session runs on a cap of its own, never the caller's cancellation.

// createBound is how long a create stuck in `steps` bounded tmux steps may
// take to return: the module's cap per step plus scheduling slack.
func createBound(mod *SessionModule, steps int) time.Duration {
	return time.Duration(steps)*mod.readTimeout() + time.Second
}

// shortCap shortens m.readTimeout() for the tests that wait a cap out.
const shortCap = 300 * time.Millisecond

// runCreate runs CreateSessionContext on its own goroutine and fails the test
// if it outlives bound.
func runCreate(t *testing.T, mod *SessionModule, ctx context.Context, name string, bound time.Duration) (*SessionInfo, error) {
	t.Helper()
	type result struct {
		info *SessionInfo
		err  error
	}
	done := make(chan result, 1)
	cwd := t.TempDir()
	go func() {
		info, err := mod.CreateSessionContext(ctx, name, cwd)
		done <- result{info, err}
	}()
	select {
	case r := <-done:
		return r.info, r.err
	case <-time.After(bound):
		t.Fatalf("create of %q did not return within %v", name, bound)
		return nil, nil
	}
}

func requireCreateMuFree(t *testing.T, mod *SessionModule) {
	t.Helper()
	require.True(t, mod.createMu.TryLock(), "createMu still held after the create returned")
	mod.createMu.Unlock()
}

// newSessionCalls counts the new-session calls that reached the fake.
func countNewSession(fake *tmux.FakeExecutor, next tmux.ReadHook) *atomic.Int32 {
	var n atomic.Int32
	fake.SetCreateHook(func(ctx context.Context, op tmux.ReadOp, target string) error {
		if op == tmux.OpNewSession {
			n.Add(1)
		}
		if next != nil {
			return next(ctx, op, target)
		}
		return nil
	})
	return &n
}

// A caller that goes away while the generation probe runs creates nothing —
// even when the probe itself still answers.
func TestCreateSessionContext_CancelDuringProbeCreatesNothing(t *testing.T) {
	mod, meta, fake := newTestModule(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mod.tmuxInstanceFn = func(context.Context) string {
		cancel()
		return "1:1"
	}
	newSessions := countNewSession(fake, nil)

	info, err := mod.CreateSessionContext(ctx, "probed", t.TempDir())
	assert.Nil(t, info)
	var ce *CreateError
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, CreateStageCancelled, ce.Stage)
	assert.False(t, ce.SessionAlive())
	assert.ErrorIs(t, err, context.Canceled)
	assert.Zero(t, newSessions.Load(), "tmux new-session ran for a caller that had gone")
	assert.False(t, fake.HasSession("probed"))
	metas, err := meta.ListMeta()
	require.NoError(t, err)
	assert.Empty(t, metas)
	requireCreateMuFree(t, mod)
}

// A hung has-session ends when the caller does.
func TestCreateSessionContext_CancelDuringHasSessionEndsIt(t *testing.T) {
	mod, _, fake := newTestModule(t)
	mod.tmuxInstanceFn = func(context.Context) string { return "1:1" }
	never := make(chan struct{})
	newSessions := countNewSession(fake, tmux.BlockReadsUntil(never, func(op tmux.ReadOp, _ string) bool {
		return op == tmux.OpHasSession
	}))
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	start := time.Now()

	_, err := runCreate(t, mod, ctx, "stuck", createBound(mod, 1))
	assert.Less(t, time.Since(start), 50*time.Millisecond+cancelledWell, "has-session kept running after the caller cancelled")
	var ce *CreateError
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, CreateStageCancelled, ce.Stage)
	assert.ErrorIs(t, err, context.Canceled)
	assert.Zero(t, newSessions.Load())
	requireCreateMuFree(t, mod)
}

// The pre-create reads are capped even for a caller with no deadline of its
// own (CreateSession passes context.Background()).
func TestCreateSessionContext_PreCreateReadsCapped(t *testing.T) {
	mod, _, fake := newTestModule(t)
	var hasDeadline, probeDeadline time.Time
	mod.tmuxInstanceFn = func(ctx context.Context) string {
		if probeDeadline.IsZero() {
			probeDeadline, _ = ctx.Deadline()
		}
		return "1:1"
	}
	fake.SetCreateHook(func(ctx context.Context, op tmux.ReadOp, _ string) error {
		if op == tmux.OpHasSession {
			hasDeadline, _ = ctx.Deadline()
		}
		return nil
	})
	start := time.Now()
	_, err := mod.CreateSession("capped", t.TempDir())
	require.NoError(t, err)
	require.False(t, hasDeadline.IsZero(), "has-session ran without a deadline")
	require.False(t, probeDeadline.IsZero(), "the pre-create probe ran without a deadline")
	assert.WithinDuration(t, start.Add(listReadTimeout), hasDeadline, time.Second)
	assert.WithinDuration(t, start.Add(listReadTimeout), probeDeadline, time.Second)
}

// new-session is not bound to the caller: a caller that cancels while it runs
// does not kill it (the server may already have made the session), and the
// create completes.
func TestCreateSessionContext_CancelDuringNewSessionDoesNotAbortIt(t *testing.T) {
	mod, meta, fake := newTestModule(t)
	mod.tmuxInstanceFn = func(context.Context) string { return "1:1" }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fake.SetCreateHook(func(nctx context.Context, op tmux.ReadOp, _ string) error {
		if op != tmux.OpNewSession {
			return nil
		}
		cancel()
		select {
		case <-time.After(50 * time.Millisecond):
			return nil
		case <-nctx.Done():
			return nctx.Err()
		}
	})

	info, err := mod.CreateSessionContext(ctx, "detached", t.TempDir())
	require.NoError(t, err)
	require.NotNil(t, info)
	m, err := meta.GetMeta(info.TmuxID)
	require.NoError(t, err)
	require.NotNil(t, m)
	requireCreateMuFree(t, mod)
}

// A new-session that never answers is killed at its own cap; nothing was
// created, createMu comes free and the next create goes through.
func TestCreateSessionContext_HungNewSessionReleasesCreateMu(t *testing.T) {
	t.Parallel()
	mod, _, fake := newTestModule(t)
	mod.listTimeout = shortCap
	mod.tmuxInstanceFn = func(context.Context) string { return "1:1" }
	var newDeadline atomic.Value
	fake.SetCreateHook(func(ctx context.Context, op tmux.ReadOp, _ string) error {
		if op != tmux.OpNewSession {
			return nil
		}
		d, _ := ctx.Deadline()
		newDeadline.Store(d)
		<-ctx.Done()
		return ctx.Err()
	})

	start := time.Now()
	info, err := runCreate(t, mod, context.Background(), "hung", createBound(mod, 2))
	assert.Nil(t, info)
	var ce *CreateError
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, CreateStageNewSession, ce.Stage, "has-session confirmed nothing exists: %v", err)
	assert.False(t, ce.SessionAlive())
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	d, _ := newDeadline.Load().(time.Time)
	require.False(t, d.IsZero(), "new-session ran without a deadline")
	assert.WithinDuration(t, start.Add(shortCap), d, 150*time.Millisecond)
	requireCreateMuFree(t, mod)

	fake.SetCreateHook(nil)
	_, err = mod.CreateSession("next", t.TempDir())
	require.NoError(t, err)
}

// A new-session that timed out after the server made the session: the
// follow-up has-session finds it, so the create finishes it (list, meta) like
// any other.
func TestCreateSessionContext_NewSessionTimedOutButCreated(t *testing.T) {
	t.Parallel()
	mod, meta, fake := newTestModule(t)
	mod.listTimeout = shortCap
	mod.tmuxInstanceFn = func(context.Context) string { return "1:1" }
	fake.SetCreateHook(func(ctx context.Context, op tmux.ReadOp, target string) error {
		if op != tmux.OpNewSession {
			return nil
		}
		_ = fake.NewSession(target, "/tmp") // the server made it...
		<-ctx.Done()                        // ...and the client never answered
		return ctx.Err()
	})

	info, err := runCreate(t, mod, context.Background(), "late-answer", createBound(mod, 2))
	require.NoError(t, err)
	require.NotNil(t, info)
	assert.Equal(t, "late-answer", info.Name)
	m, err := meta.GetMeta(info.TmuxID)
	require.NoError(t, err)
	require.NotNil(t, m, "the session the timed-out new-session made has no meta row")
	requireCreateMuFree(t, mod)
}

// A new-session that timed out and a follow-up has-session that cannot answer
// either: whether the session exists is unknown, and the error does not
// claim it does — SessionAlive is false; a refreshed list shows it if it is.
func TestCreateSessionContext_NewSessionTimedOutUnconfirmed(t *testing.T) {
	t.Parallel()
	mod, meta, fake := newTestModule(t)
	mod.listTimeout = shortCap
	mod.tmuxInstanceFn = func(context.Context) string { return "1:1" }
	var hasCalls atomic.Int32
	fake.SetCreateHook(func(ctx context.Context, op tmux.ReadOp, _ string) error {
		if op == tmux.OpHasSession && hasCalls.Add(1) == 1 {
			return nil // the pre-create check answers
		}
		<-ctx.Done()
		return ctx.Err()
	})

	info, err := runCreate(t, mod, context.Background(), "unknown", createBound(mod, 3))
	assert.Nil(t, info)
	var ce *CreateError
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, CreateStageNewSessionUnconfirmed, ce.Stage)
	assert.False(t, ce.SessionAlive(), "unknown is not announced as alive (exec-to-terminal spec: session_alive means it exists)")
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	metas, err := meta.ListMeta()
	require.NoError(t, err)
	assert.Empty(t, metas)
	requireCreateMuFree(t, mod)
}

// #1293 codex R1-P2: the post-create chain (list-sessions, generation probe,
// meta write) runs under ONE deadline. The probe used to open a fresh
// Background budget of its own, so a list that used up most of the cap
// followed by a hung probe held createMu past it.

// The post-create probe runs under the same deadline as the post-create list.
func TestCreateSessionContext_PostCreateProbeSharesListDeadline(t *testing.T) {
	mod, _, fake := newTestModule(t)
	var listDeadline, postProbeDeadline time.Time
	var probes atomic.Int32
	mod.tmuxInstanceFn = func(ctx context.Context) string {
		if probes.Add(1) == 2 {
			postProbeDeadline, _ = ctx.Deadline()
		}
		return "1:1"
	}
	fake.SetReadHook(func(ctx context.Context, op tmux.ReadOp, _ string) error {
		if op == tmux.ReadListSessions {
			listDeadline, _ = ctx.Deadline()
		}
		return nil
	})
	_, err := mod.CreateSession("one-deadline", t.TempDir())
	require.NoError(t, err)
	require.EqualValues(t, 2, probes.Load(), "want the pre- and post-create probes")
	require.False(t, listDeadline.IsZero())
	require.False(t, postProbeDeadline.IsZero(), "the post-create probe ran without a deadline")
	assert.Equal(t, listDeadline, postProbeDeadline, "the post-create probe runs on a budget of its own")
}

// A list that eats nearly the whole budget, then a probe that hangs: the
// chain still ends at the one deadline. The session exists, so the failure is
// list-stage (SessionAlive) — not a generation change read off an empty probe.
func TestCreateSessionContext_HungPostCreateProbeEndsAtChainDeadline(t *testing.T) {
	t.Parallel()
	mod, meta, fake := newTestModule(t)
	mod.listTimeout = shortCap
	var probes atomic.Int32
	mod.tmuxInstanceFn = func(ctx context.Context) string {
		if probes.Add(1) == 1 {
			return "1:1" // pre-create sample
		}
		<-ctx.Done()
		return ""
	}
	var chainStart atomic.Value
	fake.SetReadHook(func(ctx context.Context, op tmux.ReadOp, _ string) error {
		chainStart.Store(time.Now())
		d, _ := ctx.Deadline()
		select {
		case <-time.After(time.Until(d) * 3 / 4):
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})

	info, err := runCreate(t, mod, context.Background(), "probe-hangs", createBound(mod, 2))
	start, _ := chainStart.Load().(time.Time)
	require.False(t, start.IsZero())
	assert.Less(t, time.Since(start), shortCap+200*time.Millisecond, "the post-create chain outlived its one deadline")
	assert.Nil(t, info)
	var ce *CreateError
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, CreateStageList, ce.Stage)
	assert.True(t, ce.SessionAlive())
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	metas, err := meta.ListMeta()
	require.NoError(t, err)
	assert.Empty(t, metas)
	requireCreateMuFree(t, mod)
}

// Every cap a create sets — the pre-create reads, new-session's own, the
// post-create chain's — comes from m.readTimeout(), so a test override
// reaches all of them.
func TestReadTimeoutOverride_CreateCaps(t *testing.T) {
	mod, _, fake := newTestModule(t)
	mod.listTimeout = shortCap
	var mu sync.Mutex
	deadlines := map[string]time.Time{}
	record := func(step string, ctx context.Context) {
		d, _ := ctx.Deadline()
		mu.Lock()
		defer mu.Unlock()
		if _, ok := deadlines[step]; !ok {
			deadlines[step] = d
		}
	}
	var probes atomic.Int32
	mod.tmuxInstanceFn = func(ctx context.Context) string {
		if probes.Add(1) == 1 {
			record("pre-create probe", ctx)
		}
		return "1:1"
	}
	fake.SetCreateHook(func(ctx context.Context, op tmux.ReadOp, _ string) error {
		record(string(op), ctx)
		return nil
	})
	fake.SetReadHook(func(ctx context.Context, op tmux.ReadOp, _ string) error {
		record(string(op), ctx)
		return nil
	})
	start := time.Now()
	_, err := mod.CreateSession("override", t.TempDir())
	require.NoError(t, err)
	for _, step := range []string{string(tmux.OpHasSession), "pre-create probe", string(tmux.OpNewSession), string(tmux.ReadListSessions)} {
		d := deadlines[step]
		require.False(t, d.IsZero(), "%s ran without a deadline", step)
		assert.WithinDuration(t, start.Add(shortCap), d, 150*time.Millisecond, "%s is not capped by m.readTimeout()", step)
	}
}
