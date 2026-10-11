package conversation

import (
	"encoding/json"
	"testing"
)

func queueRow(op, content string) string {
	o := obj{"type": "queue-operation", "operation": op, "sessionId": sid}
	if content != "" {
		o["content"] = content
	}
	return row(o)
}

func TestSnapshot_HeaderCarriesQueued(t *testing.T) {
	e := newEnv(t)
	p := e.transcript(idleTurns(1))
	s := decode(t, e.get("/api/conversations/claude/"+sid))
	if s.Header.Queued != nil {
		t.Fatalf("an empty queue must be omitted, got %+v", s.Header.Queued)
	}
	appendRows(t, p, queueRow("enqueue", "later please"), queueRow("enqueue", "<task-notification>x</task-notification>"))
	s = decode(t, e.get("/api/conversations/claude/"+sid))
	if len(s.Header.Queued) != 1 || s.Header.Queued[0].Text != "later please" || s.Header.Queued[0].Source != "user" {
		t.Fatalf("queued = %+v", s.Header.Queued)
	}
}

// A queue-only change is a conversation.header frame, like a title change.
func TestWS_QueueOnlyChangeIsAHeaderFrame(t *testing.T) {
	e, _ := wsEnv(t)
	p := e.transcript(idleTurns(1))
	srv := e.server()
	c := e.connect(srv, "")
	c.expect("conversation.snapshot")
	c.expect("approvals.snapshot")
	appendRows(t, p, queueRow("enqueue", "wait for it"))
	h := c.expect("conversation.header")
	var hv struct {
		Header struct {
			Queued []struct {
				Text   string `json:"text"`
				Source string `json:"source"`
			} `json:"queued"`
		} `json:"header"`
	}
	if err := json.Unmarshal(h.Value, &hv); err != nil || len(hv.Header.Queued) != 1 || hv.Header.Queued[0].Text != "wait for it" || hv.Header.Queued[0].Source != "user" {
		t.Fatalf("header frame %v: %s", err, h.Value)
	}
	appendRows(t, p, queueRow("dequeue", ""))
	h = c.expect("conversation.header")
	hv.Header.Queued = nil // an empty queue is omitted from the frame
	if err := json.Unmarshal(h.Value, &hv); err != nil || len(hv.Header.Queued) != 0 {
		t.Fatalf("after dequeue %v: %s", err, h.Value)
	}
}
