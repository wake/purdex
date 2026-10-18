package conversation

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/convmodel/ccnorm"
	"github.com/wake/purdex/internal/promptq"
)

// The typed routes write the message stages (plan §1.1): received .. entered, failed and unknown on /submit, recalled on /recall.

func stagesOf(m *Module) map[string]stageJSON {
	all, _ := m.stages.snapshot(sid, time.Now())
	out := map[string]stageJSON{}
	for _, s := range all {
		out[s.ClientMsgID] = s
	}
	return out
}

func TestTypedStages_FillSubmitGoesReceivedFilledEntered(t *testing.T) {
	te := newTEnv(t)
	var atEnter string
	te.keys.onCall = func(keyCall) { atEnter = stagesOf(te.mod)["m1"].Stage }
	answer(t, te.submit("m1", "hello"), 200, "handed", "fill", "")
	assert.Equal(t, stageFilled, atEnter, "filled is written before the Enter goes out")
	got := stagesOf(te.mod)["m1"]
	assert.Equal(t, stageEntered, got.Stage)
	assert.Equal(t, "fill", got.Mode)
}

func TestTypedStages_RefusalAfterReceivedIsFailedWithTheReason(t *testing.T) {
	te := newTEnv(t)
	te.light.seq = []string{"waiting"}
	answer(t, te.submit("w1", "x"), 409, "refused", "", "waiting")
	got := stagesOf(te.mod)["w1"]
	assert.Equal(t, stageFailed, got.Stage)
	assert.Equal(t, "waiting", got.Reason)
}

func TestTypedStages_RefusalsBeforeReceivedLeaveNoRow(t *testing.T) {
	te := newTEnv(t)
	errOf(t, te.submit("v1", "/compact"), 400, "needs_terminal")
	errOf(t, te.post("/api/sessions/"+tCode+"/submit", body("v4", "x", "99:99")), 409, "generation")
	te.mod.td.owners = tResolver{}
	errOf(t, te.submit("v5", "x"), 409, "not_claude")
	assert.Empty(t, stagesOf(te.mod))
}

func TestTypedStages_ReplayAddsNoStage(t *testing.T) {
	te := newTEnv(t)
	answer(t, te.submit("m1", "hello"), 200, "handed", "fill", "")
	_, seq := te.mod.stages.snapshot(sid, time.Now())
	answer(t, te.submit("m1", "hello"), 200, "handed", "fill", "")
	_, again := te.mod.stages.snapshot(sid, time.Now())
	assert.Equal(t, seq, again, "a replayed id is answered from the ledgers and writes nothing")
}

func TestTypedStages_ReplayStillAnswersWhenTheSessionIsGone(t *testing.T) {
	te := newTEnv(t)
	answer(t, te.submit("m1", "hello"), 200, "handed", "fill", "")
	_, seq := te.mod.stages.snapshot(sid, time.Now())
	te.mod.td.owners = tResolver{}
	answer(t, te.submit("m1", "hello"), 200, "handed", "fill", "")
	_, again := te.mod.stages.snapshot(sid, time.Now())
	assert.Equal(t, seq, again)
}

func TestTypedStages_UnknownFillOutcome(t *testing.T) {
	te := newTEnv(t)
	te.fm.onFill = func(promptq.Job) (promptq.Outcome, bool) { return promptq.Outcome{}, false } // the mod never reports
	answer(t, te.submit("u1", "x"), 200, "unknown", "fill", "")
	got := stagesOf(te.mod)["u1"]
	assert.Equal(t, stageUnknown, got.Stage)
	assert.Equal(t, "fill", got.Mode)
}

func TestTypedStages_KeysSubmitIsEnteredAndATextLeftInTheTerminalIsUnknown(t *testing.T) {
	te := keysEnv(t)
	answer(t, te.submit("k1", "typed blind"), 200, "handed", "keys", "")
	got := stagesOf(te.mod)["k1"]
	assert.Equal(t, stageEntered, got.Stage)
	assert.Equal(t, "keys", got.Mode)

	te.keys.errs = []error{nil, errBoom} // the text goes, the Enter fails
	w := te.submit("k2", "second")
	assert.Equal(t, 409, w.Code)
	got = stagesOf(te.mod)["k2"]
	assert.Equal(t, stageUnknown, got.Stage, "the text may be sitting in the terminal: not failed, not known")
	assert.Equal(t, "text_left_in_terminal", got.Reason)
}

func TestTypedStages_BusyAtTheLocksIsFailedAndTheRetryIdIsItsOwnRow(t *testing.T) {
	te := newTEnv(t)
	release, ok := te.mod.lockPaneAndSession(t.Context(), tOwner.TmuxPaneID, tOwner.SessionID, time.Second)
	require.True(t, ok)
	answer(t, te.submit("b0", "again"), 429, "refused", "", "busy")
	release()
	assert.Equal(t, stageFailed, stagesOf(te.mod)["b0"].Stage)
	answer(t, te.submit("b1", "again"), 200, "handed", "fill", "")
	st := stagesOf(te.mod)
	assert.Equal(t, stageFailed, st["b0"].Stage, "the refused id stays failed: the clients fold the retry into the one bubble")
	assert.Equal(t, stageEntered, st["b1"].Stage)
}

func TestTypedStages_SuccessfulRecallTakesBackTheQueuedIds(t *testing.T) {
	re := newRecallEnv(t, queueRow("enqueue", "first"), queueRow("enqueue", "second"))
	now := time.Now()
	for id, text := range map[string]string{"a": "first", "b": "second"} {
		re.mod.stages.begin(sid, id, text, now)
		re.mod.stages.set(sid, id, stageEntered, "", "fill", now)
	}
	re.mod.stages.begin(sid, "c", "c", now) // not queued: stays
	re.mod.stages.set(sid, "c", stageEntered, "", "fill", now)
	// the feed's queue holds two rows: claim them for a and b
	re.mod.stages.observe(sid, queuedFor(t, re.mod), false, nil, now)
	re.popped = "first\nsecond"
	re.ok(t, re.recall("rc"), "first\nsecond")
	st := stagesOf(re.mod)
	assert.Equal(t, stageRecalled, st["a"].Stage)
	assert.Equal(t, stageRecalled, st["b"].Stage)
	assert.Equal(t, stageEntered, st["c"].Stage)
	_, has := st["rc"]
	assert.False(t, has, "the recall's own id is an idempotency key, not a message")
}

func TestTypedStages_RefusedRecallChangesNothing(t *testing.T) {
	re := newRecallEnv(t) // nothing queued
	now := time.Now()
	re.mod.stages.begin(sid, "a", "a", now)
	re.mod.stages.set(sid, "a", stageEntered, "", "fill", now)
	_, seq := re.mod.stages.snapshot(sid, now)
	assert.Equal(t, 409, re.recall("rc").Code)
	_, again := re.mod.stages.snapshot(sid, now)
	assert.Equal(t, seq, again)
}

// A request that waits for the locks and then finds a concurrent request already recorded its id is answered from the ledger
// and writes no stage of its own: the request that recorded the id owns the stages.
func TestTypedStages_ReplayUnderTheLocksWritesNothing(t *testing.T) {
	te := newTEnv(t)
	release, ok := te.mod.lockPaneAndSession(t.Context(), tOwner.TmuxPaneID, tOwner.SessionID, time.Second)
	require.True(t, ok)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- te.submit("r1", "hello") }()
	eventually(t, "the request has begun and waits for the locks", func() bool {
		_, has := stagesOf(te.mod)["r1"]
		return has
	})
	te.mod.keysSeen.put("r1", keysRow{handed: true, sid: tOwner.SessionID}, time.Now()) // the concurrent request recorded it
	release()
	answer(t, <-done, 200, "handed", "keys", "")
	assert.Equal(t, stageReceived, stagesOf(te.mod)["r1"].Stage, "the replay answer is not a stage of this request")
}

// An unknown recall (Up pressed, nothing proven) takes nothing back: the ids stay queued.
func TestTypedStages_UnknownRecallLeavesTheQueuedIdsAlone(t *testing.T) {
	re := newRecallEnv(t, queueRow("enqueue", "a"))
	re.tune(func(t *typedTuning) { t.recallSettle, t.recallReadBudget = 20*time.Millisecond, 60*time.Millisecond })
	re.script(&script{seq: []string{""}, clearStatus: promptq.Accepted})
	now := time.Now()
	re.mod.stages.begin(sid, "q1", "a", now)
	re.mod.stages.set(sid, "q1", stageEntered, "", "fill", now)
	re.mod.stages.observe(sid, queuedFor(t, re.mod), false, nil, now)
	require.Equal(t, stageQueued, stagesOf(re.mod)["q1"].Stage)
	re.unknown(t, re.recall("rc"), "recall_empty")
	assert.Equal(t, stageQueued, stagesOf(re.mod)["q1"].Stage)
}

var errBoom = assert.AnError

func queuedFor(t *testing.T, m *Module) []ccnorm.Queued {
	t.Helper()
	q, _, ok := m.queuedNow(t.Context(), sid)
	require.True(t, ok)
	return q
}
