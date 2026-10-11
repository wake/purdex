package conversation

import (
	"encoding/json"
	"testing"

	"github.com/wake/purdex/internal/convfeed"
)

// c9d9c7-67 (#2592): a turn carries output_tokens — the sum over its assistant messages of message.usage.output_tokens — in the snapshot and in
// the turn header of a changes frame; absent while no row reported any.

func usageRow(uuid string, sec float64, msgID string, out int) string {
	o := common("assistant", uuid, sec)
	o["message"] = obj{"id": msgID, "model": "claude-opus-5-5", "role": "assistant", "content": []obj{{"type": "text", "text": "words"}},
		"stop_reason": "end_turn", "usage": obj{"input_tokens": 3, "output_tokens": out}}
	return row(o)
}

type tokenTurns struct {
	Conversation struct {
		Turns []struct {
			ID           string `json:"id"`
			OutputTokens int64  `json:"output_tokens"`
		} `json:"turns"`
	} `json:"conversation"`
}

func TestSnapshot_TurnCarriesOutputTokens(t *testing.T) {
	e := newEnv(t)
	p := e.transcript(userRow("u0", 0, "go") + "\n" + usageRow("a0", 1, "msg_A", 240) + "\n" + usageRow("a1", 2, "msg_A", 240) + "\n" + usageRow("a2", 3, "msg_B", 60) + "\n")
	e.owners.own = []convfeed.Owner{{TranscriptPath: p, Status: "idle", SeenAt: 1}}
	var s tokenTurns
	if err := json.Unmarshal(e.get("/api/conversations/claude/"+sid).Body.Bytes(), &s); err != nil || len(s.Conversation.Turns) != 1 {
		t.Fatalf("snapshot %v: %+v", err, s)
	}
	if s.Conversation.Turns[0].OutputTokens != 300 {
		t.Errorf("output_tokens = %d, want 300 (msg_A once for its two rows, plus msg_B)", s.Conversation.Turns[0].OutputTokens)
	}
}

func TestSnapshot_NoUsageNoField(t *testing.T) {
	e := newEnv(t)
	p := e.transcript(idleTurns(1))
	e.owners.own = []convfeed.Owner{{TranscriptPath: p, Status: "idle", SeenAt: 1}}
	body := e.get("/api/conversations/claude/" + sid).Body.String()
	var raw struct {
		Conversation struct {
			Turns []map[string]json.RawMessage `json:"turns"`
		} `json:"conversation"`
	}
	if err := json.Unmarshal([]byte(body), &raw); err != nil || len(raw.Conversation.Turns) != 1 {
		t.Fatalf("%v: %s", err, body)
	}
	if _, has := raw.Conversation.Turns[0]["output_tokens"]; has {
		t.Errorf("a turn with no usage carries output_tokens: %s", body)
	}
}

// A message landing in a running turn moves the turn's header: the frame says so with the new total.
func TestWS_TokensAreInTheTurnHeaderOfAChangesFrame(t *testing.T) {
	e, _ := wsEnv(t)
	p := e.transcript(userRow("u0", 0, "go") + "\n")
	e.owners.own = []convfeed.Owner{{TranscriptPath: p, Status: "running", SeenAt: 1}}
	srv := e.server()
	c := e.connect(srv, "")
	c.expect("conversation.snapshot")
	c.expect("approvals.snapshot")
	appendRows(t, p, usageRow("a0", 1, "msg_A", 120))
	f := c.expect("conversation.changes")
	var v struct {
		Changes []struct {
			Turn struct {
				OutputTokens int64 `json:"output_tokens"`
			} `json:"turn"`
		} `json:"changes"`
	}
	if err := json.Unmarshal(f.Value, &v); err != nil || len(v.Changes) == 0 || v.Changes[0].Turn.OutputTokens != 120 {
		t.Fatalf("changes frame %v: %s", err, f.Value)
	}
	appendRows(t, p, usageRow("a1", 2, "msg_B", 30))
	f = c.expect("conversation.changes")
	if err := json.Unmarshal(f.Value, &v); err != nil || len(v.Changes) == 0 || v.Changes[len(v.Changes)-1].Turn.OutputTokens != 150 {
		t.Fatalf("second changes frame %v: %s", err, f.Value)
	}
}
