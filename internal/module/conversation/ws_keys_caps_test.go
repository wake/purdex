package conversation

import (
	"encoding/json"
	"testing"

	"github.com/wake/purdex/internal/convfeed"
)

func capsSend(t *testing.T, raw json.RawMessage) (send, reason string) {
	t.Helper()
	var v struct {
		Capabilities struct {
			Send    string            `json:"send"`
			Reasons map[string]string `json:"reasons"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("capabilities frame: %v: %s", err, raw)
	}
	return v.Capabilities.Send, v.Capabilities.Reasons["send"]
}

func (e *env) setOwners(o []convfeed.Owner) {
	e.owners.mu.Lock()
	e.owners.own = o
	e.owners.mu.Unlock()
}

// The header and the capabilities it implies leave in one order: when the resolver reports the session gone, the
// capabilities frame (send not_wired) reaches the client before the header frame that says ended, in the same tick.
// Mutation gate: pushing capabilities only before the refresh -> red.
func TestWS_CapabilitiesLeaveAheadOfTheHeaderThatImpliesThem(t *testing.T) {
	e, _ := wsEnv(t)
	p := e.transcript(idleTurns(1))
	e.setOwners([]convfeed.Owner{{TranscriptPath: p, Status: "idle", SeenAt: 1, FrameID: "f1"}})
	e.sender(&fakeSender{})
	srv := e.server()
	c := e.connect(srv, "")
	snap := c.expect("conversation.snapshot")
	var s struct {
		Conversation json.RawMessage `json:"conversation"`
	}
	_ = json.Unmarshal(snap.Value, &s)
	if send, _ := capsSend(t, s.Conversation); send != "keys" {
		t.Fatalf("live snapshot send = %q: %s", send, snap.Value)
	}
	c.expect("approvals.snapshot")
	e.setOwners(nil) // the pane is gone: the next lookup says ended
	f := c.read()
	if f.Type != "conversation.capabilities" {
		t.Fatalf("first frame after the session ended is %q, want conversation.capabilities ahead of the header: %.200s", f.Type, f.Value)
	}
	if send, reason := capsSend(t, f.Value); send != "" || reason != "not_wired" {
		t.Fatalf("capabilities after ended: send=%q reason=%q", send, reason)
	}
	h := c.read()
	if h.Type != "conversation.header" && h.Type != "conversation.changes" {
		t.Fatalf("after the capabilities came %q, want the header", h.Type)
	}
}

// On a live source the owner appearing or going (no header change) still moves send keys <-> prompt on that tick.
func TestWS_OwnerComesAndGoesOnALiveSource(t *testing.T) {
	e, _ := wsEnv(t)
	p := e.transcript(idleTurns(1))
	e.setOwners([]convfeed.Owner{{TranscriptPath: p, Status: "idle", SeenAt: 1, FrameID: "f1"}})
	f := &fakeSender{}
	e.sender(f)
	srv := e.server()
	c := e.connect(srv, "")
	c.expect("conversation.snapshot")
	c.expect("approvals.snapshot")
	f.liveOwner.Store(true)
	if send, _ := capsSend(t, c.expect("conversation.capabilities").Value); send != "prompt" {
		t.Fatalf("owner came: send = %q", send)
	}
	f.liveOwner.Store(false)
	if send, _ := capsSend(t, c.expect("conversation.capabilities").Value); send != "keys" {
		t.Fatalf("owner went: send = %q", send)
	}
}
