package nex

// PR #1586 C2: no second resume right after a success — the visibility
// barrier and the recent-resume marker.

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"lab.protype.tw/wake/nexen/store"

	"github.com/wake/purdex/internal/module/agent"
)

// fakeClock swaps nowMs for the test; the returned func advances it.
func fakeClock(t *testing.T) func(time.Duration) {
	t.Helper()
	orig := nowMs
	var mu sync.Mutex
	cur := int64(1_000_000)
	nowMs = func() int64 { mu.Lock(); defer mu.Unlock(); return cur }
	t.Cleanup(func() { nowMs = orig })
	return func(d time.Duration) { mu.Lock(); defer mu.Unlock(); cur += d.Milliseconds() }
}

func TestRecentResume_TTLAndPrune(t *testing.T) {
	advance := fakeClock(t)
	m := &Module{}
	assert.False(t, m.recentlyResumed("S"))
	m.markRecentResume("S")
	assert.True(t, m.recentlyResumed("S"))
	assert.False(t, m.recentlyResumed("other"))
	advance(recentResumeTTL - time.Millisecond)
	assert.True(t, m.recentlyResumed("S"))
	m.markRecentResume("T")
	advance(2 * time.Millisecond)
	assert.False(t, m.recentlyResumed("S"), "expired")
	assert.True(t, m.recentlyResumed("T"))
	m.recentResumes.mu.Lock()
	_, stale := m.recentResumes.at["S"]
	m.recentResumes.mu.Unlock()
	assert.False(t, stale, "pruned lazily")
}

func TestRecentResume_Concurrent(t *testing.T) {
	m := &Module{}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				sid := fmt.Sprintf("s%d", (i+j)%5)
				m.markRecentResume(sid)
				_ = m.recentlyResumed(sid)
			}
		}(i)
	}
	wg.Wait()
	assert.True(t, m.recentlyResumed("s0"))
}

func TestCheckOwners_RecentResumeMarker(t *testing.T) {
	advance := fakeClock(t)
	env := newTTEnv(t)
	m := env.m
	ctx := context.Background()
	m.markRecentResume(tbSessionID)

	herr := m.checkOwners(ctx, tbSessionID, "", "")
	require.NotNil(t, herr)
	assert.Equal(t, http.StatusConflict, herr.status)
	assert.Equal(t, "session_owned", herr.code)
	assert.Equal(t, "terminal", herr.detail["owner"])
	assert.Equal(t, true, herr.detail["recent_resume"])
	assert.Equal(t, tbSessionID, herr.detail["session_id"])
	assert.Equal(t, 0, env.terminals.Calls(), "decided before the terminal lookup")

	assert.Nil(t, m.checkOwners(ctx, tbSessionID, "", "%4"), "handoff transfers the terminal itself and ignores the marker")

	advance(recentResumeTTL + time.Second)
	assert.Nil(t, m.checkOwners(ctx, tbSessionID, "", ""))
}

func verifiedFrame(sid string) []agent.TerminalSession {
	return []agent.TerminalSession{{PaneID: "%9", SessionID: sid, AgentType: "cc", Verified: true}}
}

// The worker exits only after the new terminal's frame is visible.
func TestTakeToTerminal_BarrierWaitsForOwnerFrame(t *testing.T) {
	env := newTTEnv(t)
	env.m.ownerVisiblePoll = 2 * time.Millisecond
	env.m.ownerVisibleTimeout = 5 * time.Second
	tl := env.m.tmux.(*keysClock).tl
	post := 0
	env.terminals.byCall = func(int) []agent.TerminalSession {
		if !slices.Contains(tl.snapshot(), "keys") {
			return nil
		}
		post++
		if post <= 2 {
			return nil
		}
		tl.add("owner_seen")
		return verifiedFrame(tbSessionID)
	}
	status, body := env.post(t, tbExecID, ttBody())
	require.Equal(t, http.StatusOK, status, "%v", body)
	assert.Equal(t, 3, post, "looked up until the frame appeared")
	assert.Less(t, env.at(t, "owner_seen"), env.at(t, "terminate"), "exit after the frame is seen")
	assert.Less(t, env.at(t, "keys"), env.at(t, "owner_seen"))
	assert.True(t, env.m.recentlyResumed(tbSessionID))
}

func TestTakeToTerminal_BarrierTimeoutIsNotAnError(t *testing.T) {
	env := newTTEnv(t)
	env.m.ownerVisiblePoll = 2 * time.Millisecond
	env.m.ownerVisibleTimeout = 50 * time.Millisecond
	var mu sync.Mutex
	var logs []string
	env.m.logf = func(f string, a ...any) { mu.Lock(); logs = append(logs, fmt.Sprintf(f, a...)); mu.Unlock() }
	start := time.Now()
	status, body := env.post(t, tbExecID, ttBody())
	require.Equal(t, http.StatusOK, status, "%v", body)
	assert.GreaterOrEqual(t, time.Since(start), 50*time.Millisecond)
	assert.Contains(t, env.timeline(t), "terminate", "the exit still happens")
	mu.Lock()
	defer mu.Unlock()
	assert.True(t, slices.ContainsFunc(logs, func(l string) bool {
		return containsAll(l, "nex: take-to-terminal", tbSessionID, "terminal owner not yet visible after", "recent-resume marker")
	}), "%v", logs)
}

func containsAll(s string, subs ...string) bool {
	for _, x := range subs {
		if !strings.Contains(s, x) {
			return false
		}
	}
	return true
}

func TestTakeback_BarrierAndMarker(t *testing.T) {
	env := newTakebackEnv(t)
	env.m.ownerVisiblePoll = 2 * time.Millisecond
	env.m.ownerVisibleTimeout = 5 * time.Second
	post := 0
	env.terminals.byCall = func(n int) []agent.TerminalSession {
		if len(env.tmux.RawKeysSent()) == 0 {
			return nil
		}
		post++
		if post <= 2 {
			return nil
		}
		return verifiedFrame(tbSessionID)
	}
	status, body := env.post(t, hoCode, takebackBody())
	require.Equal(t, http.StatusOK, status, "%v", body)
	assert.Equal(t, 3, post)
	assert.True(t, env.m.recentlyResumed(tbSessionID))
}

// A second take-to-terminal of the now exited row, before the terminal's
// frame is recorded, is stopped by the marker; once it expires the owner
// check passes again.
func TestTakeToTerminal_SecondRequestStoppedByMarker(t *testing.T) {
	advance := fakeClock(t)
	env := newTTEnv(t)
	status, body := env.post(t, tbExecID, ttBody())
	require.Equal(t, http.StatusOK, status, "%v", body)
	creates := len(env.sessions.Creates())
	keys := len(env.tmux.RawKeysSent())

	exited := ttExec(store.StateTerminated)
	exited.ArchivedAt = 7
	env.store.script(exited)
	status, body = env.post(t, tbExecID, ttBody())
	assert.Equal(t, http.StatusConflict, status, "%v", body)
	assert.Equal(t, "session_owned", body["code"])
	assert.Equal(t, "terminal", body["owner"])
	assert.Equal(t, true, body["recent_resume"])
	assert.Len(t, env.sessions.Creates(), creates, "no second create")
	assert.Len(t, env.tmux.RawKeysSent(), keys, "no second resume keys")

	advance(recentResumeTTL + time.Second)
	env.store.script(exited)
	status, body = env.post(t, tbExecID, ttBody())
	assert.NotEqual(t, "session_owned", body["code"], "past the owner check (%d %v)", status, body)
}
