package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/wake/purdex/internal/agent/opencode"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/module/session"
	"github.com/wake/purdex/internal/tmux"
)

// timedOutSessionProvider answers every ListSessions with the error a
// session-list read that hit its deadline returns (#1293): it wraps
// context.DeadlineExceeded.
type timedOutSessionProvider struct {
	fakeSessionProvider
}

func (timedOutSessionProvider) ListSessions() ([]session.SessionInfo, error) {
	return nil, fmt.Errorf("tmux list-sessions: %w (signal: killed)", context.DeadlineExceeded)
}

// The agent snapshot keeps calling ListSessions(); a timed-out list is an
// error there exactly like a tmux error: the replay is skipped. Observable:
// the per-row loop never runs, so a legacy row that the loop would delete
// (see TestSendSnapshot_OpencodeLegacyEventName_SkipAndCleanup) survives and
// nothing is sent.
func TestSendSnapshot_SessionListTimeoutSkipsReplay(t *testing.T) {
	m := newTestModule(t)
	fakeTmux := tmux.NewFakeExecutor()
	m.tmux = fakeTmux
	m.sessions = &timedOutSessionProvider{}
	m.core = &core.Core{Events: core.NewEventsBroadcaster(), Tmux: fakeTmux}
	m.registry.Register(opencode.NewProvider())
	if err := m.events.Set("legacy", "Stop", json.RawMessage(`{}`), "opencode", 11); err != nil {
		t.Fatalf("seed legacy event: %v", err)
	}

	broadcaster := core.NewEventsBroadcaster()
	sub := broadcaster.AddTestSubscriber()
	defer broadcaster.RemoveTestSubscriber(sub)

	m.sendSnapshot(sub)

	select {
	case msg := <-sub.SendCh():
		t.Errorf("snapshot sent a frame despite a timed-out session list: %s", msg)
	case <-time.After(50 * time.Millisecond):
	}
	got, err := m.events.Get("legacy")
	if err != nil {
		t.Fatalf("events.Get: %v", err)
	}
	if got == nil {
		t.Errorf("the replay loop ran on a timed-out session list (legacy row was cleaned up)")
	}
}

// The hook fallback (resolveSessionCode's slow path) treats a timed-out list
// like a tmux error: the lookup fails ("").
func TestResolveSessionCode_SessionListTimeoutFailsLookup(t *testing.T) {
	m := &Module{}
	m.sessions = &timedOutSessionProvider{fakeSessionProvider{
		sessions: []session.SessionInfo{{Name: "alpha", Code: "alpha-code"}},
	}}
	if got := m.resolveSessionCode("alpha"); got != "" {
		t.Errorf("resolveSessionCode = %q, want \"\" on a timed-out list", got)
	}
}
