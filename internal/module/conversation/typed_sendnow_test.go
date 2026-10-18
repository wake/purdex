package conversation

import (
	"encoding/json"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/convmodel"
	"github.com/wake/purdex/internal/module/agent"
	"github.com/wake/purdex/internal/promptq"
	"github.com/wake/purdex/internal/tmux"
)

// POST /api/sessions/{code}/send-now (MS2-2, plan §1.3): Ctrl+X Ctrl+S into Claude Code's input, which sends the queued messages
// at once. The same real feed (queue-operation rows) and scripted composer as the recall tests.

func (re *recallEnv) sendNow(id string) *httptest.ResponseRecorder {
	return re.sendNowAt(id, tInst)
}

func (re *recallEnv) sendNowAt(id, inst string) *httptest.ResponseRecorder {
	b, _ := json.Marshal(map[string]string{"expected_tmux_instance": inst, "client_msg_id": id})
	return re.post("/api/sessions/"+tCode+"/send-now", string(b))
}

// sentNow asserts a 200 sent_now answer.
func sentNow(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	answer(t, w, 200, "sent_now", "", "")
}

func (re *recallEnv) sendNowKeys() int {
	re.keys.mu.Lock()
	defer re.keys.mu.Unlock()
	n := 0
	for _, c := range re.keys.calls {
		if len(c.keys) == 1 && c.keys[0] == tmux.KeySendNow {
			n++
		}
	}
	return n
}

func TestSendNow_SendsCtrlXCtrlSAsOneGuardedCall(t *testing.T) {
	re := newRecallEnv(t, queueRow("enqueue", "first"), queueRow("enqueue", "second"))
	sentNow(t, re.sendNow("sn1"))
	require.Len(t, re.keys.calls, 1)
	k := re.keys.calls[0]
	assert.Equal(t, "$1", k.sid)
	assert.Equal(t, tInst, k.inst)
	assert.Equal(t, "%5", k.pane)
	assert.Equal(t, []string{"\x18\x13"}, k.keys, "MS2-0: Ctrl+X Ctrl+S, both bytes in one send-keys")
}

// D7 race (plan rev 2, codex #3): the read of Claude Code's input and the keys are back to back under the locks; nothing else
// reaches the mod or tmux in between.
func TestSendNow_TheReadAndTheKeysAreBackToBack(t *testing.T) {
	re := newRecallEnv(t, queueRow("enqueue", "a"))
	var mu sync.Mutex
	var log []string
	note := func(s string) { mu.Lock(); log = append(log, s); mu.Unlock() }
	re.fm.onJob = func(j promptq.Job) promptq.Outcome {
		note("mod:" + j.Kind)
		return re.comp.job(j)
	}
	re.keys.onCall = func(c keyCall) { note("keys:" + c.keys[0]) }
	sentNow(t, re.sendNow("sn2"))
	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, []string{"mod:read", "keys:\x18\x13"}, log, "exactly one read, then the keys, nothing between")
}

func TestSendNow_Refusals(t *testing.T) {
	cases := []struct {
		name   string
		setup  func(re *recallEnv)
		status int
		reason string
	}{
		{"no mod owner", func(re *recallEnv) { re.owners.drop() }, 409, "no_owner"},
		{"a dialog is open", func(re *recallEnv) { re.light.seq = []string{"waiting"} }, 409, "waiting"},
		{"the light cannot be read", func(re *recallEnv) { re.light.seq = []string{"!"} }, 409, "light_unavailable"},
		{"two panes claim the session", func(re *recallEnv) {
			re.panes.panes = append(re.panes.panes, agent.PaneOwner{SessionID: sid, FrameID: "f2", TmuxPaneID: "%6", AgentType: "cc"})
		}, 409, "ambiguous_pane"},
		{"the person has a draft in Claude Code", func(re *recallEnv) { re.comp.set("half a thought") }, 409, "input_not_empty"},
		{"the pane is in copy mode", func(re *recallEnv) { re.keys.errs = []error{tmux.ErrPaneBusy} }, 409, "pane_busy"},
		{"the tmux server restarted", func(re *recallEnv) { re.keys.errs = []error{tmux.ErrGenerationMismatch} }, 409, "generation"},
	}
	for _, c := range cases {
		re := newRecallEnv(t, queueRow("enqueue", "queued one"))
		c.setup(re)
		w := re.sendNow("r-" + c.reason)
		answer(t, w, c.status, "refused", "", c.reason)
		if c.reason != "pane_busy" && c.reason != "generation" && re.sendNowKeys() != 0 {
			t.Fatalf("%s: keys were sent", c.name)
		}
	}
}

func TestSendNow_NothingQueuedIsRefusedWithoutKeys(t *testing.T) {
	re := newRecallEnv(t) // an empty queue
	answer(t, re.sendNow("sn3"), 409, "refused", "", "nothing_queued")
	assert.Zero(t, re.keys.n())
	assert.Zero(t, re.fm.count(promptq.KindRead), "an empty queue is decided before the input is read")
}

func TestSendNow_GenerationInTheBodyIsCheckedBeforeAnything(t *testing.T) {
	re := newRecallEnv(t, queueRow("enqueue", "a"))
	answer(t, re.sendNowAt("sn4", "99:99"), 409, "refused", "", "generation")
	assert.Zero(t, re.keys.n())
	errOf(t, re.sendNowAt("sn5", "1}"), 400, "bad_expected_tmux_instance")
	errOf(t, re.sendNow(""), 400, "bad_client_msg_id")
}

func TestSendNow_ReplayIsAnsweredFromTheLedgerAndNeverSendsTwice(t *testing.T) {
	re := newRecallEnv(t, queueRow("enqueue", "a"))
	sentNow(t, re.sendNow("sn6"))
	sentNow(t, re.sendNow("sn6"))
	assert.Equal(t, 1, re.sendNowKeys())
	// even when the session cannot be resolved any more
	re.mod.td.owners = tResolver{}
	sentNow(t, re.sendNow("sn6"))
	assert.Equal(t, 1, re.sendNowKeys())
	// another id is a new request
	re.mod.td.owners = tResolver{found: true, owner: tOwner}
	sentNow(t, re.sendNow("sn7"))
	assert.Equal(t, 2, re.sendNowKeys())
}

func TestSendNow_ARefusalBeforeTheKeysIsNotRecorded(t *testing.T) {
	re := newRecallEnv(t, queueRow("enqueue", "a"))
	re.comp.set("draft")
	answer(t, re.sendNow("sn8"), 409, "refused", "", "input_not_empty")
	re.comp.set("")
	sentNow(t, re.sendNow("sn8")) // same id, the person emptied the input: it is a fresh try
	assert.Equal(t, 1, re.sendNowKeys())
}

func TestSendNow_AGuardRefusalIsNotRecordedEither(t *testing.T) {
	re := newRecallEnv(t, queueRow("enqueue", "a"))
	re.keys.errs = []error{tmux.ErrPaneBusy} // copy mode: nothing was sent
	answer(t, re.sendNow("sn11"), 409, "refused", "", "pane_busy")
	sentNow(t, re.sendNow("sn11")) // same id, the person left copy mode: a fresh try
	assert.Equal(t, 2, re.keys.n())
}

func TestSendNow_AKeysFailureIsUnknownAndRecorded(t *testing.T) {
	re := newRecallEnv(t, queueRow("enqueue", "a"))
	re.keys.errs = []error{errBoom}
	answer(t, re.sendNow("sn9"), 200, "unknown", "", "keys_failed")
	answer(t, re.sendNow("sn9"), 200, "unknown", "", "keys_failed")
	assert.Equal(t, 1, re.sendNowKeys(), "the keys may have gone: a repeat of this id never presses them again")
}

func TestSendNow_AddsNoStage(t *testing.T) {
	re := newRecallEnv(t, queueRow("enqueue", "a"))
	now := time.Now()
	re.mod.stages.begin(sid, "q1", "a", now)
	re.mod.stages.set(sid, "q1", stageEntered, "", "fill", now)
	re.mod.stages.observe(sid, queuedFor(t, re.mod), false, nil, now)
	_, seq := re.mod.stages.snapshot(sid, now)
	sentNow(t, re.sendNow("sn10"))
	_, again := re.mod.stages.snapshot(sid, now)
	assert.Equal(t, seq, again, "the queued messages reach read through the ordinary matching, not through this route")
}

// The conversation capability send_now: "keys" for a live terminal conversation whose mod can read the input; otherwise absent
// with the reason not_wired. Mutation gate: ignoring the owner or live -> red.
func TestCapabilities_SendNowNeedsALiveConversationAndAModOwner(t *testing.T) {
	e := newEnv(t)
	f := &fakeSender{}
	e.sender(f)
	if c := e.mod.capabilitiesFor(sid, "terminal", true); c.SendNow != "" || c.Reasons["send_now"] != "not_wired" {
		t.Fatalf("live, no mod owner: %+v", c)
	}
	f.owner = true
	if c := e.mod.capabilitiesFor(sid, "terminal", true); c.SendNow != "keys" || c.Reasons["send_now"] != "" {
		t.Fatalf("live, mod owner: %+v", c)
	}
	if c := e.mod.capabilitiesFor(sid, "terminal", false); c.SendNow != "" || c.Reasons["send_now"] != "not_wired" {
		t.Fatalf("mod owner, not live: %+v", c)
	}
	if c := e.mod.capabilitiesFor(sid, "execution", true); c.SendNow != "" {
		t.Fatalf("execution: %+v", c)
	}
}

// A change of send_now alone is pushed as a capabilities frame (it is part of the key the stream compares).
func TestCapsKey_SendNowIsPartOfTheKey(t *testing.T) {
	a := capsKeyOf(&convmodel.Capabilities{Send: "prompt", Interrupt: "prompt", SendNow: "keys"})
	b := capsKeyOf(&convmodel.Capabilities{Send: "prompt", Interrupt: "prompt"})
	if a == b {
		t.Fatal("send_now must move the capabilities key")
	}
}
