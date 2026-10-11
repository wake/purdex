package ccnorm

import (
	"strings"
	"testing"

	"github.com/wake/purdex/internal/convmodel"
)

// U4-1a: a worker is `claude -p` driven by Nexen, so its transcript has SDK-entrypoint user rows (no origin, promptSource
// and turnOrigin "sdk"), may carry a peer-mailbox turn (three text blocks, no origin, no isMeta: the shape in
// spa/src/lib/nex/__fixtures__/peer-mailbox/transcript-user-line.jsonl) and sidechain rows of a subagent, and ends without
// a turn_duration row while the worker lives. Synthetic content only.

func sdkUser(uuid string, sec float64, text string, extra ...opt) []byte {
	return userRow(uuid, sec, text, append([]opt{without("origin"), entrypoint("sdk-cli"), promptSource("sdk"), turnOrigin("sdk")}, extra...)...)
}

func peerMailboxRow(uuid string, sec float64) []byte {
	return userRow(uuid, sec, "", without("origin"), entrypoint("sdk-cli"), promptSource("sdk"), turnOrigin("sdk"),
		blocksOf(
			textBlock("Another Claude session sent a message (a peer message — not typed by your user):\n<cross-session-message from-name=\"host/peer-a\" from-mode=\"prompting\">\n"),
			textBlock("Please reply with the single word PONG."),
			textBlock("\n\nTo reply, run: pdx msg send host/_abc123 \"<your reply>\"\n</cross-session-message>"),
		))
}

func workerRows() [][]byte {
	return [][]byte{
		sdkUser("u1", 1, "list the todos", entrypoint("sdk-cli")),
		assistantRow("a1", 2, "claude-opus-5-5", toolUseBlock("toolu_t1", "Task", obj{"description": "scan", "prompt": "scan the repo"})),
		assistantText("s1", 3, "scanning", sidechain(), with("agentId", agentX)), // a subagent row inside the main file
		toolResultRow("r1", 4, "toolu_t1", "two todos"),
		assistantText("a2", 5, "Two TODOs found."),
		turnDuration("d1", 6, 4000),
		peerMailboxRow("u2", 7),
		assistantText("a3", 8, "PONG"),
	}
}

func TestWorkerTranscript_SDKRowsSidechainAndPeerMailbox(t *testing.T) {
	c := conv(t, workerRows()...)
	if len(c.Turns) != 2 {
		t.Fatalf("turns = %d, want 2\n%s", len(c.Turns), dump(c))
	}
	u1 := firstUser(t, convmodel.Conversation{Provider: c.Provider, Turns: c.Turns[:1]})
	if u1.Text != "list the todos" || u1.Source == convmodel.SourcePeer {
		t.Errorf("sdk prompt = %+v", u1)
	}
	u2 := firstUser(t, convmodel.Conversation{Provider: c.Provider, Turns: c.Turns[1:]})
	if u2.Source != convmodel.SourcePeer || u2.From == nil || u2.From.Name != "host/peer-a" || !strings.Contains(u2.Text, "Please reply with the single word PONG.") || strings.Contains(u2.Text, "cross-session") {
		t.Errorf("peer-mailbox turn = %+v from %+v", u2, u2.From)
	}
	if c.Turns[0].Outcome != convmodel.OutcomeDone {
		t.Errorf("first turn outcome = %q", c.Turns[0].Outcome)
	}
	for _, it := range c.Turns[0].Items { // the sidechain row never shows as the main agent's text
		if it.AgentText != nil && it.AgentText.Markdown == "scanning" {
			t.Errorf("a sidechain row leaked into the main turn\n%s", dump(c))
		}
	}
}

// A live execution-owned conversation whose turn already ended (turn_duration written): SetLive(true) leaves it done.
func TestWorkerTranscript_EndedTurnStaysDoneWhileLive(t *testing.T) {
	n := norm(t, workerRows()[:6]...)
	n.SetLive(true)
	if tr := validated(t, n).Turns[0]; tr.Outcome != convmodel.OutcomeDone {
		t.Errorf("outcome = %q, want done", tr.Outcome)
	}
}

// A live execution-owned conversation: the worker's last turn ends with no turn_duration row until the process ends, and
// SetLive(false) (the resolver's answer once the execution is not live) still closes it as done at the last row.
func TestWorkerTranscript_LastTurnClosesWhenTheExecutionEnds(t *testing.T) {
	n2 := norm(t, workerRows()...)
	if tr := validated(t, n2).Turns[1]; tr.Outcome != convmodel.OutcomeRunning {
		t.Fatalf("live last turn = %q, want running", tr.Outcome)
	}
	n2.SetLive(false)
	if tr := validated(t, n2).Turns[1]; tr.Outcome != convmodel.OutcomeDone || endedAt(t, tr) != ms(8) {
		t.Errorf("after SetLive(false): %q ended_at %v, want done at the last row", tr.Outcome, tr.EndedAt)
	}
}
