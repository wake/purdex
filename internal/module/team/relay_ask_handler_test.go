package teammod

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/wake/purdex/internal/team"
)

// POST /api/relay/ask and its delivery (spec 2026-10-10-member-relay-ask §3.1): one write-first transaction under
// createMu, one notice per ask, a retry on every liveness tick until the notice is delivered or the ask closes.

func (f *fixture) postAsk(reqID, sid string, pct int) (int, team.RelayAskResponse, team.APIError) {
	f.t.Helper()
	code, body := f.do(http.MethodPost, "/api/relay/ask", team.RelayAskRequest{RequestID: reqID, SessionID: sid, UsedPct: pct, Window: 1000000})
	var resp team.RelayAskResponse
	var ae team.APIError
	if code == http.StatusOK {
		if err := json.Unmarshal(body, &resp); err != nil {
			f.t.Fatal(err)
		}
	} else {
		ae = decodeErr(f.t, body)
	}
	return code, resp, ae
}

// wantAskNotice is the lead's text for the fixture member (address self/w-one, ref _mem001, title worker).
func wantAskNotice(pct, minutes int) string {
	return fmt.Sprintf(team.RelayAskNoticeFmt, "self/w-one", "mem001", "worker", pct, minutes, "mem001")
}

func (f *fixture) sendFails(err error) {
	f.sender.mu.Lock()
	f.sender.err = err
	f.sender.mu.Unlock()
}

// livenessTick runs one liveness tick of the sweeper and waits for the goroutines it started.
func (f *fixture) livenessTick() {
	f.t.Helper()
	f.m.tickN = livenessEvery - 1
	f.m.tick()
	time.Sleep(80 * time.Millisecond)
}

func TestRelayAskNotice_FormatIsPinned(t *testing.T) {
	got := fmt.Sprintf(team.RelayAskNoticeFmt, "mlab/work-1", "abc123", "A 線", 71, 5, "abc123")
	want := "[pdx team] member mlab/work-1 [abc123]「A 線」已用 71%，申請接力。\n5 分鐘內同意請執行：pdx relay _abc123（不同意不用回覆，過期即作罷）"
	if got != want {
		t.Fatalf("notice = %q, want %q", got, want)
	}
}

func TestRelayAsk_OpensAnAskAndTellsTheLeadOnce(t *testing.T) {
	f := newFixture(t)
	f.memberTeam("3")
	code, resp, _ := f.postAsk(rid(600), "sid-m1", 71)
	if code != 200 || resp.ID != rid(600) || resp.State != team.RelayAskOpen || resp.Replay || resp.ExpiresAt != f.clock.Load()+300_000 {
		t.Fatalf("ask: %d %+v", code, resp)
	}
	time.Sleep(100 * time.Millisecond)
	calls := f.sender.calls()
	if len(calls) != 1 || calls[0].Text != wantAskNotice(71, 5) {
		t.Fatalf("notices = %+v", calls)
	}
	if a := f.ask(rid(600)); a.NotifiedAt == 0 || a.UsedPct != 71 {
		t.Fatalf("stored: %+v", a)
	}
	// the sweeper does not send it again
	f.livenessTick()
	if n := len(f.sender.calls()); n != 1 {
		t.Fatalf("%d notices after a tick", n)
	}
}

func TestRelayAsk_AReplayIsTheSameAskAndSendsNothing(t *testing.T) {
	f := newFixture(t)
	f.memberTeam("3")
	f.postAsk(rid(610), "sid-m1", 71)
	time.Sleep(100 * time.Millisecond)
	for _, id := range []string{rid(610), rid(611)} { // the same request, then another one while it is open
		code, resp, _ := f.postAsk(id, "sid-m1", 72)
		if code != 200 || resp.ID != rid(610) || !resp.Replay {
			t.Fatalf("replay %s: %d %+v", id, code, resp)
		}
	}
	time.Sleep(100 * time.Millisecond)
	if n := len(f.sender.calls()); n != 1 {
		t.Fatalf("%d notices, want 1", n)
	}
	// a request id that belongs to another session's ask is no replay of this one
	if code, _, ae := f.postAsk(rid(610), "sid-other", 72); code != 400 && code != 409 {
		t.Fatalf("another session's id: %d %+v", code, ae)
	}
}

func TestRelayAsk_Refusals(t *testing.T) {
	f := newFixture(t)
	f.memberTeam("3")
	for _, c := range []struct {
		name   string
		req    team.RelayAskRequest
		status int
		code   string
	}{
		{"request id not a UUID", team.RelayAskRequest{RequestID: "nope", SessionID: "sid-m1", UsedPct: 71}, 400, team.ErrBadRequest},
		{"no session", team.RelayAskRequest{RequestID: rid(620), UsedPct: 71}, 400, team.ErrBadRequest},
		{"percentage over 100", team.RelayAskRequest{RequestID: rid(621), SessionID: "sid-m1", UsedPct: 101}, 400, team.ErrBadRequest},
		{"not a member", team.RelayAskRequest{RequestID: rid(622), SessionID: "sid-nobody", UsedPct: 71}, 409, team.ErrNotMember},
		{"the lead is no member", team.RelayAskRequest{RequestID: rid(623), SessionID: "sid-1", UsedPct: 71}, 409, team.ErrNotMember},
	} {
		code, body := f.do(http.MethodPost, "/api/relay/ask", c.req)
		if code != c.status || (c.code != "" && decodeErr(t, body).Error != c.code) {
			t.Errorf("%s: %d %s", c.name, code, body)
		}
	}
	time.Sleep(60 * time.Millisecond)
	if n := len(f.sender.calls()); n != 0 {
		t.Fatalf("a refused ask told the lead (%d)", n)
	}
}

func TestRelayAsk_ARemoteMemberIsRelayUnsupported(t *testing.T) {
	f := newFixture(t)
	f.memberTeam("3")
	if _, err := f.m.store.db.Exec(`UPDATE team_members SET host_id = 'elsewhere:9' WHERE spawn_op = 'op-m1'`); err != nil {
		t.Fatal(err)
	}
	if code, _, ae := f.postAsk(rid(630), "sid-m1", 71); code != 409 || ae.Error != team.ErrRelayUnsupported {
		t.Fatalf("remote: %d %+v", code, ae)
	}
}

// The two race orders against the lead's relay (strictly ordered by createMu and the write-first transactions).
func TestRelayAsk_AskFirstTheRelayAcceptsIt_RelayFirstTheAskIsRefused(t *testing.T) {
	f := gateFixture(t, false, false, 0)
	if code, _, _ := f.postAsk(rid(640), "sid-m1", 71); code != 200 {
		t.Fatalf("ask: %d", code)
	}
	code, op, _ := f.createRelay(rid(641), "/tmp/10.sock", "_mem001")
	if a := f.ask(rid(640)); code != 201 || a.State != team.RelayAskAccepted || a.OpID != op.ID {
		t.Fatalf("ask first: create %d, ask %+v", code, a)
	}

	g := gateFixture(t, false, false, 0)
	if code, _, _ := g.createRelay(rid(642), "/tmp/10.sock", "_mem001"); code != 201 {
		t.Fatalf("relay: %d", code)
	}
	if code, _, ae := g.postAsk(rid(643), "sid-m1", 71); code != 409 || ae.Error != team.ErrRelayOpen {
		t.Fatalf("relay first: %d %+v", code, ae)
	}
	var n int
	g.m.store.db.QueryRow(`SELECT COUNT(*) FROM relay_asks`).Scan(&n)
	if n != 0 {
		t.Fatalf("a refused ask left %d rows", n)
	}
}

// Mutation gate: drop createMu from the handler → the request completes while the mutex is held (red).
func TestRelayAsk_HoldsCreateMu(t *testing.T) {
	f := newFixture(t)
	f.memberTeam("3")
	f.m.createMu.Lock()
	done := make(chan int, 1)
	go func() { code, _, _ := f.postAsk(rid(650), "sid-m1", 71); done <- code }()
	select {
	case <-done:
		f.m.createMu.Unlock()
		t.Fatal("the ask completed while createMu was held")
	case <-time.After(150 * time.Millisecond):
	}
	f.m.createMu.Unlock()
	select {
	case code := <-done:
		if code != 200 {
			t.Fatalf("ask: %d", code)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the ask never completed")
	}
}

// A send that fails leaves notified_at at 0; every liveness tick sends again with the minutes left, until it goes.
// Mutation gate: drop the retry → the second send never happens (red).
func TestRelayAsk_AFailedNoticeIsRetriedWithTheMinutesLeft(t *testing.T) {
	f := newFixture(t)
	f.memberTeam("3")
	f.sendFails(errors.New("down"))
	f.postAsk(rid(660), "sid-m1", 71)
	time.Sleep(100 * time.Millisecond)
	if n := len(f.sender.calls()); n != 0 || f.ask(rid(660)).NotifiedAt != 0 {
		t.Fatalf("sent %d, notified_at %d", n, f.ask(rid(660)).NotifiedAt)
	}
	created := f.ask(rid(660)).CreatedAt
	f.clock.Add(130 * 1000) // 2m10s gone: 2m50s left → "3"
	f.livenessTick()        // still failing
	if f.ask(rid(660)).NotifiedAt != 0 {
		t.Fatal("notified while the send fails")
	}
	f.sendFails(nil)
	f.livenessTick()
	calls := f.sender.calls()
	if len(calls) != 1 || calls[0].Text != wantAskNotice(71, 3) {
		t.Fatalf("retry = %+v", calls)
	}
	if a := f.ask(rid(660)); a.NotifiedAt == 0 || a.ExpiresAt != created+300_000 {
		t.Fatalf("the window must not restart: %+v", a)
	}
	f.livenessTick()
	if n := len(f.sender.calls()); n != 1 {
		t.Fatalf("%d notices after delivery", n)
	}
}

// A notice the daemon could not even start (it is stopping) stays owed: notified_at 0.
func TestRelayAsk_ATrackedRefusalLeavesTheNoticeOwed(t *testing.T) {
	f := newFixture(t)
	f.memberTeam("3")
	a, _, err := f.m.store.CreateRelayAsk(RelayAsk{ID: rid(670), SessionID: "sid-m1", UsedPct: 71, CreatedAt: f.clock.Load(), ExpiresAt: f.clock.Load() + 300_000})
	if err != nil {
		t.Fatal(err)
	}
	f.m.stopCancel()
	if f.m.notifyAskAsync(a) {
		t.Fatal("a stopping daemon accepted the notice")
	}
	if got := f.ask(rid(670)); got.NotifiedAt != 0 || len(f.sender.calls()) != 0 {
		t.Fatalf("ask %+v, sent %d", got, len(f.sender.calls()))
	}
}

// An expired ask is never notified, by the sweeper or by a late first send.
func TestRelayAsk_AnExpiredAskIsNeverNotified(t *testing.T) {
	f := newFixture(t)
	f.memberTeam("3")
	f.sendFails(errors.New("down"))
	f.postAsk(rid(680), "sid-m1", 71)
	time.Sleep(100 * time.Millisecond)
	f.sendFails(nil)
	f.clock.Add(300_000)
	f.livenessTick()
	if n := len(f.sender.calls()); n != 0 {
		t.Fatalf("%d notices for an expired ask", n)
	}
	a, _, _ := f.m.store.GetRelayAsk(rid(680))
	f.m.sendAskNotice(a)
	if n := len(f.sender.calls()); n != 0 {
		t.Fatalf("a late send reached the lead (%d)", n)
	}
}

// The sweeper's retry and the first send never both tell the lead.
func TestRelayAsk_ConcurrentSendsTellOnce(t *testing.T) {
	f := newFixture(t)
	f.memberTeam("3")
	a, _, err := f.m.store.CreateRelayAsk(RelayAsk{ID: rid(690), SessionID: "sid-m1", UsedPct: 71, CreatedAt: f.clock.Load(), ExpiresAt: f.clock.Load() + 300_000})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	for range 4 {
		go func() { f.m.sendAskNotice(a); done <- struct{}{} }()
	}
	for range 4 {
		<-done
	}
	if n := len(f.sender.calls()); n != 1 {
		t.Fatalf("%d notices from four concurrent sends", n)
	}
}
