package teammod

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/module/hostconfig"
	"github.com/wake/purdex/internal/team"
)

// realUnattended swaps the fixture's fake switch for the host config
// module's own store over the fixture's data dir, as the daemon wires it.
func (f *fixture) realUnattended() *hostconfig.Module {
	f.t.Helper()
	hc := hostconfig.New()
	if err := hc.Init(f.core); err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(func() { _ = hc.Stop(context.Background()) })
	f.m.unattended = hc
	return hc
}

var appDesk = team.Client{Kind: "app", Label: "Purdex.app @ air26"}

// putUnattended is the App's PUT; a 200 is decoded.
func (f *fixture) putUnattended(body any) (int, team.UnattendedView, []byte) {
	f.t.Helper()
	code, raw := f.do(http.MethodPut, UnattendedRoute, body)
	var v team.UnattendedView
	if code == http.StatusOK {
		if err := json.Unmarshal(raw, &v); err != nil {
			f.t.Fatalf("decode view: %v; body=%s", err, raw)
		}
	}
	return code, v, raw
}

func (f *fixture) switchTo(on bool) team.UnattendedView {
	f.t.Helper()
	code, v, raw := f.putUnattended(team.UnattendedPutRequest{On: &on, Client: appDesk})
	if code != http.StatusOK {
		f.t.Fatalf("PUT on=%v: %d %s", on, code, raw)
	}
	return v
}

func (f *fixture) getUnattended(query string) (int, team.UnattendedView, []byte) {
	f.t.Helper()
	code, raw := f.do(http.MethodGet, UnattendedRoute+query, nil)
	var v team.UnattendedView
	if code == http.StatusOK {
		if err := json.Unmarshal(raw, &v); err != nil {
			f.t.Fatalf("decode view: %v; body=%s", err, raw)
		}
	}
	return code, v, raw
}

// streamOf drains every event broadcast so far as "<type> <op>", and the
// states of the team.unattended ones.
func (f *fixture) streamOf() (ops []string, states []team.UnattendedState) {
	f.t.Helper()
	for {
		select {
		case raw := <-f.sub.SendCh():
			var ev core.HostEvent
			var v struct {
				Op    string               `json:"op"`
				State team.UnattendedState `json:"state"`
			}
			if json.Unmarshal(raw, &ev) != nil || json.Unmarshal([]byte(ev.Value), &v) != nil {
				f.t.Fatalf("decode %s", raw)
			}
			if ev.Type == team.RosterEventType {
				continue // the roster's tests read their own subscriber
			}
			ops = append(ops, ev.Type+" "+v.Op)
			if ev.Type == team.UnattendedEventType {
				states = append(states, v.State)
			}
		default:
			return ops, states
		}
	}
}

// D-U23-3 / rule 1: switching on approves the open lead and self_relay
// requests by unattended (a closed event each), then broadcasts changed.
// Mutation gate: drop the sweep → red.
func TestUnattendedPut_OnSweepsOpenRequestsAndBroadcastsChanged(t *testing.T) {
	f := newFixture(t)
	f.realUnattended()
	lead, relay := f.create(uid(1)), f.begin("sid-2")
	f.streamOf()
	v := f.switchTo(true)
	if !v.On || v.Since != f.clock.Load() || v.Swept != 2 || v.Pending != 0 || len(v.Approved) != 2 {
		t.Fatalf("view = %+v, want on since now, swept 2, pending 0, both listed", v)
	}
	for _, id := range []string{lead.ID, relay.RequestID} {
		a, _, _ := f.m.store.Get(id)
		assertDecidedByUnattended(t, a, f.clock.Load())
	}
	ops, states := f.streamOf()
	if want := []string{"approval.request closed", "approval.request closed", "team.unattended changed"}; !reflect.DeepEqual(ops, want) {
		t.Fatalf("events = %v, want %v", ops, want)
	}
	if !states[0].On || states[0].ChangedBy == nil || states[0].ChangedBy.Label != appDesk.Label {
		t.Fatalf("changed state = %+v", states[0])
	}
}

// Decision 5 / rule 1: a row the switch-on sweep could not approve is
// reported as pending, the PUT still answers 200, and the next tick
// approves it. Mutation gate: fail the PUT when pending > 0 → red.
func TestUnattendedPut_PendingIsReportedAndTheTickFinishesIt(t *testing.T) {
	f := newFixture(t)
	f.realUnattended()
	f.create(uid(1))
	relay := f.begin("sid-2")
	failed := false
	f.m.beforeAutoApprove = func(a team.Approval) error {
		if a.ID == relay.RequestID && !failed {
			failed = true
			return errors.New("database is locked")
		}
		return nil
	}
	if v := f.switchTo(true); v.Swept != 1 || v.Pending != 1 {
		t.Fatalf("view = swept %d pending %d, want 1, 1", v.Swept, v.Pending)
	}
	f.m.tick()
	a, _, _ := f.m.store.Get(relay.RequestID)
	assertDecidedByUnattended(t, a, f.clock.Load())
}

// SetUnattended is idempotent: a second on keeps since, sweeps nothing and
// broadcasts nothing. The open row (refused by a rule: its origin became a
// member) would be counted pending by a sweep.
func TestUnattendedPut_OnTwiceKeepsSinceAndSweepsNothing(t *testing.T) {
	f := newFixture(t)
	f.realUnattended()
	f.create(uid(1))
	f.makeMember("sid-1")
	if v := f.switchTo(true); v.Pending != 1 {
		t.Fatalf("first on: pending %d, want 1", v.Pending)
	}
	since := f.clock.Load()
	f.clock.Add(5_000)
	f.streamOf()
	v := f.switchTo(true)
	if !v.On || v.Since != since || v.Swept != 0 || v.Pending != 0 {
		t.Fatalf("second on = %+v, want on since %d, nothing swept or pending", v, since)
	}
	if ops, _ := f.streamOf(); len(ops) != 0 {
		t.Fatalf("events = %v, want none", ops)
	}
}

// Rule 3 (D-U23-6): off approves nothing more, leaves open requests open
// and keeps the list of what was auto-approved.
func TestUnattendedPut_OffKeepsTheListAndOpenRequests(t *testing.T) {
	f := newFixture(t)
	f.realUnattended()
	f.create(uid(1))
	f.makeMember("sid-1") // the lead row stays open: a rule refuses it
	f.switchTo(true)
	relay := f.begin("sid-2") // approved at begin
	f.clock.Add(1_000)
	v := f.switchTo(false)
	if v.On || v.Since != f.clock.Load()-1_000 || len(v.Approved) != 1 || v.Approved[0].ID != relay.RequestID {
		t.Fatalf("off view = %+v, want off, since kept, the self relay listed", v)
	}
	if a, _, _ := f.m.store.Get(uid(1)); a.State != team.StateOpen {
		t.Fatalf("lead row %s, want still open", a.State)
	}
	f.m.tick()
	if a, _, _ := f.m.store.Get(uid(1)); a.State != team.StateOpen {
		t.Fatalf("lead row %s after a tick with the switch off, want open", a.State)
	}
}

// D-U23-6: the list is what the daemon approved since the last switch-on;
// switching on again starts it over.
func TestUnattendedGet_ListsAutoApprovalsSinceTheLastOn(t *testing.T) {
	f := newFixture(t)
	f.realUnattended()
	if _, v, _ := f.getUnattended(""); v.On || v.Since != 0 || v.Approved == nil || len(v.Approved) != 0 {
		t.Fatalf("never on = %+v", v)
	}
	f.switchTo(true)
	lead, relay := f.create(uid(1)), f.begin("sid-2")
	f.switchTo(false)
	code, v, raw := f.getUnattended("")
	if code != http.StatusOK || v.On || !reflect.DeepEqual(ids(v.Approved), []string{lead.ID, relay.RequestID}) {
		t.Fatalf("GET after off: %d %s", code, raw)
	}
	f.clock.Add(1_000)
	f.switchTo(true)
	if _, v, raw := f.getUnattended(""); !v.On || len(v.Approved) != 0 || v.Truncated {
		t.Fatalf("GET after on again: %s, want an empty list", raw)
	}
}

// Decision 17: GET pages with before / limit; truncated and next_before
// chain the pages; a before or limit that is not a positive integer is 400.
func TestUnattendedGet_PagesWithBeforeAndTruncated(t *testing.T) {
	f := newFixture(t)
	f.unatt.set(true) // since 1
	for i, id := range []string{"r1", "r2", "r3", "r4", "r5"} {
		closedAt(t, f.m.store, id, int64(3000+100*i), team.UnattendedClient(), team.StateApproved)
	}
	query, pages := "?limit=2", [][]string{{"r5", "r4"}, {"r3", "r2"}, {"r1"}}
	for i, want := range pages {
		code, v, raw := f.getUnattended(query)
		last := i == len(pages)-1
		if code != http.StatusOK || !reflect.DeepEqual(ids(v.Approved), want) || v.Truncated == last || (v.NextBefore == 0) != last {
			t.Fatalf("page %d (%s): %d %s, want %v truncated=%v", i, query, code, raw, want, !last)
		}
		query = "?limit=2&before=" + jsonInt(v.NextBefore)
	}
	if _, v, _ := f.getUnattended("?limit=500"); len(v.Approved) != 5 {
		t.Fatalf("limit over the cap: %d rows", len(v.Approved))
	}
	for _, q := range []string{"?before=abc", "?before=0", "?before=-5", "?limit=0", "?limit=x", "?limit=-1"} {
		if code, raw := f.do(http.MethodGet, UnattendedRoute+q, nil); code != http.StatusBadRequest || decodeErr(t, raw).Error != team.ErrBadRequest {
			t.Fatalf("GET %s: %d %s, want 400 bad_request", q, code, raw)
		}
	}
}

func jsonInt(n int64) string { b, _ := json.Marshal(n); return string(b) }

// D-U23-2 / decision 6: only an app client turns the switch; a body
// without a boolean on, or with any other client, is 400 with nothing
// stored and nothing broadcast. Mutation gate: accept any client kind → red.
func TestUnattendedPut_RequiresAnAppClient(t *testing.T) {
	f := newFixture(t)
	hc := f.realUnattended()
	for _, body := range []string{
		`{"client":{"kind":"app","label":"Purdex.app @ air26"}}`,
		`{"on":"yes","client":{"kind":"app","label":"Purdex.app @ air26"}}`,
		`{"on":null,"client":{"kind":"app","label":"Purdex.app @ air26"}}`,
		`{"on":true}`,
		`{"on":true,"client":{"kind":"app","label":"  "}}`,
		`{"on":true,"client":{"kind":"unattended","label":"無人值守模式"}}`,
		`{"on":true,"client":{"kind":"terminal","label":"pdx"}}`,
		`not json`,
	} {
		if code, _, raw := f.putUnattended(body); code != http.StatusBadRequest || decodeErr(t, raw).Error != team.ErrBadRequest {
			t.Fatalf("PUT %s: %d %s, want 400 bad_request", body, code, raw)
		}
	}
	if st, err := hc.Unattended(); err != nil || st != (team.UnattendedState{}) {
		t.Fatalf("stored = %+v (%v), want never written", st, err)
	}
	if ops, _ := f.streamOf(); len(ops) != 0 {
		t.Fatalf("events = %v, want none", ops)
	}
}

// Rule 2: every change is audited — changed_by with the caller's address,
// stored with the state, and one log line; a PUT that changes nothing
// logs nothing.
func TestUnattendedPut_AuditLineAndChangedBy(t *testing.T) {
	f := newFixture(t)
	hc := f.realUnattended()
	logs := f.logs()
	v := f.switchTo(true)
	want := team.Client{Kind: "app", Label: appDesk.Label, Addr: "100.64.0.4:51234"}
	if st, err := hc.Unattended(); err != nil || st.ChangedBy == nil || *st.ChangedBy != want || v.ChangedBy == nil || *v.ChangedBy != want {
		t.Fatalf("stored = %+v (%v), answered %+v; want changed_by %+v", st, err, v.ChangedBy, want)
	}
	f.switchTo(true)
	f.switchTo(false)
	lines := logs()
	if n := countLines(lines, `[team] unattended on by app "Purdex.app @ air26" from 100.64.0.4:51234 (swept 0, pending 0)`); n != 1 {
		t.Fatalf("on lines %d in %q, want 1", n, lines)
	}
	if n := countLines(lines, `[team] unattended off by app "Purdex.app @ air26" from 100.64.0.4:51234`); n != 1 {
		t.Fatalf("off lines %d in %q, want 1", n, lines)
	}
}

// D-U23-6: changed is never lost silently. A subscriber that opted into
// nothing and whose buffer is full is closed by it (Done, deregistered),
// so its client reconnects for the snapshot instead of showing the switch
// off; a subscriber with room gets it. Mutation gate: broadcast changed
// best-effort (BroadcastEvent) → red.
func TestUnattendedPut_ChangedClosesASubscriberThatCannotTakeIt(t *testing.T) {
	f := newFixture(t)
	f.realUnattended()
	full := f.core.Events.AddTestSubscriber()
	t.Cleanup(func() { f.core.Events.RemoveTestSubscriber(full) })
	for full.TrySend([]byte(`{"type":"fill"}`)) {
	}
	f.switchTo(true)
	select {
	case <-full.Done():
	default:
		t.Fatal("a subscriber that could not take changed was kept without it")
	}
	if ops, _ := f.streamOf(); !reflect.DeepEqual(ops, []string{"team.unattended changed"}) {
		t.Fatalf("events on the subscriber with room = %v, want changed", ops)
	}
}

// A PUT whose write took effect answers 200 even when the list cannot be
// read: the state, the counts, approved [] and list_failed, so the App
// knows the switch is set and GETs the list; changed is sent. The same PUT
// again is 200 with nothing changed and nothing broadcast. GET, which
// writes nothing, still answers 500. Mutation gate: answer 500 when the
// PUT's list fails → red.
func TestUnattendedPut_ListFailureAfterTheWriteIs200(t *testing.T) {
	f := newFixture(t)
	hc := f.realUnattended()
	logs := f.logs()
	f.create(uid(1))
	f.streamOf()
	f.m.store.beforeListAutoApproved = func() error { return errors.New("disk I/O error") }
	v := f.switchTo(true)
	if !v.On || v.Since != f.clock.Load() || v.Swept != 1 || v.Pending != 0 || !v.ListFailed || v.Approved == nil || len(v.Approved) != 0 || v.Truncated {
		t.Fatalf("view = %+v, want on, swept 1, approved [], list_failed", v)
	}
	if st, err := hc.Unattended(); err != nil || !st.On {
		t.Fatalf("stored = %+v (%v), want on", st, err)
	}
	if ops, _ := f.streamOf(); !reflect.DeepEqual(ops, []string{"approval.request closed", "team.unattended changed"}) {
		t.Fatalf("events = %v, want the sweep's closed, then changed", ops)
	}
	if n := countLines(logs(), "[team] unattended list: list auto-approved: disk I/O error"); n != 1 {
		t.Fatalf("list failure lines = %d in %q, want 1", n, logs())
	}

	if again := f.switchTo(true); !again.On || again.Swept != 0 || !again.ListFailed {
		t.Fatalf("same PUT again = %+v, want on, nothing swept, list_failed", again)
	}
	if ops, _ := f.streamOf(); len(ops) != 0 {
		t.Fatalf("events after an unchanged PUT = %v, want none", ops)
	}
	if code, _, raw := f.getUnattended(""); code != http.StatusInternalServerError || decodeErr(t, raw).Error != errStorage {
		t.Fatalf("GET with a failing list = %d %s, want 500 %s", code, raw, errStorage)
	}
}

// The switch's route answers 503 once the module is stopping, writing
// nothing.
func TestUnattendedPut_StoppingIs503(t *testing.T) {
	f := newFixture(t)
	hc := f.realUnattended()
	_ = f.m.Stop(context.Background())
	on := true
	if code, _, raw := f.putUnattended(team.UnattendedPutRequest{On: &on, Client: appDesk}); code != http.StatusServiceUnavailable || decodeErr(t, raw).Error != team.ErrNotReady {
		t.Fatalf("PUT while stopping: %d %s, want 503 not_ready", code, raw)
	}
	if st, _ := hc.Unattended(); st.On {
		t.Fatal("stored on while stopping")
	}
}

// readUnattendedSnapshot connects a real WS subscriber (OnSubscribe runs
// only through HandleHostEvents) and returns its first team.unattended frame.
func readUnattendedSnapshot(t *testing.T, f *fixture) team.UnattendedEventValue {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(f.core.Events.HandleHostEvents))
	defer srv.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read snapshot: %v", err)
		}
		var ev core.HostEvent
		var v team.UnattendedEventValue
		if json.Unmarshal(raw, &ev) != nil || ev.Type != team.UnattendedEventType {
			continue
		}
		if err := json.Unmarshal([]byte(ev.Value), &v); err != nil {
			t.Fatal(err)
		}
		return v
	}
}

// D-U23-6: every window sees the state — each new subscriber gets a
// snapshot of it.
func TestUnattendedSnapshot_ToEveryNewSubscriber(t *testing.T) {
	f := newFixture(t)
	f.realUnattended()
	if err := f.m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if v := readUnattendedSnapshot(t, f); v.Op != "snapshot" || v.State.On {
		t.Fatalf("snapshot before any write = %+v", v)
	}
	f.switchTo(true)
	for range 2 {
		if v := readUnattendedSnapshot(t, f); v.Op != "snapshot" || !v.State.On || v.State.Since != f.clock.Load() {
			t.Fatalf("snapshot = %+v, want on since %d", v, f.clock.Load())
		}
	}
}

// D-U23-7: the switch and its list survive a restart: a second team
// module and host config module over the same data dir read them back.
func TestUnattended_SurvivesARestart(t *testing.T) {
	f := newFixture(t)
	hc := f.realUnattended()
	f.switchTo(true)
	lead := f.create(uid(1))
	_ = f.m.Stop(context.Background())
	_ = f.m.Close()
	_ = hc.Stop(context.Background())

	f.realUnattended() // a fresh host config module, registered for the next Init
	m2 := New()
	m2.logf, m2.now = func(string, ...any) {}, f.m.now
	if err := m2.Init(f.core); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m2.Stop(context.Background()); _ = m2.Close() })
	mux := http.NewServeMux()
	m2.RegisterRoutes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, UnattendedRoute, nil))
	var v team.UnattendedView
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil || rec.Code != http.StatusOK || !v.On || len(v.Approved) != 1 || v.Approved[0].ID != lead.ID {
		t.Fatalf("after restart: %d %s", rec.Code, rec.Body)
	}
}
