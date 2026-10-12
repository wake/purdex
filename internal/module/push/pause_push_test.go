package push

import (
	"strings"
	"testing"

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
	if done := n.NotifyPause("pause:sid-1:1700", "boss 已暫停：接力額度用完，等你接手，含 2 個 member"); !done {
		t.Fatal("NotifyPause not done")
	}
	calls := te.waitSends(t, 2)
	for _, c := range calls {
		if c.H.CollapseID != "pause:sid-1:1700" || !strings.Contains(c.Payload, "boss 已暫停：接力額度用完，等你接手，含 2 個 member") || !strings.Contains(c.Payload, `"kind":"pause"`) {
			t.Fatalf("call = %+v", c)
		}
	}
}

// A person at the computer needs no phone push; no device, nobody to tell: both are done, nothing is sent.
// Mutation gate: push regardless of presence → a send (red).
func TestPausePush_NobodyToTellIsDoneAndSendsNothing(t *testing.T) {
	te := newTriggerEnv(t, presenceFake{at: func() bool { return true }})
	te.register(tokA, "zh-TW", "mlab")
	n := te.mod.pauseNotifier()
	if !n.NotifyPause("pause:sid-1:1", "text") {
		t.Fatal("a person at the computer: want done")
	}
	te.noSends(t)

	te2 := newTriggerEnv(t, nil) // no device registered
	if !te2.mod.pauseNotifier().NotifyPause("pause:sid-1:1", "text") {
		t.Fatal("no device: want done")
	}
	te2.noSends(t)
}

// A module that is not up (not started, or stopped) is not done: the outbox row stays for the next pass.
func TestPausePush_AModuleThatIsNotUpIsNotDone(t *testing.T) {
	e := newEnv(t)
	if e.mod.pauseNotifier().NotifyPause("pause:sid-1:1", "text") {
		t.Fatal("a module with no sender reported done")
	}
}
