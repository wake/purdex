package nex

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"lab.protype.tw/wake/nexen/execution"
	"lab.protype.tw/wake/nexen/store"

	"github.com/wake/purdex/internal/module/agent"
)

// U18 (#1647, member of the handoff spec): a resume command rendered by a handoff, a rollback or a take-back carries the
// session's model and effort.

const (
	opusID    = "claude-opus-5-5"
	flagsOpus = " --model 'claude-opus-5-5' --effort xhigh"
)

var opusXhigh = sessionReading{Model: opusID, Effort: "xhigh"}

// The flags go right after the session id of `--resume {id}`, whatever follows it. Mutations: append at the end → the "-v" case
// is red; no insertion → red.
func TestApplySessionFlags_InsertedAfterTheResumeID(t *testing.T) {
	cases := map[string]string{
		"claude --resume {id}":          "claude --resume {id}" + flagsOpus,
		"cld-yolo --resume {id} -v":     "cld-yolo --resume {id}" + flagsOpus + " -v",
		"claude -r {id}":                "claude -r {id}" + flagsOpus,
		"claude --resume={id} --x":      "claude --resume={id}" + flagsOpus + " --x",
		"FOO=1 claude --resume {id};ls": "FOO=1 claude --resume {id}" + flagsOpus + ";ls",
	}
	for in, want := range cases {
		assert.Equal(t, want, applySessionFlags(in, opusXhigh), in)
	}
}

// A template with no `--resume {id}` is not ours to change (codex, opencode, wrappers that take the id another way), and a
// reading with nothing in it changes nothing. Mutation: touch every template → red.
func TestApplySessionFlags_LeavesWhatIsNotAClaudeResumeAlone(t *testing.T) {
	for _, in := range []string{"codex resume {id}", "opencode -s {id}", "ccr {id}", "claude -c", "claude --resume latest", ""} {
		assert.Equal(t, in, applySessionFlags(in, opusXhigh), in)
	}
	assert.Equal(t, "claude --resume {id}", applySessionFlags("claude --resume {id}", sessionReading{}))
}

// What the user's template already says wins, flag by flag. Mutation: add them anyway → duplicated flags (red).
func TestApplySessionFlags_TheTemplatesOwnFlagsWin(t *testing.T) {
	// what is added goes right after the id, so it comes before the user's own flags
	assert.Equal(t, "claude --resume {id} --effort xhigh --model sonnet",
		applySessionFlags("claude --resume {id} --model sonnet", sessionReading{Model: opusID, Effort: "xhigh"}))
	assert.Equal(t, "claude --resume {id} --effort xhigh --model=sonnet",
		applySessionFlags("claude --resume {id} --model=sonnet", sessionReading{Model: opusID, Effort: "xhigh"}))
	assert.Equal(t, "claude --resume {id} --model 'claude-opus-5-5' --effort low",
		applySessionFlags("claude --resume {id} --effort low", sessionReading{Model: opusID, Effort: "xhigh"}))
	assert.Equal(t, "claude --resume {id} --model a --effort low",
		applySessionFlags("claude --resume {id} --model a --effort low", opusXhigh))
}

// Each field on its own: only the known one is added. A value that would not be safe on a command line is dropped.
// Mutation: skip ValidModel / ValidEffort → the injection cases are red.
func TestApplySessionFlags_OnlyWhatIsKnownAndSafe(t *testing.T) {
	assert.Equal(t, "claude --resume {id} --model 'claude-opus-5-5'", applySessionFlags("claude --resume {id}", sessionReading{Model: opusID}))
	assert.Equal(t, "claude --resume {id} --effort high", applySessionFlags("claude --resume {id}", sessionReading{Effort: "high"}))
	assert.Equal(t, "claude --resume {id} --model 'claude-opus-5-5[1m]'", applySessionFlags("claude --resume {id}", sessionReading{Model: "claude-opus-5-5[1m]"}))
	for _, bad := range []sessionReading{
		{Model: "x'; rm -rf ~; '"}, {Model: "a b"}, {Model: "$(id)"}, {Model: "a\nb"}, {Effort: "ultra"}, {Effort: "high; ls"}, {Effort: "XHIGH"},
	} {
		assert.Equal(t, "claude --resume {id}", applySessionFlags("claude --resume {id}", bad), "%+v", bad)
	}
}

// usageOwners is the fixture's owner resolver that also answers the statusline's readings.
type usageOwners struct {
	agent.OwnerResolver
	usage map[string]agent.ContextUsage
}

func (u usageOwners) ContextUsage(sid string) (agent.ContextUsage, bool) {
	r, ok := u.usage[sid]
	return r, ok
}

func (e *handoffEnv) setUsage(sid, model, effort string) {
	e.m.owners = usageOwners{OwnerResolver: e.m.owners, usage: map[string]agent.ContextUsage{sid: {ModelID: model, Effort: effort}}}
}

// The reading: the execution's labels first, the statusline for what they lack, nothing when neither has it. Mutations: labels
// ignored → the label case is red; statusline ignored → the fallback is red; an invalid label used → red.
func TestReadingOf_LabelsThenTheStatusline(t *testing.T) {
	env := newHandoffEnv(t)
	env.setUsage(hoSessionID, "claude-sonnet-5-5", "medium")
	assert.Equal(t, sessionReading{Model: "claude-sonnet-5-5", Effort: "medium"}, env.m.readingOf(hoSessionID, ""), "statusline alone")
	assert.Equal(t, opusXhigh, env.m.readingOf(hoSessionID, `{"purdex.model":"claude-opus-5-5","purdex.effort":"xhigh"}`), "labels win")
	assert.Equal(t, sessionReading{Model: opusID, Effort: "medium"}, env.m.readingOf(hoSessionID, `{"purdex.model":"claude-opus-5-5"}`), "each field on its own")
	assert.Equal(t, sessionReading{Model: "claude-sonnet-5-5", Effort: "medium"}, env.m.readingOf(hoSessionID, `{"purdex.model":"a b","purdex.effort":"ultra"}`), "an invalid label is no label")
	assert.Equal(t, sessionReading{Model: "claude-sonnet-5-5", Effort: "medium"}, env.m.readingOf(hoSessionID, `not json`))
	assert.Equal(t, sessionReading{}, env.m.readingOf("another-session", ""), "no reading at all")
}

func TestReadingOf_NoReaderIsNoReading(t *testing.T) {
	env := newHandoffEnv(t) // the plain fixture owner resolver does not read the statusline
	assert.Equal(t, sessionReading{}, env.m.readingOf(hoSessionID, ""))
}

// ---- the three places a resume command is rendered ----

// terminal → worker: the reading is written on the execution as labels (the worker side keeps it for the way back), and a
// rejected delegate's rollback resumes with the flags. Mutations: no labels → red; rollback unflagged → red.
func TestHandoff_WritesTheReadingAndRollsBackWithIt(t *testing.T) {
	env := newHandoffEnv(t)
	env.setUsage(hoSessionID, opusID, "xhigh")
	env.svc.result = execution.Result{State: store.StateRejected, RejectReason: "no"}
	reviveCCAfterKeys(env)
	status, body := env.post(t, hoCode, goodBody())
	require.Equal(t, http.StatusConflict, status, "%v", body)
	reqs := env.svc.Requests()
	require.Len(t, reqs, 1)
	assert.Equal(t, opusID, reqs[0].Labels[handoffModelLabel])
	assert.Equal(t, "xhigh", reqs[0].Labels[handoffEffortLabel])
	assert.Equal(t, []string{"claude --resume " + hoSessionID + flagsOpus + "\n"}, rawKeysText(env.tmux))
}

// No reading: the labels are not there and the rollback is today's. Mutation: write empty labels → red.
func TestHandoff_WithoutAReadingNothingChanges(t *testing.T) {
	env := newHandoffEnv(t)
	env.svc.result = execution.Result{State: store.StateRejected, RejectReason: "no"}
	reviveCCAfterKeys(env)
	env.post(t, hoCode, goodBody())
	reqs := env.svc.Requests()
	require.Len(t, reqs, 1)
	_, hasModel := reqs[0].Labels[handoffModelLabel]
	_, hasEffort := reqs[0].Labels[handoffEffortLabel]
	assert.False(t, hasModel || hasEffort)
	assert.Equal(t, []string{"claude --resume " + hoSessionID + "\n"}, rawKeysText(env.tmux))
}

// worker → terminal: the labels the handoff wrote, else the statusline. Mutations: take-back unflagged → red; labels ignored
// → the label case is red.
func TestTakeback_ResumesWithTheReading(t *testing.T) {
	t.Run("from the execution's labels", func(t *testing.T) {
		env := newTakebackEnv(t)
		e := idleExec()
		e.Labels = `{"handoff_session":"` + hoCode + `","source":"purdex","purdex.model":"` + opusID + `","purdex.effort":"xhigh"}`
		env.store.results = []getResult{{exec: e}}
		status, body := env.post(t, hoCode, takebackBody())
		require.Equal(t, http.StatusOK, status, "%v", body)
		assert.Equal(t, []string{"claude --resume " + tbSessionID + flagsOpus + "\n"}, rawKeysText(env.tmux))
	})
	t.Run("from the statusline when the labels have none", func(t *testing.T) {
		env := newTakebackEnv(t)
		env.setUsage(tbSessionID, opusID, "xhigh")
		status, body := env.post(t, hoCode, takebackBody())
		require.Equal(t, http.StatusOK, status, "%v", body)
		assert.Equal(t, []string{"claude --resume " + tbSessionID + flagsOpus + "\n"}, rawKeysText(env.tmux))
	})
	t.Run("no reading: today's command", func(t *testing.T) {
		env := newTakebackEnv(t)
		status, _ := env.post(t, hoCode, takebackBody())
		require.Equal(t, http.StatusOK, status)
		assert.Equal(t, []string{"claude --resume " + tbSessionID + "\n"}, rawKeysText(env.tmux))
	})
}
