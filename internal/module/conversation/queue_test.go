package conversation

import (
	"encoding/json"
	"testing"

	"github.com/wake/purdex/internal/convfeed"
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
	e.owners.own = []convfeed.Owner{{TranscriptPath: p, Status: "running", SeenAt: 1}}
	s := decode(t, e.get("/api/conversations/claude/"+sid))
	if s.Header.Queued != nil {
		t.Fatalf("an empty queue must be omitted, got %+v", s.Header.Queued)
	}
	appendRows(t, p, queueRow("enqueue", "later please"), queueRow("enqueue", "<task-notification>x</task-notification>"))
	s = decode(t, e.get("/api/conversations/claude/"+sid))
	if len(s.Header.Queued) != 1 || s.Header.Queued[0].Text != "later please" || s.Header.Queued[0].Source != "user" {
		t.Fatalf("queued = %+v", s.Header.Queued)
	}
	// a queue that outlives its session is a leftover: omitted when the pane is gone or the session ended
	e.owners.own = nil
	s = decode(t, e.get("/api/conversations/claude/"+sid))
	if s.Header.Queued != nil {
		t.Fatalf("an ended session must not carry a queue, got %+v", s.Header.Queued)
	}
}

func TestSnapshot_PeerQueuedEntryCarriesFrom(t *testing.T) {
	e := newEnv(t)
	p := e.transcript(idleTurns(1))
	e.owners.own = []convfeed.Owner{{TranscriptPath: p, Status: "running", SeenAt: 1}}
	appendRows(t, p, queueRow("enqueue", `<cross-session-message from-name="mlab/x-1">hi there</cross-session-message>`))
	var s struct {
		Header struct {
			Queued []struct{ Text, Source, From string } `json:"queued"`
		} `json:"header"`
	}
	if err := json.Unmarshal(e.get("/api/conversations/claude/"+sid).Body.Bytes(), &s); err != nil || len(s.Header.Queued) != 1 ||
		s.Header.Queued[0].Text != "hi there" || s.Header.Queued[0].Source != "peer" || s.Header.Queued[0].From != "mlab/x-1" {
		t.Fatalf("%v %+v", err, s.Header.Queued)
	}
}

// A queue-only change is a conversation.header frame, like a title change.
func TestWS_QueueOnlyChangeIsAHeaderFrame(t *testing.T) {
	e, _ := wsEnv(t)
	p := e.transcript(idleTurns(1))
	e.owners.own = []convfeed.Owner{{TranscriptPath: p, Status: "running", SeenAt: 1}}
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
