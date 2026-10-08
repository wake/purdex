package ccnorm

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/wake/purdex/internal/convmodel"
)

// ---- agent text and thinking ----------------------------------------------

func TestAgentText_OneItemPerBlock(t *testing.T) {
	// the rows of one API message share message.id; each is its own item
	c := conv(t,
		userRow("u1", 1, "hi"),
		assistantThinking("a1", 2, "", 653),
		assistantText("a2", 3, "first"),
		assistantText("a3", 4, "second"),
		turnDuration("d1", 5, 1),
	)
	got := sigs(c.Turns[0].Items)
	want := []string{"user:user:hi", `thinking:"":653`, "agent_text:first", "agent_text:second"}
	if !equalStrings(got, want) {
		t.Errorf("items = %v, want %v", got, want)
	}
	if c.Turns[0].Items[2].AgentText.At != ms(3) {
		t.Errorf("at = %d", c.Turns[0].Items[2].AgentText.At)
	}
}

func TestAgentText_SyntheticRowNeverText(t *testing.T) {
	n := norm(t, userRow("u1", 1, "hi"),
		assistantRow("s1", 2, "<synthetic>", textBlock("No response requested.")))
	c := validated(t, n)
	if len(c.Turns[0].Items) != 1 || n.Stats().Skipped["synthetic"] != 1 {
		t.Errorf("synthetic row: items %v skipped %v", sigs(c.Turns[0].Items), n.Stats().Skipped)
	}
}

func TestAgentText_EmptyTextBlockDropped(t *testing.T) {
	c := conv(t, userRow("u1", 1, "hi"), assistantText("a1", 2, ""))
	if len(c.Turns[0].Items) != 1 {
		t.Errorf("items = %v", sigs(c.Turns[0].Items))
	}
}

func TestAgentText_CappedAt64KiB(t *testing.T) {
	long := strings.Repeat("界", 30000)
	c := conv(t, userRow("u1", 1, "hi"), assistantText("a1", 2, long))
	a := c.Turns[0].Items[1].AgentText
	if !a.Truncated || len(a.Markdown) > convmodel.MaxText || len(a.Markdown) < convmodel.MaxText-3 || !utf8.ValidString(a.Markdown) {
		t.Errorf("len %d truncated %v", len(a.Markdown), a.Truncated)
	}
}

func TestThinking_EmptyTextWithDuration(t *testing.T) {
	c := conv(t, userRow("u1", 1, "hi"), assistantThinking("a1", 2, "", 653))
	th := c.Turns[0].Items[1].Thinking
	if th == nil || th.Text != "" || th.DurationMS != 653 || th.ID != "a1" || th.At != ms(2) {
		t.Fatalf("thinking = %+v", th)
	}
	if got := jsonOf(t, c.Turns[0].Items[1]); strings.Contains(got, `"text"`) {
		t.Errorf("an empty thinking text must be omitted: %s", got)
	}
}

func TestThinking_EmptyWithoutDurationDropped(t *testing.T) {
	n := norm(t, assistantThinking("a0", 0.5, "", 0))
	if len(n.Conversation().Turns) != 0 {
		t.Error("a droppable thinking row opened a userless turn")
	}
	c := conv(t, userRow("u1", 1, "hi"), assistantThinking("a1", 2, "", 0))
	if len(c.Turns[0].Items) != 1 {
		t.Errorf("items = %v", sigs(c.Turns[0].Items))
	}
}

func TestThinking_TextKept(t *testing.T) {
	c := conv(t, userRow("u1", 1, "hi"), assistantThinking("a1", 2, "let me think", 0))
	if th := c.Turns[0].Items[1].Thinking; th == nil || th.Text != "let me think" || th.DurationMS != 0 {
		t.Errorf("thinking = %+v", th)
	}
}

// ---- system items ---------------------------------------------------------

func TestSystem_InterruptedReplacesMarker(t *testing.T) {
	c := conv(t, userRow("u1", 1, "go"), interruptRow("i1", 2, false))
	got := sigs(c.Turns[0].Items)
	if !equalStrings(got, []string{"user:user:go", "system:interrupted"}) {
		t.Errorf("items = %v", got)
	}
	for _, tool := range []bool{false, true} {
		c = conv(t, userRow("u1", 1, "go"), interruptRow("i1", 2, tool))
		if s := c.Turns[0].Items[1].System; s == nil || s.Kind != convmodel.SystemInterrupted || s.ID != "i1" || s.At != ms(2) {
			t.Errorf("tool=%v: system = %+v", tool, s)
		}
	}
}

func TestSystem_Compacted(t *testing.T) {
	c := conv(t,
		userRow("u1", 1, "/compact"), compactBoundary("cb1", 2, "manual"), compactSummaryRow("cs1", 2.1), turnDuration("d1", 3, 1),
	)
	items := c.Turns[0].Items
	if len(items) != 2 || items[1].System == nil || items[1].System.Kind != convmodel.SystemCompacted {
		t.Fatalf("items = %v", sigs(items))
	}
	if s := items[1].System; s.ID != "cb1" || string(s.Detail) != `{"trigger":"manual"}` {
		t.Errorf("system = %+v detail %s", s, s.Detail)
	}
}

func TestTurns_CompactBoundaryBetweenTurnsJoinsPrevious(t *testing.T) {
	c := conv(t,
		userRow("u1", 1, "go"), assistantText("a1", 2, "ok"), turnDuration("d1", 3, 1),
		compactBoundary("cb1", 4, "auto"), compactSummaryRow("cs1", 4.1),
		userRow("u2", 5, "next"),
	)
	if len(c.Turns) != 2 {
		t.Fatalf("turns = %d, want 2: a compact_boundary opens no turn\n%s", len(c.Turns), dump(c))
	}
	if got := sigs(c.Turns[0].Items); got[len(got)-1] != "system:compacted" {
		t.Errorf("turn 0 items = %v, want the compacted item last", got)
	}
	if len(c.Turns[1].Items) != 1 {
		t.Errorf("turn 1 items = %v", sigs(c.Turns[1].Items))
	}

	// with no turn yet it opens one with no user item
	c = conv(t, compactBoundary("cb1", 1, "auto"), userRow("u1", 2, "go"))
	if len(c.Turns) != 2 || c.Turns[0].ID != "cb1" || len(c.Turns[0].Items) != 1 {
		t.Errorf("leading compact_boundary: %s", dump(c))
	}
}

func TestSystem_HandoffOnEntrypointChange(t *testing.T) {
	c := conv(t,
		userRow("u1", 1, "in the terminal"), assistantText("a1", 2, "ok", entrypoint("cli")), turnDuration("d1", 3, 1),
		userRow("u2", 4, "in an execution", entrypoint("sdk-cli")), assistantText("a2", 5, "ok", entrypoint("sdk-cli")), turnDuration("d2", 6, 1, entrypoint("sdk-cli")),
		userRow("u3", 7, "same again", entrypoint("sdk-cli")),
		userRow("u4", 8, "back", entrypoint("cli")),
	)
	if len(c.Turns) != 4 {
		t.Fatalf("turns = %d", len(c.Turns))
	}
	handoff := func(i int) *convmodel.System {
		for _, it := range c.Turns[i].Items {
			if it.System != nil && it.System.Kind == convmodel.SystemHandoff {
				return it.System
			}
		}
		return nil
	}
	if handoff(0) != nil || handoff(2) != nil {
		t.Error("a handoff without a change of entrypoint")
	}
	if h := handoff(1); h == nil || h.ID != "u2#handoff" || string(h.Detail) != `{"to":"execution"}` || h.At != ms(4) {
		t.Errorf("turn 1 handoff = %+v", h)
	}
	if h := handoff(3); h == nil || h.ID != "u4#handoff" || string(h.Detail) != `{"to":"terminal"}` {
		t.Errorf("turn 3 handoff = %+v", h)
	}
	if c.Turns[1].Items[0].User == nil {
		t.Error("the user item must stay the first item of its turn")
	}
}

func TestSystem_ModelChangedBetweenTurns(t *testing.T) {
	asst := func(uuid string, sec float64, model string) []byte {
		return assistantRow(uuid, sec, model, textBlock("ok"))
	}
	c := conv(t,
		userRow("u1", 1, "a"), asst("a1", 2, "claude-opus-5-5"), turnDuration("d1", 3, 1),
		userRow("u2", 4, "b"), asst("a2", 5, "claude-opus-5-5"), turnDuration("d2", 6, 1),
		userRow("u3", 7, "c"), asst("a3", 8, "claude-sonnet-5-5"), asst("a4", 8.5, "claude-haiku-5-5"), turnDuration("d3", 9, 1),
	)
	model := func(i int) *convmodel.System {
		for _, it := range c.Turns[i].Items {
			if it.System != nil && it.System.Kind == convmodel.SystemModelChanged {
				return it.System
			}
		}
		return nil
	}
	if model(0) != nil || model(1) != nil {
		t.Error("model_changed without a change (the first turn has no previous one)")
	}
	m := model(2)
	if m == nil || m.ID != "u3#model" || string(m.Detail) != `{"model":"claude-sonnet-5-5"}` {
		t.Fatalf("turn 2 model_changed = %+v", m)
	}
	// at the first main-thread reply, after the prompt
	if got := sigs(c.Turns[2].Items); got[0] != "user:user:c" {
		t.Errorf("items = %v", got)
	}
	if c.Usage == nil || c.Usage.Model != "claude-haiku-5-5" {
		t.Errorf("usage = %+v", c.Usage)
	}
}

func TestSystem_ModelChangedIgnoresSynthetic(t *testing.T) {
	c := conv(t,
		userRow("u1", 1, "a"), assistantRow("a1", 2, "claude-opus-5-5", textBlock("ok")), turnDuration("d1", 3, 1),
		userRow("u2", 4, "b"), apiErrorRow("e1", 5, "rate_limit", "limit"), turnDuration("d2", 6, 1),
		userRow("u3", 7, "c"), assistantRow("a3", 8, "claude-opus-5-5", textBlock("ok")),
	)
	for i, tr := range c.Turns {
		for _, it := range tr.Items {
			if it.System != nil && it.System.Kind == convmodel.SystemModelChanged {
				t.Errorf("turn %d got model_changed from a <synthetic> row", i)
			}
		}
	}
}

func TestSystem_LocalCommandOutput(t *testing.T) {
	c := conv(t,
		localCommandRow("lc1", 1, "<command-name>/usage</command-name>"),
		localCommandRow("lc2", 1.1, "<local-command-stdout>a < b & c</local-command-stdout>"),
	)
	s := c.Turns[0].Items[1].System
	if s == nil || s.Kind != convmodel.SystemCommandOutput || string(s.Detail) != `{"text":"a < b & c"}` {
		t.Errorf("system = %+v detail %s", s, s.Detail)
	}
	// an empty output adds nothing
	c = conv(t, localCommandRow("lc1", 1, "<command-name>/clear</command-name>"), localCommandRow("lc2", 1.1, "<local-command-stdout></local-command-stdout>"))
	if len(c.Turns[0].Items) != 1 {
		t.Errorf("items = %v", sigs(c.Turns[0].Items))
	}
	// other local_command rows are skipped and counted
	n := norm(t, localCommandRow("lc3", 1, "<local-command-stderr>x</local-command-stderr>"), localCommandRow("lc4", 2, "<command-name>nope"))
	if len(n.Conversation().Turns) != 0 || n.Stats().Skipped["local_command:other"] != 2 {
		t.Errorf("skipped = %v", n.Stats().Skipped)
	}
}

func TestSystem_OtherSubtypesSkipped(t *testing.T) {
	n := norm(t, userRow("u1", 1, "go"), stopHookSummary("h1", 2))
	if n.Stats().Skipped["system:stop_hook_summary"] != 1 || len(n.Conversation().Turns[0].Items) != 1 {
		t.Errorf("skipped = %v", n.Stats().Skipped)
	}
}

// ---- title and usage ------------------------------------------------------

func TestTitle_CustomOverAi(t *testing.T) {
	cases := []struct {
		name  string
		lines [][]byte
		want  string
	}{
		{"none", nil, ""},
		{"ai only", [][]byte{aiTitle("first"), aiTitle("second")}, "second"},
		{"custom beats a later ai", [][]byte{aiTitle("ai"), customTitle("mine"), aiTitle("newer ai")}, "mine"},
		{"last custom wins", [][]byte{customTitle("one"), customTitle("two")}, "two"},
		{"blank values ignored", [][]byte{aiTitle("ai"), customTitle("  "), aiTitle(""), customTitle("")}, "ai"},
		{"trimmed", [][]byte{customTitle("  My name  ")}, "My name"},
	}
	for _, tc := range cases {
		if got := norm(t, tc.lines...).Conversation().Title; got != tc.want {
			t.Errorf("%s: title = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestUsage_ModelAndEffortFromLastMainRow(t *testing.T) {
	if u := norm(t, userRow("u1", 1, "hi")).Conversation().Usage; u != nil {
		t.Errorf("usage with no assistant row = %+v", u)
	}
	c := conv(t,
		userRow("u1", 1, "hi"),
		assistantRow("a1", 2, "claude-opus-5-5", textBlock("x"), with("perTurnEffort", "high"), with("effort", "low")),
		// ignored: a synthetic row and a sidechain row
		apiErrorRow("e1", 3, "rate_limit", "limit"),
		assistantRow("s1", 3.5, "claude-haiku-5-5", textBlock("sub"), sidechain()),
	)
	if u := c.Usage; u == nil || u.Model != "claude-opus-5-5" || u.Effort != "high" {
		t.Errorf("usage = %+v, want opus / high (perTurnEffort over effort)", u)
	}
	// effort falls back to the row's effort, and is taken from the LAST row
	c = conv(t,
		userRow("u1", 1, "hi"),
		assistantRow("a1", 2, "claude-opus-5-5", textBlock("x"), with("perTurnEffort", "high")),
		assistantRow("a2", 3, "claude-sonnet-5-5", textBlock("y"), without("perTurnEffort"), with("effort", "low")),
	)
	if u := c.Usage; u == nil || u.Model != "claude-sonnet-5-5" || u.Effort != "low" {
		t.Errorf("usage = %+v, want sonnet / low", u)
	}
	// a tool_use row counts too (it is a main-thread assistant row)
	c = conv(t, userRow("u1", 1, "hi"), assistantRow("a1", 2, "claude-opus-5-5", toolUseBlock("toolu_1", "Bash", obj{"command": "ls"})))
	if c.Usage == nil || c.Usage.Model != "claude-opus-5-5" {
		t.Errorf("usage from a tool_use row = %+v", c.Usage)
	}
}

// ---- ids ------------------------------------------------------------------

func TestIDs_RowUUIDs(t *testing.T) {
	c := conv(t, userRow("U-1", 1, "hi"), assistantThinking("T-1", 2, "", 5), assistantText("A-1", 3, "yo"), interruptRow("I-1", 4, false))
	if c.Turns[0].ID != "U-1" {
		t.Errorf("turn id = %q", c.Turns[0].ID)
	}
	var ids []string
	for _, it := range c.Turns[0].Items {
		ids = append(ids, itemID(it))
	}
	if !equalStrings(ids, []string{"U-1", "T-1", "A-1", "I-1"}) {
		t.Errorf("item ids = %v", ids)
	}
}

func TestIDs_MultiBlockRowSuffix(t *testing.T) {
	// older versions: several blocks in one row. The first item keeps the
	// row's uuid, the n-th further one is <uuid>#<n>; dropped blocks do not
	// take a number.
	c := conv(t, userRow("u1", 1, "hi"),
		multiBlockAssistant("m1", 2, thinkingBlock("hmm"), thinkingBlock(""), textBlock("one"), textBlock("two")))
	var ids []string
	for _, it := range c.Turns[0].Items[1:] {
		ids = append(ids, itemID(it))
	}
	if !equalStrings(ids, []string{"m1", "m1#1", "m1#2"}) {
		t.Errorf("ids = %v, want [m1 m1#1 m1#2]", ids)
	}
}

func TestIDs_DerivedSystemIDs(t *testing.T) {
	c := conv(t,
		userRow("u1", 1, "a"), assistantRow("a1", 2, "claude-opus-5-5", textBlock("x")), turnDuration("d1", 3, 1),
		userRow("u2", 4, "b", entrypoint("sdk-cli")), assistantRow("a2", 5, "claude-sonnet-5-5", textBlock("y"), entrypoint("sdk-cli")),
	)
	var ids []string
	for _, it := range c.Turns[1].Items {
		ids = append(ids, itemID(it))
	}
	if !equalStrings(ids, []string{"u2", "u2#handoff", "u2#model", "a2"}) {
		t.Errorf("ids = %v", ids)
	}
}

func TestIDs_StableAcrossRenormalize(t *testing.T) {
	lines := [][]byte{
		userRow("u1", 1, "go"), assistantThinking("t1", 2, "", 9), assistantText("a1", 3, "x"), turnDuration("d1", 4, 1),
		userRow("u2", 5, "again", promptSource("queued")), interruptRow("i1", 6, false),
		localCommandRow("lc1", 7, "<command-name>/usage</command-name>"),
	}
	a := jsonOf(t, conv(t, lines...))
	b := jsonOf(t, conv(t, lines...))
	if a != b {
		t.Errorf("two normalizations differ:\n%s\n%s", a, b)
	}
}
