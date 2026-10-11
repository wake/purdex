package ccnorm

import (
	"encoding/json"
	"testing"
)

// c9d9c7-67 (#2592): a turn's output tokens are the sum, over its assistant messages, of message.usage.output_tokens. Claude Code writes one
// content block per row, and the rows of one message.id all carry the message's final count (measured on real transcripts: 0 of 936 ids with
// differing rows), so the count is kept per id and never summed over rows.

// usageOf sets the row's message id and output_tokens (message.usage).
func usageOf(id string, out any) opt {
	return func(o obj) {
		m := o["message"].(obj)
		m["id"] = id
		m["usage"] = obj{"input_tokens": 3, "output_tokens": out, "cache_read_input_tokens": 100}
	}
}

func tokensOf(t testing.TB, rows ...[]byte) []int64 {
	t.Helper()
	c := conv(t, rows...)
	out := make([]int64, len(c.Turns))
	for i, tr := range c.Turns {
		out[i] = tr.OutputTokens
	}
	return out
}

func TestTokens_OneMessageManyRowsCountsOnce(t *testing.T) {
	// thinking, text and a tool_use of ONE message: three rows, the same 240
	got := tokensOf(t,
		userRow("u1", 1, "go"),
		assistantThinking("a1", 2, "hmm", 800, usageOf("msg_A", 240)),
		assistantText("a2", 2.1, "doing it", usageOf("msg_A", 240)),
		assistantRow("a3", 2.2, "claude-opus-5-5", toolUseBlock("tu1", "Bash", obj{"command": "ls"}), usageOf("msg_A", 240)),
	)
	if got[0] != 240 {
		t.Errorf("output_tokens = %d, want 240 (one message, three rows)", got[0])
	}
}

func TestTokens_MessagesAddUp(t *testing.T) {
	got := tokensOf(t,
		userRow("u1", 1, "go"),
		assistantText("a1", 2, "one", usageOf("msg_A", 462)),
		assistantText("a2", 3, "two", usageOf("msg_B", 73)),
		assistantText("a3", 4, "three", usageOf("msg_C", 481)),
	)
	if got[0] != 462+73+481 {
		t.Errorf("output_tokens = %d, want %d", got[0], 462+73+481)
	}
}

func TestTokens_EachTurnHasItsOwn(t *testing.T) {
	got := tokensOf(t,
		userRow("u1", 1, "first"), assistantText("a1", 2, "x", usageOf("msg_A", 100)), turnDuration("d1", 3, 2000),
		userRow("u2", 4, "second"), assistantText("a2", 5, "y", usageOf("msg_B", 40)), turnDuration("d2", 6, 2000),
	)
	if len(got) != 2 || got[0] != 100 || got[1] != 40 {
		t.Errorf("per-turn output_tokens = %v, want [100 40]", got)
	}
}

func TestTokens_AbsentWithoutUsage(t *testing.T) {
	got := tokensOf(t, userRow("u1", 1, "go"), assistantText("a1", 2, "no usage here"))
	if got[0] != 0 {
		t.Errorf("output_tokens = %d, want 0 (no usage on the row)", got[0])
	}
	b, _ := json.Marshal(conv(t, userRow("u1", 1, "go"), assistantText("a1", 2, "x")).Turns[0])
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	if _, has := m["output_tokens"]; has {
		t.Errorf("output_tokens is on the wire for a turn with none: %s", b)
	}
}

func TestTokens_BadValuesAreIgnored(t *testing.T) {
	for name, v := range map[string]any{"string": "12", "negative": -5, "null": nil, "object": obj{"a": 1}} {
		got := tokensOf(t, userRow("u1", 1, "go"), assistantText("a1", 2, "x", usageOf("msg_A", v)))
		if got[0] != 0 {
			t.Errorf("%s: output_tokens = %d, want 0", name, got[0])
		}
	}
}

func TestTokens_RowsWithoutAnIdCountUnderTheirOwn(t *testing.T) {
	noID := func(o obj) { delete(o["message"].(obj), "id") }
	// two rows with no message id and different uuids are two messages
	n := norm(t, userRow("u1", 1, "go"))
	feed(t, n, assistantText("a1", 2, "one", usageOf("", 10), noID), assistantText("a2", 3, "two", usageOf("", 20), noID))
	if tok := n.Conversation().Turns[0].OutputTokens; tok != 30 {
		t.Errorf("output_tokens = %d, want 30 (two id-less rows are two messages)", tok)
	}
}

func TestTokens_ChangeReportedOnlyWhenTheTotalMoves(t *testing.T) {
	n := norm(t, userRow("u1", 1, "go"))
	first := feed(t, n, assistantText("a1", 2, "one", usageOf("msg_A", 50)))
	if !hasTurnChange(first, "u1") {
		t.Errorf("the turn header is not reported when the total went from 0 to 50: %+v", first)
	}
	// the next row of the same message repeats 50: no new change for the header (the item itself is reported)
	second := feed(t, n, assistantRow("a2", 2.1, "claude-opus-5-5", toolUseBlock("tu1", "Bash", obj{"command": "ls"}), usageOf("msg_A", 50)))
	if hasTurnChange(second, "u1") {
		t.Errorf("the turn header is reported again though the total stayed 50: %+v", second)
	}
	third := feed(t, n, assistantText("a3", 3, "two", usageOf("msg_B", 25)))
	if !hasTurnChange(third, "u1") {
		t.Errorf("the turn header is not reported when the total went to 75: %+v", third)
	}
	if got := n.Conversation().Turns[0].OutputTokens; got != 75 {
		t.Errorf("output_tokens = %d, want 75", got)
	}
}

func TestTokens_AMessageNeverLowersItsCount(t *testing.T) {
	// a later row of the same message with a smaller number (never seen on real files) must not lower the total
	got := tokensOf(t,
		userRow("u1", 1, "go"),
		assistantText("a1", 2, "x", usageOf("msg_A", 200)),
		assistantText("a2", 2.1, "y", usageOf("msg_A", 150)),
	)
	if got[0] != 200 {
		t.Errorf("output_tokens = %d, want 200", got[0])
	}
}

func TestTokens_SidechainRowsAreNotTheMainThreads(t *testing.T) {
	got := tokensOf(t,
		userRow("u1", 1, "go"),
		assistantText("a1", 2, "main", usageOf("msg_A", 100)),
		assistantText("s1", 2.5, "sub", usageOf("msg_S", 9999), with("isSidechain", true)),
	)
	if got[0] != 100 {
		t.Errorf("output_tokens = %d, want 100 (the subagent's rows are not the main thread's)", got[0])
	}
}

// hasTurnChange reports whether changes carry a header-only change (empty item id) for the turn.
func hasTurnChange(changes []Change, turnID string) bool {
	for _, c := range changes {
		if c.TurnID == turnID && c.ItemID == "" {
			return true
		}
	}
	return false
}
