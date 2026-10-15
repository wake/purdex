package conversation

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
)

// fakePred is a PredecessorReader (both the team's and the agent's have this one method) over a mutable map.
type fakePred struct {
	mu   sync.Mutex
	rows map[string]struct {
		prev string
		at   int64
	}
	err error
}

func (f *fakePred) set(sid, prev string, at int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.rows == nil {
		f.rows = map[string]struct {
			prev string
			at   int64
		}{}
	}
	f.rows[sid] = struct {
		prev string
		at   int64
	}{prev, at}
}

func (f *fakePred) PredecessorOf(sid string) (string, int64, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return "", 0, false, f.err
	}
	r, ok := f.rows[sid]
	return r.prev, r.at, ok, nil
}

// windowOf decodes only the window object of a snapshot body, as a raw map (so an absent key is visible).
func windowOf(t *testing.T, body []byte) map[string]json.RawMessage {
	t.Helper()
	var s struct {
		Window map[string]json.RawMessage `json:"window"`
	}
	if err := json.Unmarshal(body, &s); err != nil {
		t.Fatalf("body: %v: %.200s", err, body)
	}
	return s.Window
}

func wantPredecessor(t *testing.T, body []byte, wantSID, wantKind string, wantAt int64) {
	t.Helper()
	raw, ok := windowOf(t, body)["predecessor"]
	if !ok {
		t.Fatalf("window.predecessor missing: %.300s", body)
	}
	var p struct {
		SessionID string `json:"session_id"`
		Kind      string `json:"kind"`
		At        int64  `json:"at"`
	}
	if err := json.Unmarshal(raw, &p); err != nil || p.SessionID != wantSID || p.Kind != wantKind || p.At != wantAt {
		t.Fatalf("predecessor = %s (%v), want %s/%s/%d", raw, err, wantSID, wantKind, wantAt)
	}
}

func noPredecessor(t *testing.T, body []byte) {
	t.Helper()
	if raw, ok := windowOf(t, body)["predecessor"]; ok {
		t.Fatalf("window.predecessor present: %s", raw)
	}
}

const prevSID = "11111111-2222-3333-4444-555555555555"

func TestPredecessor_RelayWinsOverClear(t *testing.T) {
	e := newEnv(t)
	e.transcript(idleTurns(3))
	relay, clr := &fakePred{}, &fakePred{}
	relay.set(sid, prevSID, 2000)
	clr.set(sid, "ffffffff-0000-0000-0000-000000000000", 1000)
	e.mod.relayPred, e.mod.clearPred = relay, clr
	wantPredecessor(t, e.get("/api/conversations/claude/"+sid).Body.Bytes(), prevSID, "relay", 2000)
}

// A relay's /clear is recorded as a plain clear first; the lineage row lands a moment later. The same session answers
// clear, then relay.
func TestPredecessor_RelayGapAnswersClearThenRelay(t *testing.T) {
	e := newEnv(t)
	e.transcript(idleTurns(3))
	relay, clr := &fakePred{}, &fakePred{}
	clr.set(sid, prevSID, 1000)
	e.mod.relayPred, e.mod.clearPred = relay, clr
	wantPredecessor(t, e.get("/api/conversations/claude/"+sid).Body.Bytes(), prevSID, "clear", 1000)
	relay.set(sid, prevSID, 1500)
	wantPredecessor(t, e.get("/api/conversations/claude/"+sid).Body.Bytes(), prevSID, "relay", 1500)
}

func TestPredecessor_NoneOrMissingReadersOrErrorsOmitTheField(t *testing.T) {
	e := newEnv(t)
	e.transcript(idleTurns(3))
	noPredecessor(t, e.get("/api/conversations/claude/"+sid).Body.Bytes()) // no readers at all
	e.mod.relayPred, e.mod.clearPred = &fakePred{}, &fakePred{}
	noPredecessor(t, e.get("/api/conversations/claude/"+sid).Body.Bytes()) // readers, no row
	e.mod.relayPred = &fakePred{err: errors.New("db down")}
	w := e.get("/api/conversations/claude/" + sid)
	if w.Code != 200 {
		t.Fatalf("a reader error must not fail the snapshot: %d", w.Code)
	}
	noPredecessor(t, w.Body.Bytes())
}

func TestPredecessor_EveryHTTPSnapshotCarriesIt(t *testing.T) {
	e := newEnv(t)
	e.transcript(idleTurns(20))
	clr := &fakePred{}
	clr.set(sid, prevSID, 1000)
	e.mod.clearPred = clr
	base := "/api/conversations/claude/" + sid
	wantPredecessor(t, e.get(base+"?turns=3&before=10").Body.Bytes(), prevSID, "clear", 1000)
	_, mid := itemIDs(t, e.get(base+"?turns=1&before=11"))
	var one struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(mid.Conversation.Turns[0].Items[0], &one)
	wantPredecessor(t, e.get(base+"?turns=5&around="+one.ID).Body.Bytes(), prevSID, "clear", 1000)
}

func TestPredecessor_WebSocketSnapshotCarriesItAndChangesDoNot(t *testing.T) {
	e, _ := wsEnv(t)
	p := e.transcript(idleTurns(2))
	clr := &fakePred{}
	clr.set(sid, prevSID, 1000)
	e.mod.clearPred = clr
	c := e.connect(e.server(), "")
	snap := c.expect("conversation.snapshot")
	wantPredecessor(t, snap.Value, prevSID, "clear", 1000)
	c.expect("approvals.snapshot")
	appendRows(t, p, userRow("u9", 60, "live row"))
	ch := c.expect("conversation.changes")
	if strings.Contains(string(ch.Value), "predecessor") {
		t.Fatalf("an increment carries the predecessor: %.300s", ch.Value)
	}
}

// The predecessor is part of the envelope that is sized before the trim: at every cap the body stays under it, the
// oldest turns go first, and the field survives.
func TestPredecessor_SurvivesTheBodyCapTrim(t *testing.T) {
	e := newEnv(t)
	e.transcript(idleTurns(40))
	clr := &fakePred{}
	clr.set(sid, prevSID, 1000)
	e.mod.clearPred = clr
	checked := 0
	for cap := 3000; cap <= 6000; cap += 53 {
		e.mod.maxBody = cap
		w := e.get("/api/conversations/claude/" + sid + "?turns=200")
		if w.Code != 200 {
			continue // too small for the envelope: an error, not a body
		}
		checked++
		if w.Body.Len() > cap {
			t.Fatalf("cap %d: body %d bytes over the cap", cap, w.Body.Len())
		}
		wantPredecessor(t, w.Body.Bytes(), prevSID, "clear", 1000)
		s := decode(t, w)
		if len(s.Conversation.Turns) == 0 || len(s.Conversation.Turns) >= 40 || s.Window.LastIndex != 39 || !s.Window.HasMoreBefore {
			t.Fatalf("cap %d: not the newest turns only: %d turns %+v", cap, len(s.Conversation.Turns), s.Window)
		}
	}
	if checked < 10 {
		t.Fatalf("only %d caps produced a body", checked)
	}
}
