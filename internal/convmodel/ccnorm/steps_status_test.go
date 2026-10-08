package ccnorm

import (
	"testing"

	"github.com/wake/purdex/internal/convmodel"
)

func TestStatus_Matrix(t *testing.T) {
	const (
		running = convmodel.StepRunning
		done    = convmodel.StepDone
		failed  = convmodel.StepFailed
		denied  = convmodel.StepDenied
	)
	type row struct {
		name    string
		content string
		isErr   bool
		opts    []opt
		want    convmodel.StepStatus
		denial  string
	}
	rows := []row{
		{name: "ok", content: "fine", want: done},
		{name: "ok, empty text", content: "", want: done},
		// toolDenialKind: any value, with or without is_error (M-U1-7)
		{name: "user-rejected", content: refusal, isErr: true, opts: []opt{denialKind("user-rejected")}, want: denied, denial: "user-rejected"},
		{name: "user-rejected, no is_error", content: "x", opts: []opt{denialKind("user-rejected")}, want: denied, denial: "user-rejected"},
		{name: "permission-rule", content: "denied by rule", isErr: true, opts: []opt{denialKind("permission-rule")}, want: denied, denial: "permission-rule"},
		{name: "permission-rule, no is_error", content: "x", opts: []opt{denialKind("permission-rule")}, want: denied, denial: "permission-rule"},
		{name: "interrupted", content: "stopped", isErr: true, opts: []opt{denialKind("interrupted")}, want: denied, denial: "interrupted"},
		{name: "interrupted, no is_error", content: "stopped", opts: []opt{denialKind("interrupted")}, want: denied, denial: "interrupted"},
		{name: "cancelled", content: "cancelled", isErr: true, opts: []opt{denialKind("cancelled")}, want: denied, denial: "cancelled"},
		{name: "cancelled, no is_error", content: "x", opts: []opt{denialKind("cancelled")}, want: denied, denial: "cancelled"},
		{name: "a future denial kind passes through", content: "x", isErr: true, opts: []opt{denialKind("some-new-kind")}, want: denied, denial: "some-new-kind"},
		{name: "empty denial kind is absent", content: "boom", isErr: true, opts: []opt{denialKind("")}, want: failed},
		{name: "non-string denial kind is absent", content: "boom", isErr: true, opts: []opt{with("toolDenialKind", 5)}, want: failed},
		// text fallback for rows without the field (older versions)
		{name: "refusal text", content: refusal, isErr: true, want: denied, denial: "user-rejected"},
		{name: "interrupted-for-tool-use text", content: "[Request interrupted by user for tool use]", isErr: true, want: denied, denial: "user-rejected"},
		{name: "was rejected text", content: "Permission to use Bash was rejected", isErr: true, want: denied, denial: "user-rejected"},
		// failures
		{name: "exit code", content: "Exit code 1\nboom", isErr: true, want: failed},
		{name: "tool_use_error", content: "<tool_use_error>File does not exist.</tool_use_error>", isErr: true, want: failed},
		{name: "hook block", content: "PreToolUse:Bash hook blocking error from command: \"check.sh\": no", isErr: true, want: failed},
		{name: "empty is_error text", content: "", isErr: true, want: failed},
		// the field outranks the text, both ways
		{name: "field with a refusal text", content: refusal, isErr: true, opts: []opt{denialKind("permission-rule")}, want: denied, denial: "permission-rule"},
		{name: "field with a failure text", content: "Exit code 2", isErr: true, opts: []opt{denialKind("cancelled")}, want: denied, denial: "cancelled"},
		// text only counts on an error result
		{name: "non-error with refusal words", content: "the user doesn't want to proceed, they said", want: done},
	}
	for _, r := range rows {
		s := oneStep(t, "Bash", obj{"command": "x"}, resultRow("r1", 3, "toolu_1", r.content, r.isErr, r.opts...))
		if s.Status != r.want || s.Denial != r.denial {
			t.Errorf("%s: status %q denial %q, want %q %q", r.name, s.Status, s.Denial, r.want, r.denial)
		}
	}

	// no result: running while the turn is, else denied interrupted
	s := oneStep(t, "Bash", obj{"command": "x"}, nil)
	if s.Status != running || s.Denial != "" {
		t.Errorf("no result, running turn: %q %q", s.Status, s.Denial)
	}
	c := conv(t, userRow("u1", 1, "go"), toolCall("a1", 2, "toolu_1", "Bash", obj{"command": "x"}), turnDuration("d1", 3, 100))
	if s := stepNamed(t, c, "toolu_1"); s.Status != denied || s.Denial != "interrupted" {
		t.Errorf("no result, turn ended by turn_duration: %q %q", s.Status, s.Denial)
	}
	c = conv(t, userRow("u1", 1, "go"), toolCall("a1", 2, "toolu_1", "Bash", obj{"command": "x"}), userRow("u2", 9, "again"))
	if s := stepNamed(t, c, "toolu_1"); s.Status != denied || s.Denial != "interrupted" {
		t.Errorf("no result, turn closed by the next turn: %q %q", s.Status, s.Denial)
	}
	c = conv(t, userRow("u1", 1, "go"), toolCall("a1", 2, "toolu_1", "Bash", obj{"command": "x"}), interruptRow("i1", 3, false))
	if s := stepNamed(t, c, "toolu_1"); s.Status != denied || s.Denial != "interrupted" {
		t.Errorf("no result, interrupt marker: %q %q", s.Status, s.Denial)
	}
}

func TestStatus_MultiResultRowWithDenialKindIsCountedNotAttributed(t *testing.T) {
	multi := func() []byte {
		o := common("user", "r1", 3)
		o["message"] = obj{"role": "user", "content": []obj{
			{"type": "tool_result", "tool_use_id": "toolu_1", "content": "a"},
			{"type": "tool_result", "tool_use_id": "toolu_2", "content": "Exit code 1", "is_error": true},
		}}
		return line(o, denialKind("user-rejected"))
	}
	n := norm(t, userRow("u1", 1, "go"),
		toolCall("a1", 2, "toolu_1", "Bash", obj{"command": "a"}), toolCall("a2", 2.1, "toolu_2", "Bash", obj{"command": "b"}),
		multi())
	c := validated(t, n)
	for _, id := range []string{"toolu_1", "toolu_2"} {
		if s := stepNamed(t, c, id); s.Status == convmodel.StepDenied || s.Denial != "" {
			t.Errorf("%s: the row's field was attributed: %q %q", id, s.Status, s.Denial)
		}
	}
	if got := n.Stats().Skipped["multi_result_denial"]; got != 1 {
		t.Errorf("multi_result_denial = %d, want 1", got)
	}

	// a single-result row with the field is unchanged, and not counted
	n = norm(t, userRow("u1", 1, "go"), toolCall("a1", 2, "toolu_1", "Bash", obj{"command": "a"}),
		resultRow("r1", 3, "toolu_1", "x", false, denialKind("user-rejected")))
	if s := stepNamed(t, validated(t, n), "toolu_1"); s.Status != convmodel.StepDenied || s.Denial != "user-rejected" {
		t.Errorf("single result: %q %q", s.Status, s.Denial)
	}
	if got := n.Stats().Skipped["multi_result_denial"]; got != 0 {
		t.Errorf("single result counted: %d", got)
	}

	// several results without the field: nothing to count
	o := common("user", "r2", 4)
	o["message"] = obj{"role": "user", "content": []obj{
		{"type": "tool_result", "tool_use_id": "toolu_1", "content": "a"},
		{"type": "tool_result", "tool_use_id": "toolu_2", "content": "b"},
	}}
	n = norm(t, userRow("u1", 1, "go"),
		toolCall("a1", 2, "toolu_1", "Bash", obj{"command": "a"}), toolCall("a2", 2.1, "toolu_2", "Bash", obj{"command": "b"}),
		line(o))
	if got := n.Stats().Skipped["multi_result_denial"]; got != 0 {
		t.Errorf("no field, counted: %d", got)
	}
}

func TestStatus_DenialKindOnRowWithSeveralResultsIsNotApplied(t *testing.T) {
	// the row-level toolDenialKind cannot say which of two results it is for
	o := common("user", "r1", 3)
	o["message"] = obj{"role": "user", "content": []obj{
		{"type": "tool_result", "tool_use_id": "toolu_1", "content": "a"},
		{"type": "tool_result", "tool_use_id": "toolu_2", "content": "Exit code 1", "is_error": true},
	}}
	c := conv(t, userRow("u1", 1, "go"),
		toolCall("a1", 2, "toolu_1", "Bash", obj{"command": "a"}), toolCall("a2", 2.1, "toolu_2", "Bash", obj{"command": "b"}),
		line(o, denialKind("user-rejected")))
	if s := stepNamed(t, c, "toolu_1"); s.Status != convmodel.StepDone {
		t.Errorf("toolu_1: %q %q", s.Status, s.Denial)
	}
	if s := stepNamed(t, c, "toolu_2"); s.Status != convmodel.StepFailed {
		t.Errorf("toolu_2: %q %q", s.Status, s.Denial)
	}
}

func TestStatus_LateResultReplacesStale(t *testing.T) {
	// a background tool's result arrives after its turn ended and the next
	// turn began: the step was denied{interrupted}, the late result makes it
	// done — in the older turn, by the row's offset
	n := norm(t,
		userRow("u1", 1, "run it"),
		toolCall("a1", 2, "toolu_1", "Bash", obj{"command": "sleep 20", "run_in_background": true}),
		assistantText("a2", 3, "started"),
		turnDuration("d1", 4, 100),
		userRow("u2", 5, "next"),
	)
	c := validated(t, n)
	if s := stepNamed(t, c, "toolu_1"); s.Status != convmodel.StepDenied || s.Denial != "interrupted" {
		t.Fatalf("before the result: %q %q", s.Status, s.Denial)
	}
	off := n.Next()
	ch, err := n.Feed(off, resultRow("r1", 20, "toolu_1", "done after all", false))
	if err != nil {
		t.Fatal(err)
	}
	c = validated(t, n)
	s := stepNamed(t, c, "toolu_1")
	if s.Status != convmodel.StepDone || s.Denial != "" {
		t.Errorf("after the late result: %q %q", s.Status, s.Denial)
	}
	if len(c.Turns) != 2 || len(stepsIn(t, c, 0)) != 1 {
		t.Errorf("the result opened a turn or moved the step\n%s", dump(c))
	}
	found := false
	for _, x := range ch {
		if x.TurnID == "u1" && x.ItemID == "toolu_1" {
			found = true
			if x.Offset != off {
				t.Errorf("change offset %d, want the result row's %d", x.Offset, off)
			}
		}
	}
	if !found {
		t.Errorf("no change for the step: %+v", ch)
	}

	// the field can also keep it denied, with the field's own value
	n = norm(t,
		userRow("u1", 1, "run it"), toolCall("a1", 2, "toolu_1", "Bash", obj{"command": "x"}),
		turnDuration("d1", 4, 100), userRow("u2", 5, "next"),
		resultRow("r1", 20, "toolu_1", refusal, true, denialKind("user-rejected")),
	)
	if s := stepNamed(t, validated(t, n), "toolu_1"); s.Status != convmodel.StepDenied || s.Denial != "user-rejected" {
		t.Errorf("late refusal: %q %q", s.Status, s.Denial)
	}
}

func TestPosition_LateToolResultMovesUpdatedNotCreated(t *testing.T) {
	n := norm(t,
		userRow("u1", 1, "go"), toolCall("a1", 2, "toolu_1", "Bash", obj{"command": "x"}),
		turnDuration("d1", 3, 1), userRow("u2", 4, "next"), assistantText("a2", 5, "hi"),
	)
	before, ok := n.Position("u1", "toolu_1")
	if !ok {
		t.Fatal("no position for the step")
	}
	turnBefore, _ := n.Position("u1", "")
	off := n.Next()
	feed(t, n, resultRow("r1", 9, "toolu_1", "ok", false))
	after, _ := n.Position("u1", "toolu_1")
	turnAfter, _ := n.Position("u1", "")
	if after.Created != before.Created || after.Updated != off {
		t.Errorf("step position %+v → %+v, want Created kept and Updated = %d", before, after, off)
	}
	if turnAfter.Created != turnBefore.Created || turnAfter.Updated != off {
		t.Errorf("turn position %+v → %+v, want Updated = %d", turnBefore, turnAfter, off)
	}
	if p, _ := n.Position("u2", ""); p.Updated == off {
		t.Errorf("the newer turn was marked updated by an older step's result")
	}
}

func TestDuration_FromRowTimes(t *testing.T) {
	s := oneStep(t, "Bash", obj{"command": "x"}, resultRow("r1", 5.5, "toolu_1", "ok", false))
	if s.DurationMS == nil || *s.DurationMS != 3500 {
		t.Errorf("duration = %v, want 3500 (2 s → 5.5 s)", s.DurationMS)
	}
	if s := oneStep(t, "Bash", obj{"command": "x"}, nil); s.DurationMS != nil {
		t.Errorf("duration without a result = %v", *s.DurationMS)
	}
	// a result row without a time gives no duration
	s = oneStep(t, "Bash", obj{"command": "x"}, resultRow("r1", 5, "toolu_1", "ok", false, without("timestamp")))
	if s.DurationMS != nil {
		t.Errorf("duration without a result time = %v", *s.DurationMS)
	}
	// a clock that went backwards is 0, not negative
	s = oneStep(t, "Bash", obj{"command": "x"}, resultRow("r1", 1, "toolu_1", "ok", false))
	if s.DurationMS == nil || *s.DurationMS != 0 {
		t.Errorf("duration = %v, want 0", s.DurationMS)
	}
}

func TestSteps_ResultRowsPairAcrossRowsAndOpenNoTurn(t *testing.T) {
	// a file that starts mid-conversation: the tool_use row opens a userless
	// turn (id = its uuid); a result with no known step is skipped and counted
	n := norm(t, resultRow("r0", 1, "toolu_gone", "orphan", false))
	if len(n.Conversation().Turns) != 0 || n.Stats().Skipped["step:orphan_result"] != 1 {
		t.Fatalf("an orphan result opened a turn or was not counted: %v", n.Stats().Skipped)
	}
	feed(t, n,
		toolCall("a1", 2, "toolu_1", "Bash", obj{"command": "ls"}),
		resultRow("r1", 3, "toolu_1", "file", false),
		assistantText("a2", 4, "done"),
		turnDuration("d1", 5, 3000),
	)
	c := validated(t, n)
	if len(c.Turns) != 1 || c.Turns[0].ID != "a1" {
		t.Fatalf("turns:\n%s", dump(c))
	}
	if got := sigs(c.Turns[0].Items); !equalStrings(got, []string{"step", "agent_text:done"}) {
		t.Errorf("items = %v", got)
	}
	if s := stepNamed(t, c, "toolu_1"); s.Status != convmodel.StepDone {
		t.Errorf("status = %q", s.Status)
	}
	if c.Turns[0].Outcome != convmodel.OutcomeDone {
		t.Errorf("outcome = %q", c.Turns[0].Outcome)
	}
}

func TestSteps_TurnCloseDeniesAndReopenRestores(t *testing.T) {
	// the turn ending changes its result-less steps and reports them
	n := norm(t, userRow("u1", 1, "go"), toolCall("a1", 2, "toolu_1", "Bash", obj{"command": "x"}))
	if s := stepNamed(t, validated(t, n), "toolu_1"); s.Status != convmodel.StepRunning {
		t.Fatalf("running turn: %q", s.Status)
	}
	ch := feed(t, n, turnDuration("d1", 3, 5))
	hit := false
	for _, x := range ch {
		hit = hit || (x.TurnID == "u1" && x.ItemID == "toolu_1")
	}
	if !hit {
		t.Errorf("closing the turn did not report its step: %+v", ch)
	}
	if s := stepNamed(t, validated(t, n), "toolu_1"); s.Status != convmodel.StepDenied || s.Denial != "interrupted" {
		t.Errorf("closed turn: %q %q", s.Status, s.Denial)
	}
	// a step whose result came is not touched by the close
	n = norm(t, userRow("u1", 1, "go"), toolCall("a1", 2, "toolu_1", "Bash", obj{"command": "x"}),
		resultRow("r1", 3, "toolu_1", "ok", false), turnDuration("d1", 4, 5))
	if s := stepNamed(t, validated(t, n), "toolu_1"); s.Status != convmodel.StepDone {
		t.Errorf("resulted step after the close: %q", s.Status)
	}
}

func TestSteps_ApiErrorClearedByToolUseRowReopensSteps(t *testing.T) {
	// an API error closes the turn as failed; a real tool_use row after it
	// clears the error, so the turn is running and its step is too
	c := conv(t, userRow("u1", 1, "go"), apiErrorRow("e1", 2, "server_error", "oops"),
		toolCall("a1", 3, "toolu_1", "Bash", obj{"command": "x"}))
	if c.Turns[0].Outcome != convmodel.OutcomeRunning {
		t.Fatalf("outcome = %q", c.Turns[0].Outcome)
	}
	if s := stepNamed(t, c, "toolu_1"); s.Status != convmodel.StepRunning || s.Denial != "" {
		t.Errorf("step = %q %q", s.Status, s.Denial)
	}
}
