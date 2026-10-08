package ccnorm

import (
	"reflect"
	"testing"

	"github.com/wake/purdex/internal/convmodel"
)

func endedAt(t testing.TB, tr convmodel.Turn) int64 {
	t.Helper()
	if tr.EndedAt == nil {
		t.Fatalf("turn %q has no ended_at (outcome %q)", tr.ID, tr.Outcome)
	}
	return *tr.EndedAt
}

func TestOutcome_DoneByTurnDuration(t *testing.T) {
	c := conv(t,
		userRow("u1", 1, "hi"), assistantText("a1", 2, "yo"),
		stopHookSummary("h1", 3), turnDuration("d1", 3.1, 2100),
	)
	tr := c.Turns[0]
	if tr.Outcome != convmodel.OutcomeDone || endedAt(t, tr) != ms(3.1) {
		t.Errorf("outcome = %q ended_at = %v, want done at the turn_duration row's time %d", tr.Outcome, tr.EndedAt, ms(3.1))
	}
}

func TestOutcome_InterruptedByMarkerWithoutTurnDuration(t *testing.T) {
	// f2b lines 82-86: Esc while the model writes — aborted assistant row,
	// the marker, and no turn_duration; the queued prompt then opens a turn
	c := conv(t,
		userRow("u1", 1, "write a story"),
		assistantThinking("a1", 2, "", 3871),
		assistantText("a2", 2.5, "# a kite", with("isAbortedMidStream", true)),
		interruptRow("i1", 3, false),
		userRow("u2", 3.1, "QUEUED4", promptSource("queued")),
	)
	if len(c.Turns) != 2 {
		t.Fatalf("turns = %d: the marker must not open one\n%s", len(c.Turns), dump(c))
	}
	tr := c.Turns[0]
	if tr.Outcome != convmodel.OutcomeInterrupted || endedAt(t, tr) != ms(3) {
		t.Errorf("outcome = %q ended_at = %v, want interrupted at the marker's time", tr.Outcome, tr.EndedAt)
	}
	items := tr.Items
	if last := items[len(items)-1]; last.System == nil || last.System.Kind != convmodel.SystemInterrupted || last.System.ID != "i1" {
		t.Errorf("last item = %+v, want the interrupted system item with the marker's uuid", last)
	}
	for _, it := range items {
		if it.User != nil && it.User.Text != "write a story" {
			t.Errorf("the marker text became a user item: %+v", it.User)
		}
	}
	// even while it is still the last turn
	c = conv(t, userRow("u1", 1, "go"), assistantText("a1", 2, "wr"), interruptRow("i1", 3, false))
	if c.Turns[0].Outcome != convmodel.OutcomeInterrupted {
		t.Errorf("last turn with a marker: %q", c.Turns[0].Outcome)
	}
}

func TestOutcome_RefusalWithTurnDurationIsInterrupted(t *testing.T) {
	// f3 lines 69-79: the refusal writes the tool result, the "for tool use"
	// marker (no interruptedMessageId) and then a turn_duration — the marker
	// wins (lead ruling D5)
	c := conv(t,
		userRow("u1", 1, "run touch"),
		assistantRow("a1", 2, "claude-opus-5-5", toolUseBlock("toolu_1", "Bash", obj{"command": "touch x"})),
		toolResultRow("r1", 3, "toolu_1", "The user doesn't want to proceed with this tool use.",
			with("toolDenialKind", "user-rejected")),
		interruptRow("i1", 3.1, true),
		turnDuration("d1", 3.2, 1681),
	)
	tr := c.Turns[0]
	if tr.Outcome != convmodel.OutcomeInterrupted {
		t.Errorf("outcome = %q, want interrupted", tr.Outcome)
	}
	if endedAt(t, tr) != ms(3.2) {
		t.Errorf("ended_at = %d, want the turn_duration time %d", endedAt(t, tr), ms(3.2))
	}
}

func TestOutcome_FailedByApiError(t *testing.T) {
	c := conv(t,
		userRow("u1", 1, "hi"),
		apiErrorRow("e1", 2, "rate_limit", "You've hit your limit"),
		turnDuration("d1", 2.1, 900),
	)
	tr := c.Turns[0]
	if tr.Outcome != convmodel.OutcomeFailed {
		t.Fatalf("outcome = %q, want failed", tr.Outcome)
	}
	if tr.Error == nil || tr.Error.Kind != "rate_limit" || tr.Error.Message != "You've hit your limit" {
		t.Errorf("error = %+v", tr.Error)
	}
	for _, it := range tr.Items {
		if it.AgentText != nil {
			t.Errorf("the synthetic row became agent text: %+v", it.AgentText)
		}
	}
	if endedAt(t, tr) != ms(2.1) {
		t.Errorf("ended_at = %d", endedAt(t, tr))
	}

	// the error is the LAST assistant row: a later reply clears it (a retry)
	c = conv(t, userRow("u1", 1, "hi"), apiErrorRow("e1", 2, "server_error", "oops"), assistantText("a1", 3, "back"), turnDuration("d1", 4, 1))
	if tr := c.Turns[0]; tr.Outcome != convmodel.OutcomeDone || tr.Error != nil {
		t.Errorf("after a retry: outcome %q error %+v", tr.Outcome, tr.Error)
	}

	// failed before any prompt: a userless turn
	c = conv(t, apiErrorRow("e1", 2, "authentication_failed", "log in"))
	if len(c.Turns) != 1 || c.Turns[0].Outcome != convmodel.OutcomeFailed || c.Turns[0].ID != "e1" {
		t.Errorf("userless failed turn: %s", dump(c))
	}
}

func TestOutcome_ApiErrorThenToolUseRowIsNotFailed(t *testing.T) {
	// a tool_use row is a main-thread assistant row (its step arrives in
	// U1-4c): as the turn's last assistant row it clears the API error
	c := conv(t,
		userRow("u1", 1, "hi"),
		apiErrorRow("e1", 2, "server_error", "oops"),
		assistantRow("a1", 3, "claude-opus-5-5", toolUseBlock("toolu_1", "Bash", obj{"command": "ls"})),
		turnDuration("d1", 4, 1),
	)
	tr := c.Turns[0]
	if tr.Outcome != convmodel.OutcomeDone || tr.Error != nil {
		t.Errorf("outcome %q error %+v, want done without error", tr.Outcome, tr.Error)
	}
}

func TestOutcome_OpenNonLastTurnIsInterrupted(t *testing.T) {
	// a killed process: no turn_duration, no marker, and the next turn began
	c := conv(t, userRow("u1", 1, "go"), assistantText("a1", 2, "wr"), userRow("u2", 9, "again"))
	tr := c.Turns[0]
	if tr.Outcome != convmodel.OutcomeInterrupted || endedAt(t, tr) != ms(2) {
		t.Errorf("outcome = %q ended_at = %v, want interrupted at the last row's time", tr.Outcome, tr.EndedAt)
	}
	if c.Turns[1].Outcome != convmodel.OutcomeRunning || c.Turns[1].EndedAt != nil {
		t.Errorf("the new last turn: %q %v", c.Turns[1].Outcome, c.Turns[1].EndedAt)
	}
}

func TestOutcome_LastOpenTurnRunningWhenLive(t *testing.T) {
	n := norm(t, userRow("u1", 1, "go"), assistantText("a1", 2, "wr"))
	for i := 0; i < 2; i++ { // asking never closes it
		tr := validated(t, n).Turns[0]
		if tr.Outcome != convmodel.OutcomeRunning || tr.EndedAt != nil {
			t.Fatalf("ask %d: outcome %q ended_at %v, want running", i, tr.Outcome, tr.EndedAt)
		}
	}
}

func TestOutcome_SetLiveFalseClosesLastTurnAndReportsChanges(t *testing.T) {
	n := norm(t, userRow("u1", 1, "go"), assistantText("a1", 2, "wr"))
	before, _ := n.Position("u1", "")
	ch := n.SetLive(false)
	if want := []Change{{"u1", "", -1}}; !reflect.DeepEqual(ch, want) {
		t.Errorf("changes = %v, want %v", ch, want)
	}
	tr := validated(t, n).Turns[0]
	if tr.Outcome != convmodel.OutcomeDone || endedAt(t, tr) != ms(2) {
		t.Errorf("outcome = %q ended_at = %v, want done at the last row's time", tr.Outcome, tr.EndedAt)
	}
	if after, _ := n.Position("u1", ""); after != before {
		t.Errorf("SetLive moved the position %+v → %+v: it is not a row", before, after)
	}
	if again := n.SetLive(false); len(again) != 0 {
		t.Errorf("SetLive(false) twice reported %v", again)
	}
}

func TestOutcome_SetLiveTrueReopensTurnWithoutEndMarker(t *testing.T) {
	n := norm(t, userRow("u1", 1, "go"), assistantText("a1", 2, "wr"))
	n.SetLive(false)
	ch := n.SetLive(true)
	if want := []Change{{"u1", "", -1}}; !reflect.DeepEqual(ch, want) {
		t.Errorf("changes = %v, want %v", ch, want)
	}
	if tr := validated(t, n).Turns[0]; tr.Outcome != convmodel.OutcomeRunning || tr.EndedAt != nil {
		t.Errorf("outcome = %q ended_at = %v, want running", tr.Outcome, tr.EndedAt)
	}
	// and a row that arrives after the reopen is taken as usual
	feed(t, n, assistantText("a2", 3, "more"), turnDuration("d1", 4, 3000))
	if tr := validated(t, n).Turns[0]; tr.Outcome != convmodel.OutcomeDone {
		t.Errorf("outcome after turn_duration = %q", tr.Outcome)
	}
}

func TestOutcome_SetLiveDoesNotTouchEndedTurn(t *testing.T) {
	n := norm(t,
		userRow("u1", 1, "go"), assistantText("a1", 2, "ok"), turnDuration("d1", 3, 2000),
		userRow("u2", 4, "again"), assistantText("a2", 5, "wr"), interruptRow("i1", 6, false),
	)
	before := jsonOf(t, n.Conversation())
	if ch := n.SetLive(false); len(ch) != 0 {
		t.Errorf("SetLive(false) reported %v for turns that already ended", ch)
	}
	if ch := n.SetLive(true); len(ch) != 0 {
		t.Errorf("SetLive(true) reported %v for turns that already ended", ch)
	}
	if got := jsonOf(t, n.Conversation()); got != before {
		t.Error("SetLive changed an ended turn")
	}
}

func TestOutcome_LocalCommandTurnIsDoneWithoutTurnDuration(t *testing.T) {
	n := norm(t, localCommandRow("lc1", 1, "<command-name>/usage</command-name>"), localCommandRow("lc2", 1.1, "<local-command-stdout>ok</local-command-stdout>"))
	if tr := validated(t, n).Turns[0]; tr.Outcome != convmodel.OutcomeDone || tr.EndedAt == nil {
		t.Errorf("local command as the last turn: %q %v", tr.Outcome, tr.EndedAt)
	}
}

func TestOutcome_EndedAtNeverBeforeStart(t *testing.T) {
	// a turn_duration row without a timestamp parses to time 0
	c := conv(t, userRow("u1", 5, "go"), line(func() obj {
		o := common("system", "d1", 0)
		o["subtype"] = "turn_duration"
		delete(o, "timestamp")
		return o
	}()))
	tr := c.Turns[0]
	if tr.Outcome != convmodel.OutcomeDone || endedAt(t, tr) < tr.StartedAt {
		t.Errorf("outcome %q ended_at %v started_at %d", tr.Outcome, tr.EndedAt, tr.StartedAt)
	}
}

func TestOutcome_OrphanTurnDurationIgnored(t *testing.T) {
	n := norm(t, turnDuration("d1", 1, 5))
	if len(n.Conversation().Turns) != 0 || n.Stats().Skipped["orphan_turn_duration"] != 1 {
		t.Errorf("skipped = %v", n.Stats().Skipped)
	}
}
