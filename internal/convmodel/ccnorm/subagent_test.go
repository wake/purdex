package ccnorm

import (
	"bytes"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/convmodel"
)

const agentX = "a2e79003c6fe3e9fb"

// joinLines is a transcript file: the lines, each ended by "\n".
func joinLines(lines ...[]byte) []byte {
	return append(bytes.Join(lines, []byte("\n")), '\n')
}

// asConversation wraps items in one turn so Validate can check them.
func asConversation(items []convmodel.Item) convmodel.Conversation {
	return convmodel.Conversation{Provider: "claude", Turns: []convmodel.Turn{
		{ID: "t", Index: 0, Outcome: convmodel.OutcomeDone, Items: items},
	}}
}

// subagentFile is a subagent's transcript as Claude Code writes it: every
// row sidechain, carrying the agent id, the first row the brief.
func subagentFile() []byte {
	a := func(o ...opt) []opt { return append([]opt{sidechain(), with("agentId", agentX)}, o...) }
	brief := userRow("s0", 1, "Find the TODOs in the repo.", append(a(), without("origin"), without("promptSource"), without("turnOrigin"), without("turnPosition"))...)
	return joinLines(
		brief,
		assistantThinking("s1", 2, "", 900, a()...),
		assistantRow("s2", 3, "claude-haiku-5-5", toolUseBlock("toolu_s1", "Grep", obj{"pattern": "TODO"}), a()...),
		resultRow("s3", 4, "toolu_s1", "a.go:1: TODO\nb.go:9: TODO", false, a()...),
		assistantRow("s4", 5, "claude-haiku-5-5", textBlock("Two TODOs."), a()...),
	)
}

func TestNormalizeSubagent_ItemsFromSidechainFile(t *testing.T) {
	items, st := NormalizeSubagent(bytes.NewReader(subagentFile()), agentX)
	c := asConversation(items)
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	var got []string
	for _, it := range items {
		got = append(got, sig(it)+"|"+itemID(it))
	}
	want := []string{
		"user:task:Find the TODOs in the repo.|s0",
		`thinking:"":900|s1`,
		"step|toolu_s1",
		"agent_text:Two TODOs.|s4",
	}
	if !equalStrings(got, want) {
		t.Fatalf("items:\n got %q\nwant %q", got, want)
	}
	step := items[2].Step
	if step.Status != convmodel.StepDone || step.Kind != convmodel.StepSearch || step.Output == nil || step.Output.TotalLines != 2 {
		t.Errorf("step = %+v", step)
	}
	if st.Skipped["sidechain"] != 0 || st.Lines != 5 {
		t.Errorf("stats = %+v", st)
	}
}

func TestNormalizeSubagent_BriefIsTheFirstPromptOnly(t *testing.T) {
	// a later prompt row (the agent was messaged again) is an ordinary user
	// item; a row of another agent is left out and counted
	a := func(o ...opt) []opt { return append([]opt{sidechain(), with("agentId", agentX)}, o...) }
	file := joinLines(
		userRow("s0", 1, "first brief", a()...),
		assistantText("s1", 2, "ok", a()...),
		userRow("s2", 3, "follow-up", a()...),
		userRow("o1", 4, "someone else", sidechain(), with("agentId", "zzzz")),
	)
	items, st := NormalizeSubagent(bytes.NewReader(file), agentX)
	var got []string
	for _, it := range items {
		got = append(got, sig(it))
	}
	if want := []string{"user:task:first brief", "agent_text:ok", "user:user:follow-up"}; !equalStrings(got, want) {
		t.Errorf("items = %q", got)
	}
	if st.Skipped["agent:other"] != 1 {
		t.Errorf("Skipped = %v", st.Skipped)
	}
	// without an agent id nothing is filtered
	items, _ = NormalizeSubagent(bytes.NewReader(file), "")
	if len(items) != 4 {
		t.Errorf("no filter: %d items", len(items))
	}
}

func TestNormalizeSubagent_StepsOpenWhileTheFileEndsMidTool(t *testing.T) {
	// the file is read as live: a tool still running has no result yet
	a := func(o ...opt) []opt { return append([]opt{sidechain(), with("agentId", agentX)}, o...) }
	items, _ := NormalizeSubagent(bytes.NewReader(joinLines(
		userRow("s0", 1, "brief", a()...),
		assistantRow("s2", 3, "claude-haiku-5-5", toolUseBlock("toolu_s1", "Bash", obj{"command": "make"}), a()...),
	)), agentX)
	if len(items) != 2 || items[1].Step == nil || items[1].Step.Status != convmodel.StepRunning {
		t.Errorf("items = %+v", items)
	}
}

func TestNormalizeSubagent_EmptyAndMalformedInput(t *testing.T) {
	items, st := NormalizeSubagent(strings.NewReader(""), agentX)
	if items == nil || len(items) != 0 || st.Lines != 0 {
		t.Errorf("empty: %v %+v", items, st)
	}
	// no final newline, a bad line, a blank line: all counted, none fatal
	file := append(joinLines(subagentLine("{not json"), subagentLine("")), subagentFile()[:len(subagentFile())-1]...)
	items, st = NormalizeSubagent(bytes.NewReader(file), agentX)
	if len(items) != 4 || st.BadJSON != 2 {
		t.Errorf("items %d badjson %d", len(items), st.BadJSON)
	}
}

func subagentLine(s string) []byte { return []byte(s) }

func TestNormalizeSubagent_LineFillingTheReadBufferExactly(t *testing.T) {
	// 64 KiB is the reader's buffer: a last line of exactly that size with no
	// newline ends in an empty chunk at EOF
	pad := 64<<10 - len(userRow("s0", 1, "", sidechain()))
	row := userRow("s0", 1, strings.Repeat("a", pad), sidechain())
	if len(row) != 64<<10 {
		t.Fatalf("row is %d bytes", len(row))
	}
	items, st := NormalizeSubagent(bytes.NewReader(row), "")
	if len(items) != 1 || st.Lines != 1 || st.BadJSON != 0 {
		t.Errorf("items %d stats %+v", len(items), st)
	}
}

func TestNormalizeSubagent_OversizeLineSkippedAndReadingGoesOn(t *testing.T) {
	big := userRow("big", 2, strings.Repeat("a", 9<<20), sidechain(), with("agentId", agentX))
	file := joinLines(
		userRow("s0", 1, "brief", sidechain(), with("agentId", agentX)),
		big,
		assistantText("s1", 3, "after", sidechain(), with("agentId", agentX)),
	)
	items, st := NormalizeSubagent(bytes.NewReader(file), agentX)
	if len(items) != 2 || st.Skipped["line:oversize"] != 1 || st.Lines != 3 {
		t.Errorf("items %d stats %+v", len(items), st)
	}
}

func TestRows_SidechainStillSkippedInAMainFile(t *testing.T) {
	// only NormalizeSubagent accepts sidechain rows
	n := norm(t, userRow("u1", 1, "hi"), assistantText("a1", 2, "sub", sidechain()))
	if len(n.Conversation().Turns[0].Items) != 1 || n.Stats().Skipped["sidechain"] != 1 {
		t.Errorf("a sidechain row entered a main file: %s", dump(n.Conversation()))
	}
}
