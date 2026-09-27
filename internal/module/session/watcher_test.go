package session

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/store"
	"github.com/wake/purdex/internal/tmux"
)

func newWatcherTestModule(t *testing.T) (*SessionModule, *tmux.FakeExecutor, *core.EventsBroadcaster) {
	t.Helper()
	meta, err := store.OpenMeta(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { meta.Close() })

	fake := tmux.NewFakeExecutor()
	mod := NewSessionModule(meta)
	c := core.New(core.CoreDeps{
		Tmux:     fake,
		Registry: core.NewServiceRegistry(),
	})
	require.NoError(t, mod.Init(c))
	return mod, fake, c.Events
}

func TestWatcherTmuxAliveInitialState(t *testing.T) {
	mod, _, _ := newWatcherTestModule(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, mod.Start(ctx))
	assert.True(t, mod.TmuxAlive(), "tmux should be alive when FakeExecutor default alive=true")
}

// A broken tmux (not merely no server, #1474) is the one down edge the SPA
// hears about.
func TestWatcherTransitionsToTmuxDown(t *testing.T) {
	mod, fake, events := newWatcherTestModule(t)
	sub := events.AddTestSubscriber()
	defer events.RemoveTestSubscriber(sub)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, mod.Start(ctx))

	fake.SetServerState(tmux.ServerBroken)
	mod.checkAndBroadcast()
	assert.False(t, mod.TmuxAlive())

	select {
	case msg := <-sub.SendCh():
		assert.Contains(t, string(msg), `"type":"tmux"`)
		assert.Contains(t, string(msg), `"value":"unavailable"`)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected tmux unavailable broadcast")
	}
}

func TestWatcherRecoverFromTmuxDown(t *testing.T) {
	mod, fake, events := newWatcherTestModule(t)
	sub := events.AddTestSubscriber()
	defer events.RemoveTestSubscriber(sub)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, mod.Start(ctx))

	fake.SetServerState(tmux.ServerBroken)
	mod.checkAndBroadcast()
	assert.False(t, mod.TmuxAlive())
	<-sub.SendCh()

	fake.SetAlive(true)
	fake.AddSession("recovered", "/tmp")
	mod.checkAndBroadcast()
	assert.True(t, mod.TmuxAlive())

	select {
	case msg := <-sub.SendCh():
		assert.Contains(t, string(msg), `"type":"tmux"`)
		assert.Contains(t, string(msg), `"value":"ok"`)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected tmux ok broadcast")
	}
}

func TestWatcherNilSessionsWithTmuxAlive(t *testing.T) {
	mod, fake, events := newWatcherTestModule(t)
	sub := events.AddTestSubscriber()
	defer events.RemoveTestSubscriber(sub)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fake.SetAlive(true)
	require.NoError(t, mod.Start(ctx))

	mod.checkAndBroadcast()
	assert.True(t, mod.TmuxAlive())
}

// TestBroadcastSessionsDebounce verifies that rapid concurrent calls to
// broadcastSessions() within the 500ms window result in only one broadcast.
func TestBroadcastSessionsDebounce(t *testing.T) {
	mod, fake, events := newWatcherTestModule(t)
	sub := events.AddTestSubscriber()
	defer events.RemoveTestSubscriber(sub)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fake.AddSession("s1", "/tmp")
	require.NoError(t, mod.Start(ctx))

	// Call broadcastSessions twice back-to-back within the debounce window.
	mod.broadcastSessions()
	mod.broadcastSessions()

	// Only one broadcast should have been sent.
	count := 0
	timeout := time.After(100 * time.Millisecond)
drain:
	for {
		select {
		case msg := <-sub.SendCh():
			if len(msg) > 0 {
				count++
			}
		case <-timeout:
			break drain
		}
	}
	assert.Equal(t, 1, count, "debounce should suppress second broadcast within 500ms window")
}

// TestBroadcastSessionsDebounceExpiry verifies that a second call after the
// debounce window has passed DOES produce a broadcast.
func TestBroadcastSessionsDebounceExpiry(t *testing.T) {
	mod, fake, events := newWatcherTestModule(t)
	sub := events.AddTestSubscriber()
	defer events.RemoveTestSubscriber(sub)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fake.AddSession("s1", "/tmp")
	require.NoError(t, mod.Start(ctx))

	// First call sets the lastBroadcast timestamp.
	mod.broadcastSessions()

	// Drain first broadcast.
	select {
	case <-sub.SendCh():
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected first broadcast")
	}

	// Manually expire the debounce window by backdating lastBroadcast.
	mod.wstate.mu.Lock()
	mod.wstate.lastBroadcast = mod.wstate.lastBroadcast.Add(-600 * time.Millisecond)
	mod.wstate.mu.Unlock()

	// Second call after window expiry should go through.
	mod.broadcastSessions()

	select {
	case msg := <-sub.SendCh():
		assert.Contains(t, string(msg), `"type":"sessions"`, "second broadcast should contain sessions event")
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected second broadcast after debounce expiry")
	}
}

func TestWatcherNoRepeatBroadcastInTmuxDown(t *testing.T) {
	mod, fake, events := newWatcherTestModule(t)
	sub := events.AddTestSubscriber()
	defer events.RemoveTestSubscriber(sub)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, mod.Start(ctx))

	fake.SetServerState(tmux.ServerBroken)
	mod.checkAndBroadcast()
	<-sub.SendCh()

	mod.checkAndBroadcast()

	select {
	case <-sub.SendCh():
		t.Fatal("should not broadcast tmux unavailable twice in a row")
	case <-time.After(50 * time.Millisecond):
	}
}

// TestTickNormal_InvalidatesNameCacheOnHashChange verifies that when the
// watcher's polling loop detects a session-list change (hash diff), it
// invalidates the LookupCodeByName cache. This is the safety net for the
// case where an external `tmux rename-session` mutates state without going
// through the daemon's HTTP handlers.
func TestTickNormal_InvalidatesNameCacheOnHashChange(t *testing.T) {
	mod, fake, _ := newWatcherTestModule(t)

	fake.AddSession("alpha", "/tmp")

	// Prime tickNormal so its lastHash matches the current session list,
	// otherwise the very first tick we run below would already see a hash
	// change from "" → some-hash and invalidate, masking the real assertion.
	mod.tickNormal()

	// Pre-populate the name cache.
	_, ok := mod.LookupCodeByName("alpha")
	require.True(t, ok)
	mod.nameCacheMu.Lock()
	require.False(t, mod.nameCacheAt.IsZero(), "cache must be populated before the test")
	mod.nameCacheMu.Unlock()

	// Mutate session list externally so the next tickNormal sees a new hash.
	fake.AddSession("beta", "/tmp")

	mod.tickNormal()

	mod.nameCacheMu.Lock()
	defer mod.nameCacheMu.Unlock()
	assert.True(t, mod.nameCacheAt.IsZero(), "tickNormal must invalidate name cache when hash changes")
}

// TestTickNormal_DoesNotInvalidateOnUnchangedHash verifies that the steady
// state (no session changes) does not pointlessly bust the cache.
func TestTickNormal_DoesNotInvalidateOnUnchangedHash(t *testing.T) {
	mod, fake, _ := newWatcherTestModule(t)

	fake.AddSession("alpha", "/tmp")

	// First tick to register the current hash.
	mod.tickNormal()

	// Pre-populate the name cache.
	_, ok := mod.LookupCodeByName("alpha")
	require.True(t, ok)
	mod.nameCacheMu.Lock()
	require.False(t, mod.nameCacheAt.IsZero(), "cache must be populated before the test")
	mod.nameCacheMu.Unlock()

	// No mutation: the next tick must see an identical hash and not invalidate.
	mod.tickNormal()

	mod.nameCacheMu.Lock()
	defer mod.nameCacheMu.Unlock()
	assert.False(t, mod.nameCacheAt.IsZero(), "tickNormal must not invalidate name cache when hash is unchanged")
}

// TestBroadcastSessions_InvalidatesNameCache verifies that the wait-for path
// (which lands in broadcastSessions) invalidates the name cache. This is the
// authoritative signal — tmux just told us "something changed".
func TestBroadcastSessions_InvalidatesNameCache(t *testing.T) {
	mod, fake, events := newWatcherTestModule(t)
	sub := events.AddTestSubscriber()
	defer events.RemoveTestSubscriber(sub)

	fake.AddSession("alpha", "/tmp")

	// Pre-populate the name cache.
	_, ok := mod.LookupCodeByName("alpha")
	require.True(t, ok)
	mod.nameCacheMu.Lock()
	require.False(t, mod.nameCacheAt.IsZero(), "cache must be populated before the test")
	mod.nameCacheMu.Unlock()

	mod.broadcastSessions()

	mod.nameCacheMu.Lock()
	defer mod.nameCacheMu.Unlock()
	assert.True(t, mod.nameCacheAt.IsZero(), "broadcastSessions must invalidate name cache")
}

func TestHashSessionsChangesWhenPaneTitleChanges(t *testing.T) {
	base := []SessionInfo{{
		Code:      "aa",
		TmuxID:    "$0",
		Name:      "dev",
		Exists:    true,
		Mode:      "terminal",
		Cwd:       "/tmp",
		PaneTitle: "first title",
	}}
	changed := []SessionInfo{{
		Code:      "aa",
		TmuxID:    "$0",
		Name:      "dev",
		Exists:    true,
		Mode:      "terminal",
		Cwd:       "/tmp",
		PaneTitle: "second title",
	}}

	assert.NotEqual(t, hashSessions("i", base), hashSessions("i", changed))
}

// drainSessions collects the inner JSON payload of every "sessions" event the
// subscriber received. Frames on SendCh() are the outer core.HostEvent
// envelope, so the type is checked before the value is kept.
func drainSessions(t *testing.T, sub *core.EventSubscriber) []string {
	t.Helper()
	var out []string
	timeout := time.After(100 * time.Millisecond)
	for {
		select {
		case msg := <-sub.SendCh():
			var env struct {
				Type  string `json:"type"`
				Value string `json:"value"`
			}
			if err := json.Unmarshal(msg, &env); err != nil || env.Type != "sessions" {
				continue
			}
			out = append(out, env.Value)
		case <-timeout:
			return out
		}
	}
}

func TestTickNormal_TmuxRestartWithIdenticalList_Broadcasts(t *testing.T) {
	mod, fake, events := newWatcherTestModule(t)
	sub := events.AddTestSubscriber()
	defer events.RemoveTestSubscriber(sub)

	fake.AddSession("dev", "/w")
	mod.tmuxInstanceFn = func(context.Context) string { return "111:1000" }
	mod.tickNormal()
	require.Len(t, drainSessions(t, sub), 1, "first tick must broadcast")

	// Same session list, new tmux server.
	mod.tmuxInstanceFn = func(context.Context) string { return "222:2000" }
	mod.tickNormal()
	got := drainSessions(t, sub)
	require.Len(t, got, 1, "restart with an identical list must still broadcast")
	assert.Contains(t, got[0], `"tmux_instance":"222:2000"`)
}

func TestTickNormal_UnchangedInstanceAndList_DoesNotBroadcast(t *testing.T) {
	mod, fake, events := newWatcherTestModule(t)
	sub := events.AddTestSubscriber()
	defer events.RemoveTestSubscriber(sub)

	fake.AddSession("dev", "/w")
	mod.tmuxInstanceFn = func(context.Context) string { return "111:1000" }
	mod.tickNormal()
	drainSessions(t, sub)

	mod.tickNormal()
	assert.Empty(t, drainSessions(t, sub), "unchanged state must not broadcast")
}

func TestListSessions_SamplesInstanceOutsideTheTick(t *testing.T) {
	// A restart between two ticks must not be reported with the previous
	// generation by the list path.
	mod, fake, _ := newWatcherTestModule(t)
	fake.AddSession("dev", "/w")
	mod.tmuxInstanceFn = func(context.Context) string { return "111:1000" }
	mod.tickNormal()

	mod.tmuxInstanceFn = func(context.Context) string { return "222:2000" }
	sessions, err := mod.ListSessions()
	require.NoError(t, err)
	require.NotEmpty(t, sessions)
	assert.Equal(t, "222:2000", sessions[0].TmuxInstance,
		"list must sample the instance, not reuse the last tick's value")
}

func TestSessionInfo_TmuxInstanceKeyAlwaysPresent(t *testing.T) {
	raw, err := json.Marshal(SessionInfo{Code: "abc", Name: "dev"})
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"tmux_instance":""`,
		"the key must be transmitted even when unknown (spec §4.6)")
}

func TestTickNormal_InstanceProbeFailure_PropagatesEmpty(t *testing.T) {
	mod, fake, _ := newWatcherTestModule(t)
	fake.AddSession("dev", "/w")
	mod.tmuxInstanceFn = func(context.Context) string { return "" }
	mod.tickNormal()

	sessions, err := mod.ListSessions()
	require.NoError(t, err)
	require.NotEmpty(t, sessions)
	assert.Equal(t, "", sessions[0].TmuxInstance, "a probe failure must propagate empty, not a stale value")
}

func TestGetSession_StampsTmuxInstance(t *testing.T) {
	mod, fake, _ := newWatcherTestModule(t)
	fake.AddSession("dev", "/w")
	mod.tmuxInstanceFn = func(context.Context) string { return "333:3000" }

	sessions, err := mod.ListSessions()
	require.NoError(t, err)
	require.NotEmpty(t, sessions)

	info, err := mod.GetSession(sessions[0].Code)
	require.NoError(t, err)
	require.NotNil(t, info)
	assert.Equal(t, "333:3000", info.TmuxInstance, "the single-get path must stamp the generation too")
}

func TestTmuxInstance_ProviderMethodSamplesEveryCall(t *testing.T) {
	mod, _, _ := newWatcherTestModule(t)
	calls := 0
	mod.tmuxInstanceFn = func(context.Context) string {
		calls++
		return "444:4000"
	}
	assert.Equal(t, "444:4000", mod.TmuxInstance())
	assert.Equal(t, "444:4000", mod.TmuxInstance())
	assert.Equal(t, 2, calls, "every call must re-sample rather than reuse a cached value")
}

// --- versioned sessions frames (spec 2026-09-23 §3.2) ---

// sessionsFrame is a decoded WS `sessions` frame with its version keys.
type sessionsFrame struct {
	Type  string `json:"type"`
	Value string `json:"value"`
	Epoch string `json:"epoch"`
	Seq   uint64 `json:"seq"`
}

// drainSessionFrames collects every `sessions` frame currently queued on sub.
func drainSessionFrames(t *testing.T, sub *core.EventSubscriber) []sessionsFrame {
	t.Helper()
	var out []sessionsFrame
	timeout := time.After(100 * time.Millisecond)
	for {
		select {
		case msg := <-sub.SendCh():
			var f sessionsFrame
			if err := json.Unmarshal(msg, &f); err != nil || f.Type != "sessions" {
				continue
			}
			out = append(out, f)
		case <-timeout:
			return out
		}
	}
}

// expireDebounce backdates the broadcastSessions debounce stamp so the next
// call goes through.
// #1293 T4: a push path whose tmux read hangs is bounded by the list read
// timeout; it logs, sends nothing, consumes no seq, and leaves the slot free —
// the next ?fresh=1 and a new subscriber's snapshot succeed right after.
func TestPushPaths_StuckReadTimesOutThenFreshAndSnapshotSucceed(t *testing.T) {
	for _, tc := range []struct {
		name string
		push func(mod *SessionModule)
	}{
		{"tickNormal", func(mod *SessionModule) { mod.tickNormal() }},
		{"broadcastSessions", func(mod *SessionModule) { expireDebounce(mod); mod.broadcastSessions() }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mod, fake, events := newWatcherTestModule(t)
			sub := events.AddTestSubscriber()
			defer events.RemoveTestSubscriber(sub)
			fake.AddSession("s1", "/tmp")
			mod.listTimeout = 100 * time.Millisecond
			mux := http.NewServeMux()
			mod.RegisterRoutes(mux)

			fake.SetReadHook(tmux.BlockReadsUntil(make(chan struct{}), nil))
			done := make(chan struct{})
			go func() {
				tc.push(mod)
				close(done)
			}()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatalf("%s: a hung tmux read was not bounded", tc.name)
			}
			assert.Empty(t, drainSessionFrames(t, sub), "a timed-out read pushes nothing")

			fake.SetReadHook(nil)
			v := getFresh(t, mux)
			assert.Equal(t, uint64(1), v.Seq, "the timed-out push consumed no seq")

			snapSub := events.AddTestSubscriber()
			defer events.RemoveTestSubscriber(snapSub)
			mod.sendSessionsSnapshot(snapSub)
			frames := drainSessionFrames(t, snapSub)
			require.Len(t, frames, 1)
			assert.Equal(t, uint64(2), frames[0].Seq)
		})
	}
}

func expireDebounce(mod *SessionModule) {
	mod.wstate.mu.Lock()
	mod.wstate.lastBroadcast = time.Time{}
	mod.wstate.mu.Unlock()
}

func TestBroadcastSessions_FrameCarriesVersion(t *testing.T) {
	mod, fake, events := newWatcherTestModule(t)
	sub := events.AddTestSubscriber()
	defer events.RemoveTestSubscriber(sub)
	fake.AddSession("s1", "/tmp")

	mod.broadcastSessions()
	first := drainSessionFrames(t, sub)
	require.Len(t, first, 1)
	assert.Equal(t, mod.epoch, first[0].Epoch)
	assert.GreaterOrEqual(t, first[0].Seq, uint64(1))

	expireDebounce(mod)
	mod.broadcastSessions()
	second := drainSessionFrames(t, sub)
	require.Len(t, second, 1)
	assert.Equal(t, mod.epoch, second[0].Epoch)
	assert.Greater(t, second[0].Seq, first[0].Seq)
}

func TestTickNormal_FrameCarriesVersion(t *testing.T) {
	mod, fake, events := newWatcherTestModule(t)
	sub := events.AddTestSubscriber()
	defer events.RemoveTestSubscriber(sub)
	fake.AddSession("s1", "/tmp")

	mod.tickNormal()
	first := drainSessionFrames(t, sub)
	require.Len(t, first, 1)
	assert.Equal(t, mod.epoch, first[0].Epoch)
	assert.GreaterOrEqual(t, first[0].Seq, uint64(1))

	// Unchanged list: a new seq alone must not trigger a broadcast.
	mod.tickNormal()
	assert.Empty(t, drainSessionFrames(t, sub), "only the seq differs — no broadcast")

	fake.AddSession("s2", "/tmp")
	mod.tickNormal()
	next := drainSessionFrames(t, sub)
	require.Len(t, next, 1)
	assert.Equal(t, mod.epoch, next[0].Epoch)
	assert.Greater(t, next[0].Seq, first[0].Seq)
}

// The on-subscribe snapshot goes through the real WS path: AddTestSubscriber
// does not run OnSubscribe callbacks.
func TestOnSubscribeSnapshot_CarriesVersion(t *testing.T) {
	mod, fake, events := newWatcherTestModule(t)
	fake.AddSession("s1", "/tmp")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, mod.Start(ctx))

	srv := httptest.NewServer(http.HandlerFunc(events.HandleHostEvents))
	defer srv.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	require.NoError(t, err)
	defer conn.Close()

	require.NoError(t, conn.SetReadDeadline(time.Now().Add(3*time.Second)))
	for {
		_, msg, err := conn.ReadMessage()
		require.NoError(t, err, "no sessions frame arrived")
		var f sessionsFrame
		require.NoError(t, json.Unmarshal(msg, &f))
		if f.Type != "sessions" {
			continue
		}
		assert.Equal(t, mod.epoch, f.Epoch)
		assert.GreaterOrEqual(t, f.Seq, uint64(1))
		assert.Contains(t, f.Value, `"name":"s1"`)
		return
	}
}

// --- Self-healing tmux hooks (#1473 spec D3) ---

var allHookEvents = []string{"session-created", "session-closed", "session-renamed"}

// newHookTestModule builds a module whose watcher state is seeded by hand
// instead of by Start, so no real `tmux wait-for` goroutine runs and the
// wait-for gate can be read directly.
func newHookTestModule(t *testing.T, alive bool) (*SessionModule, *tmux.FakeExecutor, *core.EventsBroadcaster) {
	t.Helper()
	mod, fake, events := newWatcherTestModule(t)
	mod.waitForGate = make(chan bool, 1)
	mod.wstate.setTmuxAlive(alive)
	fake.SetAlive(alive)
	return mod, fake, events
}

func setInstance(mod *SessionModule, v string) {
	mod.tmuxInstanceFn = func(context.Context) string { return v }
}

// drainTypes returns the envelope types broadcast within the drain window,
// with the tmux status value appended ("tmux:ok").
func drainTypes(t *testing.T, sub *core.EventSubscriber) []string {
	t.Helper()
	var out []string
	timeout := time.After(100 * time.Millisecond)
	for {
		select {
		case msg := <-sub.SendCh():
			var env struct {
				Type  string `json:"type"`
				Value string `json:"value"`
			}
			if err := json.Unmarshal(msg, &env); err != nil {
				continue
			}
			if env.Type == "tmux" {
				out = append(out, "tmux:"+env.Value)
			} else {
				out = append(out, env.Type)
			}
		case <-timeout:
			return out
		}
	}
}

// A daemon that saw tmux go down must install the hooks when a server comes
// back: the new server has none (global hooks live in server memory).
func TestWatcherAliveEdge_InstallsHooks(t *testing.T) {
	mod, fake, events := newWatcherTestModule(t)
	sub := events.AddTestSubscriber()
	defer events.RemoveTestSubscriber(sub)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, mod.Start(ctx))

	fake.SetAlive(false)
	mod.checkAndBroadcast()
	require.False(t, mod.TmuxAlive())
	fake.ResetHookSets()

	fake.SetAlive(true)
	fake.AddSession("recovered", "/tmp")
	mod.checkAndBroadcast()
	require.True(t, mod.TmuxAlive())
	assert.Equal(t, allHookEvents, fake.HookSets())
}

func TestWatcherStayingDown_InstallsNoHooks(t *testing.T) {
	mod, fake, _ := newWatcherTestModule(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, mod.Start(ctx))

	fake.SetAlive(false)
	mod.checkAndBroadcast()
	fake.ResetHookSets()

	mod.checkAndBroadcast()
	mod.checkAndBroadcast()
	assert.Empty(t, fake.HookSets())
}

// A failed install on the alive edge must not skip the rest of the recovery,
// and the next normal tick retries until it succeeds — then stops.
func TestWatcherAliveEdge_FailedInstallRetriedByTickNormal(t *testing.T) {
	mod, fake, events := newHookTestModule(t, false)
	sub := events.AddTestSubscriber()
	defer events.RemoveTestSubscriber(sub)

	fake.SetHookGlobalError(errors.New("no server running"))
	fake.SetAlive(true)
	fake.AddSession("dev", "/w")
	setInstance(mod, "111:1000")
	mod.tickTmuxDown()

	assert.True(t, mod.TmuxAlive())
	// No tmux frame: "no server" was already reported as ok (#1474 D2).
	assert.Equal(t, []string{"sessions"}, drainTypes(t, sub),
		"a failed hook install must not skip the sessions broadcast")
	select {
	case v := <-mod.waitForGate:
		assert.True(t, v, "wait-for must be resumed")
	default:
		t.Fatal("wait-for was not resumed")
	}
	require.NotEmpty(t, fake.HookSets(), "the alive edge must attempt an install")

	fake.SetHookGlobalError(nil)
	fake.ResetHookSets()
	mod.tickNormal()
	assert.Equal(t, allHookEvents, fake.HookSets(), "tickNormal must retry the failed install")

	fake.ResetHookSets()
	mod.tickNormal()
	assert.Empty(t, fake.HookSets(), "a successful install must not be repeated")
}

// A server restart between two ticks (never seen as down) shows up as a new
// non-empty instance; the new server has no hooks, so reinstall.
func TestTickNormal_InstanceChange_ReinstallsHooks(t *testing.T) {
	mod, fake, _ := newHookTestModule(t, true)
	fake.AddSession("dev", "/w")
	setInstance(mod, "111:1000")
	mod.tickNormal()
	require.Equal(t, allHookEvents, fake.HookSets())

	fake.ResetHookSets()
	mod.tickNormal()
	require.Empty(t, fake.HookSets(), "same instance: no reinstall")

	setInstance(mod, "222:2000")
	mod.tickNormal()
	assert.Equal(t, allHookEvents, fake.HookSets(), "new instance: reinstall")
}

// An empty payload carries no instance, which proves nothing about a restart.
func TestTickNormal_EmptyPayload_DoesNotReinstallHooks(t *testing.T) {
	mod, fake, _ := newHookTestModule(t, true)
	fake.AddSession("dev", "/w")
	setInstance(mod, "111:1000")
	mod.tickNormal()
	require.Equal(t, allHookEvents, fake.HookSets())

	fake.ResetHookSets()
	require.NoError(t, fake.KillSession("dev"))
	mod.tickNormal()
	require.True(t, mod.TmuxAlive())
	assert.Empty(t, fake.HookSets())
}

// Start's own install counts: a later tick with no instance to compare must
// not install again, but a failed Start install is retried.
func TestStart_HookInstallSeedsWatcher(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		mod, fake, _ := newWatcherTestModule(t)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		require.NoError(t, mod.Start(ctx))
		require.Equal(t, allHookEvents, fake.HookSets())

		fake.ResetHookSets()
		mod.tickNormal() // no sessions: payload instance ""
		assert.Empty(t, fake.HookSets())
	})
	t.Run("failure", func(t *testing.T) {
		mod, fake, _ := newWatcherTestModule(t)
		fake.SetHookGlobalError(errors.New("no server running"))
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		require.NoError(t, mod.Start(ctx))

		fake.SetHookGlobalError(nil)
		fake.ResetHookSets()
		mod.tickNormal()
		assert.Equal(t, allHookEvents, fake.HookSets())
	})
}
