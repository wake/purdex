package dialogue

import (
	"os"
	"strings"
	"testing"
)

// The embedded prompt is the plan's appendix B-1 code block, word for word.
func TestTurnSummaryPrompt_MatchesPlanAppendix(t *testing.T) {
	raw, err := os.ReadFile("../../../docs/plans/2026-11-01-session-dialogue-log-plan.md")
	if err != nil {
		t.Fatal(err)
	}
	plan := string(raw)
	i := strings.Index(plan, "### B-1 ")
	if i < 0 {
		t.Fatal("appendix B-1 not found")
	}
	rest := plan[i:]
	open := strings.Index(rest, "```\n")
	if open < 0 {
		t.Fatal("B-1 code block not found")
	}
	rest = rest[open+4:]
	end := strings.Index(rest, "\n```")
	if end < 0 {
		t.Fatal("B-1 code block is not closed")
	}
	want := rest[:end]
	if strings.TrimRight(turnSummaryPrompt, "\n") != want {
		t.Fatalf("prompts/turn-summary-v1.txt differs from plan appendix B-1")
	}
	if !strings.Contains(want, `{"skip": false, "title": "…", "push": "…", "summary": "…"}`) {
		t.Fatal("extracted block is not the turn-summary prompt")
	}
}
