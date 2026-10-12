package push

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/module/session"

	"github.com/wake/purdex/internal/team"
)

// The team module's pause outbox asks the push module for a phone push in process (unattended quota pause spec §3.3, UP-2a).
// The module publishes itself as the PauseNotifier when it is ready, and the push reaches every device in its own title.

func TestPausePush_RegisteredAndReachesEveryDevice(t *testing.T) {
	te := newTriggerEnv(t, nil)
	te.register(tokA, "zh-TW", "mlab")
	te.register(tokB, "en", "mlab-en")
	svc, ok := te.mod.core.Registry.Get(team.PauseNotifyKey)
	if !ok {
		t.Fatal("the push module did not publish its PauseNotifier")
	}
	n, ok := svc.(team.PauseNotifier)
	if !ok {
		t.Fatalf("%T is not a PauseNotifier", svc)
	}
	if done := n.NotifyPause(team.PausePush{CollapseID: "pause:sid-1:1700", Text: "boss 已暫停：接力額度用完，等你接手，含 2 個 member", SessionID: "sid-1"}); !done {
		t.Fatal("NotifyPause not done")
	}
	calls := te.waitSends(t, 2)
	for _, c := range calls {
		if c.H.CollapseID != "pause:sid-1:1700" || !strings.Contains(c.Payload, "boss 已暫停：接力額度用完，等你接手，含 2 個 member") ||
			!strings.Contains(c.Payload, `"kind":"pause"`) || !strings.Contains(c.Payload, `"session_id":"sid-1"`) || !strings.Contains(c.Payload, `"host_id"`) {
			t.Fatalf("call = %+v", c)
		}
	}
}

// The payload names the session on this host (session_code, resolved from its tmux session by the sender, as an approval push does)
// so a tap on the notification goes to it; host_id is always there.
func TestPausePush_CarriesTheSessionCode(t *testing.T) {
	te := newTriggerEnv(t, nil)
	te.register(tokA, "zh-TW", "mlab")
	te.mod.sessions = func(context.Context) (map[string]session.SessionRef, error) {
		return map[string]session.SessionRef{"dev": {Code: "abc123", Created: sessionAt}}, nil
	}
	if !te.mod.pauseNotifier().NotifyPause(team.PausePush{CollapseID: "pause:sid-1:1", Text: "t", SessionID: "sid-1", TmuxSession: "dev", SinceMs: approvalAtMs}) {
		t.Fatal("not done")
	}
	c := te.waitSends(t, 1)[0]
	if !strings.Contains(c.Payload, `"session_code":"abc123"`) || !strings.Contains(c.Payload, `"host_id"`) {
		t.Fatalf("payload = %s", c.Payload)
	}
}

// The pause push never carries an EMPTY open_approvals / open_approval_keys that did not come from a read at send time: it goes
// through the same sender as an approval push, which reads the open set when it sends. A read that fails leaves both fields out
// (not []), a read that works carries the current set.
// Mutation gate: write the fields without a read → "open_approval_keys":[] in the failing-read case (red).
func TestPausePush_NeverCarriesAnEmptyOpenSetItDidNotRead(t *testing.T) {
	te := newTriggerEnv(t, nil)
	te.register(tokA, "zh-TW", "mlab")
	te.events.readOpen = func() ([]team.Approval, error) { return nil, errors.New("team.db busy") }
	te.mod.reader = te.events
	if !te.mod.pauseNotifier().NotifyPause(team.PausePush{CollapseID: "pause:sid-1:1", Text: "t", SessionID: "sid-1"}) {
		t.Fatal("not done")
	}
	c := te.waitSends(t, 1)[0]
	if strings.Contains(c.Payload, "open_approvals") || strings.Contains(c.Payload, "open_approval_keys") {
		t.Fatalf("the read failed, yet the payload carries the open set: %s", c.Payload)
	}

	te2 := newTriggerEnv(t, nil)
	te2.register(tokA, "zh-TW", "mlab")
	te2.events.open = []team.Approval{leadApproval("ap1")}
	te2.mod.reader = te2.events
	if !te2.mod.pauseNotifier().NotifyPause(team.PausePush{CollapseID: "pause:sid-1:2", Text: "t", SessionID: "sid-1"}) {
		t.Fatal("not done")
	}
	c2 := te2.waitSends(t, 1)[0]
	if !strings.Contains(c2.Payload, `"open_approvals":1`) || !strings.Contains(c2.Payload, `"open_approval_keys":["a:ap1"]`) {
		t.Fatalf("a read that worked: want the current set, payload = %s", c2.Payload)
	}
}

// A person at the computer needs no phone push; no device, nobody to tell: both are done, nothing is sent.
// Mutation gate: push regardless of presence → a send (red).
func TestPausePush_NobodyToTellIsDoneAndSendsNothing(t *testing.T) {
	te := newTriggerEnv(t, presenceFake{at: func() bool { return true }})
	te.register(tokA, "zh-TW", "mlab")
	n := te.mod.pauseNotifier()
	if !n.NotifyPause(team.PausePush{CollapseID: "pause:sid-1:1", Text: "text"}) {
		t.Fatal("a person at the computer: want done")
	}
	te.noSends(t)

	te2 := newTriggerEnv(t, nil) // no device registered
	if !te2.mod.pauseNotifier().NotifyPause(team.PausePush{CollapseID: "pause:sid-1:1", Text: "text"}) {
		t.Fatal("no device: want done")
	}
	te2.noSends(t)
}

// A module that is not up (not started, or stopped) is not done: the outbox row stays for the next pass.
func TestPausePush_AModuleThatIsNotUpIsNotDone(t *testing.T) {
	e := newEnv(t)
	if e.mod.pauseNotifier().NotifyPause(team.PausePush{CollapseID: "pause:sid-1:1", Text: "text"}) {
		t.Fatal("a module with no sender reported done")
	}
}
