package watchmod

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/devices"
	"github.com/wake/purdex/internal/middleware"
	peersmod "github.com/wake/purdex/internal/module/peers"
	"github.com/wake/purdex/internal/watch"
)

// ackPair is two daemons, each with events of its own recorded on disk ("mlab-…" on a, "a26-…" on b) and its watch.events collected.
type ackPair struct {
	*pair
	aIDs, bIDs []string
	aEv, bEv   *events
}

func newAckPair(t *testing.T) *ackPair {
	t.Helper()
	p := newPair(t, bothOrder, oppositeOrder)
	ap := &ackPair{pair: p, aEv: &events{}, bEv: &events{}}
	p.a.m.emit, p.b.m.emit = ap.aEv.add, ap.bEv.add
	record := func(d *daemon, host string) []string {
		rc := watch.NewRecorder(d.m.dlv.logDir, host)
		var ids []string
		for i, k := range []watch.Kind{watch.KindUnmanagedHeavy, watch.KindPressureHigh} {
			rec, err := rc.RecordFull(watch.Event{Kind: k, At: d.getNow().Add(-time.Duration(2-i) * time.Minute), Label: watch.LabelGateGap, Argv: []string{"vitest"}, Pressure: 2, Detail: "d"})
			require.NoError(t, err)
			ids = append(ids, rec.ID)
		}
		return ids
	}
	ap.aIDs, ap.bIDs = record(p.a, "mlab"), record(p.b, "a26")
	return ap
}

func (ap *ackPair) ackOn(t *testing.T, d *daemon, body map[string]any) (int, ackResponse) {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/watch/ack", strings.NewReader(string(b))).WithContext(adminCtx)
	rr := httptest.NewRecorder()
	d.mux.ServeHTTP(rr, req)
	var resp ackResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	return rr.Code, resp
}

func (ap *ackPair) unhandledOn(t *testing.T, d *daemon) int {
	t.Helper()
	n, err := d.m.unhandledCount()
	require.NoError(t, err)
	return n
}

func TestAck_AnEventOfAnotherHostIsForwardedToItsHostWhichRecordsWhoAndFromWhere(t *testing.T) {
	ap := newAckPair(t)
	code, resp := ap.ackOn(t, ap.a, map[string]any{"ids": []string{ap.aIDs[0], ap.bIDs[0]}, "note": "已查"})
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, []watch.AckResult{{ID: ap.aIDs[0], Outcome: watch.AckHandled}, {ID: ap.bIDs[0], Outcome: watch.AckHandled}}, resp.Results)
	require.Equal(t, 2, resp.Handled)

	got, ok := ap.b.m.acks.Get(ap.bIDs[0])
	require.True(t, ok, "recorded on the host that owns the event")
	require.Equal(t, "已查", got.Note)
	require.Equal(t, "mlab", got.FromHost, "which host the order came from")
	_, onA := ap.a.m.acks.Get(ap.bIDs[0])
	require.False(t, onA, "and not on the host it was given on")

	require.Equal(t, 1, ap.bEv.n(), "watch.events goes out on the host of the event, once")
	require.Equal(t, EventsPayload{HostID: idB, Unhandled: 1, Cause: CauseAcked}, payloadOf(t, ap.bEv))
	require.Equal(t, 1, ap.aEv.n(), "a's own event: one push on a; none for b's event")
}

func TestAck_TheAnswerSaysWhatBecameOfEachIDIncludingTheOnesThatCouldNotBeReached(t *testing.T) {
	ap := newAckPair(t)
	ap.a.loop().setDown(idB, true)
	code, resp := ap.ackOn(t, ap.a, map[string]any{"ids": []string{ap.aIDs[0], ap.bIDs[0], "nohost-1"}, "note": "n"})
	require.Equal(t, http.StatusOK, code, "a partial failure is reported, not hidden in a status")
	require.Equal(t, []watch.AckResult{
		{ID: ap.aIDs[0], Outcome: watch.AckHandled},
		{ID: ap.bIDs[0], Outcome: watch.AckUnreachable},
		{ID: "nohost-1", Outcome: watch.AckNotFound},
	}, resp.Results)
	require.Equal(t, 1, resp.Handled)
	require.Equal(t, 2, ap.unhandledOn(t, ap.b), "b's event was not touched")
}

func TestAck_ADaemonWithoutTheRouteIsUnsupported(t *testing.T) {
	ap := newAckPair(t)
	ap.a.loop().setOld(idB, true)
	_, resp := ap.ackOn(t, ap.a, map[string]any{"ids": []string{ap.bIDs[0]}, "note": "n"})
	require.Equal(t, []watch.AckResult{{ID: ap.bIDs[0], Outcome: watch.AckUnsupported}}, resp.Results)
}

func TestAck_AgainOnAnotherHostIsAlreadyAndKeepsTheFirstNote(t *testing.T) {
	ap := newAckPair(t)
	ap.ackOn(t, ap.a, map[string]any{"ids": []string{ap.bIDs[0]}, "note": "first"})
	n := ap.bEv.n()
	_, resp := ap.ackOn(t, ap.a, map[string]any{"ids": []string{ap.bIDs[0]}, "note": "second"})
	require.Equal(t, []watch.AckResult{{ID: ap.bIDs[0], Outcome: watch.AckAlready}}, resp.Results)
	got, _ := ap.b.m.acks.Get(ap.bIDs[0])
	require.Equal(t, "first", got.Note)
	require.Equal(t, n, ap.bEv.n(), "nothing changed there: no push")
}

func TestAck_AllWithAHostAcksEveryPendingEventOfThatHostWithTheNote(t *testing.T) {
	ap := newAckPair(t)
	code, resp := ap.ackOn(t, ap.a, map[string]any{"all": true, "host": "a26", "note": "all of b"})
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, 2, resp.Handled)
	require.Equal(t, 0, ap.unhandledOn(t, ap.b))
	require.Equal(t, 2, ap.unhandledOn(t, ap.a), "a's own events are not touched")
	got, _ := ap.b.m.acks.Get(ap.bIDs[1])
	require.Equal(t, "all of b", got.Note, "the note goes with it")

	ap.a.loop().setDown(idB, true)
	_, resp = ap.ackOn(t, ap.a, map[string]any{"all": true, "host": "a26", "note": "n"})
	require.Len(t, resp.Results, 1)
	require.Equal(t, watch.AckUnreachable, resp.Results[0].Outcome, "an unreachable host is one line saying so")
}

func TestAck_AHostThatIsNotPairedIsRefused(t *testing.T) {
	ap := newAckPair(t)
	code, _ := ap.ackOn(t, ap.a, map[string]any{"all": true, "host": "nowhere", "note": "n"})
	require.Equal(t, http.StatusBadRequest, code)
	code, _ = ap.ackOn(t, ap.a, map[string]any{"ids": []string{ap.aIDs[0]}, "host": "a26", "note": "n"})
	require.Equal(t, http.StatusBadRequest, code, "--host is for --all")
}

func postPeerAck(to *daemon, p *middleware.Principal, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/peers/watch/ack", strings.NewReader(body))
	if p != nil {
		req = req.WithContext(middleware.WithPrincipal(req.Context(), *p))
	}
	rr := httptest.NewRecorder()
	to.mux.ServeHTTP(rr, req)
	return rr
}

// The peer route handles only the events this host recorded: an id that is another host's is refused as not found and is not passed
// on, so no chain or loop of forwards can form.
func TestPeerAck_OnlyEventsOfThisHostAndNeverForwarded(t *testing.T) {
	ap := newAckPair(t)
	hostA := &middleware.Principal{Kind: middleware.PrincipalHost, Alias: "mlab", HostID: idA}
	body := func(m map[string]any) string {
		m["to_host_id"], m["note"] = idB, "n"
		b, _ := json.Marshal(m)
		return string(b)
	}
	before := len(ap.b.loop().pathsNow())
	rr := postPeerAck(ap.b, hostA, body(map[string]any{"ids": []string{ap.aIDs[0], ap.bIDs[0]}}))
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	var resp struct {
		ackResponse
		HostID string `json:"host_id"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))
	require.Equal(t, idB, resp.HostID, "the caller checks the answer names the host it asked")
	require.Equal(t, []watch.AckResult{{ID: ap.aIDs[0], Outcome: watch.AckNotFound}, {ID: ap.bIDs[0], Outcome: watch.AckHandled}}, resp.Results)
	require.Equal(t, before, len(ap.b.loop().pathsNow()), "b made no call to anyone")
	require.False(t, ap.a.m.acks.Handled(ap.aIDs[0]), "and a's event was not touched")
}

func TestPeerAck_Refusals(t *testing.T) {
	ap := newAckPair(t)
	hostA := &middleware.Principal{Kind: middleware.PrincipalHost, Alias: "mlab", HostID: idA}
	ok := func(mut func(m map[string]any)) string {
		m := map[string]any{"to_host_id": idB, "ids": []string{ap.bIDs[0]}, "note": "n"}
		if mut != nil {
			mut(m)
		}
		b, _ := json.Marshal(m)
		return string(b)
	}
	require.Equal(t, http.StatusForbidden, postPeerAck(ap.b, nil, ok(nil)).Code)
	require.Equal(t, http.StatusForbidden, postPeerAck(ap.b, &middleware.Principal{Kind: middleware.PrincipalAdmin}, ok(nil)).Code)
	require.Equal(t, http.StatusConflict, postPeerAck(ap.b, hostA, ok(func(m map[string]any) { m["to_host_id"] = "c5n:dddddd" })).Code)
	for name, mut := range map[string]func(m map[string]any){
		"no note":     func(m map[string]any) { delete(m, "note") },
		"two lines":   func(m map[string]any) { m["note"] = "a\nb" },
		"no ids":      func(m map[string]any) { m["ids"] = []string{} },
		"ids and all": func(m map[string]any) { m["all"] = true },
		"a bad id":    func(m map[string]any) { m["ids"] = []string{"a b"} },
		"a bad by":    func(m map[string]any) { m["by"] = "x\ny" },
	} {
		require.Equal(t, http.StatusBadRequest, postPeerAck(ap.b, hostA, ok(mut)).Code, name)
	}
	require.Equal(t, http.StatusBadRequest, postPeerAck(ap.b, hostA, `{nope`).Code)
	require.Equal(t, http.StatusRequestEntityTooLarge, postPeerAck(ap.b, hostA, `{"to_host_id":"`+strings.Repeat("x", 70000)+`"}`).Code)
	require.False(t, ap.b.m.acks.Handled(ap.bIDs[0]), "none of the refused ones changed anything")
	require.Equal(t, 0, ap.bEv.n())
}

func TestAckRoutes_PeerRouteIsInThePolicyAndTheLocalOneIsStillAdminOnly(t *testing.T) {
	require.True(t, policyAllows("/api/peers/watch/ack"))
	require.False(t, policyAllows("/api/watch/ack"), "the local route is not a host route")
	ap := newAckPair(t)
	device := devices.WithPrincipal(context.Background(), devices.Principal{ID: "phone"})
	req := httptest.NewRequest(http.MethodPost, "/api/watch/ack", strings.NewReader(`{"ids":["`+ap.aIDs[0]+`"],"note":"n"}`)).WithContext(device)
	rr := httptest.NewRecorder()
	ap.a.mux.ServeHTTP(rr, req)
	require.Equal(t, http.StatusForbidden, rr.Code)
}

func TestAck_WhoGaveTheOrderTravelsWithIt(t *testing.T) {
	ap := newAckPair(t)
	ap.a.m.core.Registry.Register("peers.origin-resolver", fakeResolver{})
	ap.ackOn(t, ap.a, map[string]any{"ids": []string{ap.bIDs[0]}, "note": "n", "origin_inbox": "/tmp/x.sock"})
	got, _ := ap.b.m.acks.Get(ap.bIDs[0])
	require.Equal(t, "mlab/the-watcher", got.By, "the address of the session that gave the order, as a resolved it")
	require.Equal(t, "mlab", got.FromHost)
}

func policyAllows(path string) bool {
	return peersmod.HostRoutePolicy(httptest.NewRequest(http.MethodPost, path, nil))
}

// An id named twice is one event with one answer: the second mention does not hide that the first one was handled.
func TestAck_AnIDNamedTwiceIsOneEventWithOneAnswer(t *testing.T) {
	ap := newAckPair(t)
	_, resp := ap.ackOn(t, ap.a, map[string]any{"ids": []string{ap.aIDs[0], ap.bIDs[0], ap.aIDs[0], ap.bIDs[0]}, "note": "n"})
	require.Equal(t, []watch.AckResult{{ID: ap.aIDs[0], Outcome: watch.AckHandled}, {ID: ap.bIDs[0], Outcome: watch.AckHandled}}, resp.Results)
	require.Equal(t, 2, resp.Handled)

	hostA := &middleware.Principal{Kind: middleware.PrincipalHost, Alias: "mlab", HostID: idA}
	rr := postPeerAck(ap.b, hostA, `{"to_host_id":"`+idB+`","note":"n","ids":["`+ap.bIDs[1]+`","`+ap.bIDs[1]+`"]}`)
	var out ackPeerResponse
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &out))
	require.Equal(t, []watch.AckResult{{ID: ap.bIDs[1], Outcome: watch.AckHandled}}, out.Results)
	require.Equal(t, 1, out.Handled)
}

// A peer's answer is taken only for the ids that were sent to it, each once, with an outcome that exists: it cannot say what became of
// an id of this host or of another peer, and what it leaves out is reported as refused, not assumed.
func TestForwardAck_TakesOnlyWhatWasAskedFromTheAnswer(t *testing.T) {
	ap := newAckPair(t)
	entry, _, _ := ap.a.m.peerEntry("a26")
	fc := &fakeCaller{reply: map[string]peersmod.CallResult{}}
	ap.a.m.caller = fc
	asked := []string{"a26-1", "a26-2", "a26-3"}
	answer := ackPeerResponse{HostID: idB, ackResponse: ackResponse{Results: []watch.AckResult{
		{ID: "a26-1", Outcome: watch.AckHandled},
		{ID: "a26-1", Outcome: watch.AckAlready},    // twice: the first stands
		{ID: ap.aIDs[0], Outcome: watch.AckHandled}, // an id of this host that was not asked
		{ID: "a26-2", Outcome: "great"},             // an outcome that does not exist
		// a26-3 is not mentioned at all
	}}}
	raw, _ := json.Marshal(answer)
	fc.reply[pathAck] = peersmod.CallResult{Class: peersmod.ClassDone, Status: 200, Body: raw}
	got, _ := ap.a.m.forwardAck(context.Background(), entry, asked, false, "n", "")
	require.Equal(t, []watch.AckResult{
		{ID: "a26-1", Outcome: watch.AckHandled},
		{ID: "a26-2", Outcome: watch.AckRefused},
		{ID: "a26-3", Outcome: watch.AckRefused},
	}, got)

	// for "all" the answer may name the events that host had, and only those
	answer.Results = []watch.AckResult{{ID: "a26-9", Outcome: watch.AckHandled}, {ID: "mlab-1", Outcome: watch.AckHandled}, {ID: "a26-x y", Outcome: watch.AckHandled}}
	raw, _ = json.Marshal(answer)
	fc.reply[pathAck] = peersmod.CallResult{Class: peersmod.ClassDone, Status: 200, Body: raw}
	got, _ = ap.a.m.forwardAck(context.Background(), entry, nil, true, "n", "")
	require.Equal(t, []watch.AckResult{{ID: "a26-9", Outcome: watch.AckHandled}}, got)
}

// Two hosts that go by the same host name cannot be told apart by the id of an event: the ack is not sent to either, and the answer says so.
func TestAck_AnOwnerNameThatTwoHostsShareIsAmbiguousNotGuessed(t *testing.T) {
	ap := newAckPair(t)
	ap.a.m.core.CfgMu.Lock()
	ap.a.m.core.Cfg.Peers.Hosts = append(ap.a.m.core.Cfg.Peers.Hosts, config.PeerHost{Alias: "a26-twin", HostID: "a26:cccccc", URL: "http://x", Token: "t", InboundToken: "i"})
	ap.a.m.core.CfgMu.Unlock()
	acksSent := func() int { // the ack route calls (the count question to the targets goes to the same route)
		n := 0
		for _, p := range ap.a.loop().pathsNow() {
			if strings.HasSuffix(p, pathAck) {
				n++
			}
		}
		return n
	}
	before := acksSent()
	_, resp := ap.ackOn(t, ap.a, map[string]any{"ids": []string{ap.bIDs[0], ap.aIDs[0]}, "note": "n"})
	require.Equal(t, []watch.AckResult{{ID: ap.bIDs[0], Outcome: watch.AckAmbiguous}, {ID: ap.aIDs[0], Outcome: watch.AckHandled}}, resp.Results)
	require.LessOrEqual(t, acksSent()-before, 1, "no ack was sent to either a26 (the one call is the count question to the target)")
	require.False(t, ap.b.m.acks.Handled(ap.bIDs[0]))

	// a peer that goes by this host's own name: this host's events are not guessed at either
	ap.a.m.core.CfgMu.Lock()
	ap.a.m.core.Cfg.Peers.Hosts = append(ap.a.m.core.Cfg.Peers.Hosts, config.PeerHost{Alias: "mlab-twin", HostID: "mlab:dddddd", URL: "http://x", Token: "t", InboundToken: "i"})
	ap.a.m.core.CfgMu.Unlock()
	_, resp = ap.ackOn(t, ap.a, map[string]any{"ids": []string{ap.aIDs[1]}, "note": "n"})
	require.Equal(t, []watch.AckResult{{ID: ap.aIDs[1], Outcome: watch.AckAmbiguous}}, resp.Results)
}

// `--all --host X` says how many are left on X, not on the host that was asked.
func TestAck_AllOnAnotherHostReportsThatHostsCount(t *testing.T) {
	ap := newAckPair(t)
	ap.ackOn(t, ap.a, map[string]any{"all": true, "host": "a26", "note": "n"}) // a26 has nothing pending now
	_, resp := ap.ackOn(t, ap.a, map[string]any{"all": true, "host": "a26", "note": "again"})
	require.Equal(t, 0, resp.Handled)
	require.Equal(t, []HostLeft{{Host: "mlab", Unhandled: 2}, {Host: "a26", Unhandled: 0}}, resp.Hosts, "a26 has none left, a still has its own")
	require.Equal(t, 0, resp.Unhandled, "unhandled keeps its meaning: what is left on the host named")
	require.Equal(t, 2, ap.unhandledOn(t, ap.a), "a still has its own")
}

// The count after an ack is of every target host, not of this one only: acking an event of one host must not read as "all done" while
// another host still has some (#3143).
func TestAck_TheRemainingCountListsEveryTargetHost(t *testing.T) {
	ap := newAckPair(t)
	code, resp := ap.ackOn(t, ap.a, map[string]any{"ids": []string{ap.aIDs[0]}, "note": "n"})
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, []HostLeft{{Host: "mlab", Unhandled: 1}, {Host: "a26", Unhandled: 2}}, resp.Hosts, "a has one left, b still has both")
	require.Equal(t, 3, *resp.Remaining, "the sum")
	require.Equal(t, 1, resp.Unhandled, "and unhandled keeps its meaning: what is left on the host asked (a CLI that predates hosts reads it so)")

	// the ids of the other host: its own answer is used, a is asked for nothing it has already said
	code, resp = ap.ackOn(t, ap.a, map[string]any{"ids": []string{ap.bIDs[0]}, "note": "n"})
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, []HostLeft{{Host: "mlab", Unhandled: 1}, {Host: "a26", Unhandled: 1}}, resp.Hosts)

	// all, on this host
	code, resp = ap.ackOn(t, ap.a, map[string]any{"all": true, "note": "n"})
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, []HostLeft{{Host: "mlab", Unhandled: 0}, {Host: "a26", Unhandled: 1}}, resp.Hosts)
	require.Equal(t, 1, *resp.Remaining)
}

// A host that cannot be asked is "?" (-1) and makes the sum unknown; it is never counted as 0.
func TestAck_AHostThatCannotBeAskedIsUnknownNotZero(t *testing.T) {
	ap := newAckPair(t)
	ap.a.loop().setDown(idB, true)
	code, resp := ap.ackOn(t, ap.a, map[string]any{"ids": []string{ap.aIDs[0]}, "note": "n"})
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, []HostLeft{{Host: "mlab", Unhandled: 1}, {Host: "a26", Unhandled: -1}}, resp.Hosts)
	require.Equal(t, -1, *resp.Remaining)
	require.Equal(t, 1, resp.Unhandled, "this host's own count is still unhandled")
}

// The peer route answers a count-only question without needing ids or a note.
func TestAckPeer_CountOnlyAnswersTheCount(t *testing.T) {
	ap := newAckPair(t)
	n := ap.a.m.askLeft(context.Background(), "a26")
	require.Equal(t, 2, n)
}

// An ack that already took most of the caller's patience asks no further question: the other hosts' counts are unknown, the ack's own
// answer still goes out.
func TestAck_NoCountQuestionsWhenTheAckItselfTookTheTime(t *testing.T) {
	ap := newAckPair(t)
	began := time.Now().Add(-ackDeadline - time.Second)
	hosts, total := ap.a.m.leftEverywhere(context.Background(), began, 1, nil)
	require.Equal(t, []HostLeft{{Host: "mlab", Unhandled: 1}, {Host: "a26", Unhandled: -1}}, hosts)
	require.Equal(t, -1, total)
}
