// internal/module/team/host_roots_reconciler_test.go — the lead host delivers its root changes to the member hosts, one worker per host
// (cross-host roots spec 2026-10-12 §3.2, XR-2a), against a scripted HostCaller.
package teammod

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	peersmod "github.com/wake/purdex/internal/module/peers"
	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/team"
)

// rootsFixture: a lead-host fixture with the fake caller; every host announces roots_change unless a test says otherwise.
func rootsFixture(t *testing.T, hosts ...string) (*fixture, *fakeHostCaller) {
	t.Helper()
	f, fc, _ := pumpFixture(t)
	fc.caps = map[string]ipeers.TeamCaps{}
	for _, h := range hosts {
		fc.caps[h] = ipeers.TeamCaps{Kinds: allKinds, AllowTeam: true, RootsChange: true}
	}
	return f, fc
}

func (f *fixture) change(id, host, op, path string) {
	f.t.Helper()
	if err := f.m.store.InsertHostRootChange(HostRootChange{ID: id, HostID: host, Op: op, Path: path, CreatedAt: f.clock.Load()}); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) changeRow(id string) HostRootChange {
	f.t.Helper()
	c, ok, err := f.m.store.RootChange(id)
	if err != nil || !ok {
		f.t.Fatalf("change %s: %v ok=%v", id, err, ok)
	}
	return c
}

// rootsOK is the member host's 200 answer to the request in body.
func rootsOK(body map[string]any, result, path string, roots []string, rev int64) peersmod.CallResult {
	if roots == nil {
		roots = []string{} // the member host always sends an array
	}
	b, _ := json.Marshal(team.RootsChangeResponse{ID: fmt.Sprint(body["id"]), HostID: fmt.Sprint(body["to_host_id"]), Op: fmt.Sprint(body["op"]),
		Result: result, Path: path, TeamRoots: roots, TeamRootsRev: rev})
	return peersmod.CallResult{Class: peersmod.ClassDone, Status: 200, Body: b}
}

func refusedWith(code, detail string) peersmod.CallResult {
	return peersmod.CallResult{Class: peersmod.ClassRefused, Status: 409, Code: code, Detail: detail}
}

func (fc *fakeHostCaller) callsTo(host string) []map[string]any {
	var out []map[string]any
	for _, c := range fc.sent() {
		if c.Host != host {
			continue
		}
		var m map[string]any
		_ = json.Unmarshal(c.Body, &m)
		m["_path"] = c.Path
		out = append(out, m)
	}
	return out
}

// A 200: the row is done with the member host's canonical spelling, and the cache holds what it answered.
func TestRootsReconciler_A200SettlesTheRowAndTheCache(t *testing.T) {
	f, fc := rootsFixture(t, "hostA")
	f.change("c1", "hostA", "add", "/link/w")
	fc.script = func(host string, body map[string]any) peersmod.CallResult {
		return rootsOK(body, "added", "/real/w", []string{"/real/w"}, 4)
	}
	f.m.drainRootsHost("hostA")
	c := f.changeRow("c1")
	if c.State != RootChangeDone || c.Canonical != "/real/w" {
		t.Fatalf("row = %+v", c)
	}
	seen, ok, _ := f.m.store.HostRootsSeen("hostA")
	if !ok || seen.Rev != 4 || len(seen.Roots) != 1 || seen.Roots[0] != "/real/w" {
		t.Fatalf("cache = %+v", seen)
	}
	calls := fc.callsTo("hostA")
	if len(calls) != 1 || calls[0]["_path"] != team.RootsChangeRoute || calls[0]["op"] != "add" || calls[0]["path"] != "/link/w" || calls[0]["to_host_id"] != "hostA" || calls[0]["id"] != "c1" {
		t.Fatalf("calls = %+v", calls)
	}
	if got := f.m.RootsHostStatus("hostA"); got != RootsStatusOK {
		t.Fatalf("status = %q", got)
	}
}

// The host's rows go in rowid order, one call each, all in one pass when each settles.
func TestRootsReconciler_RowsGoInOrderOneCallEach(t *testing.T) {
	f, fc := rootsFixture(t, "hostA")
	f.change("c1", "hostA", "add", "/w/1")
	f.change("c2", "hostA", "remove", "/w/2")
	f.change("c3", "hostA", "add", "/w/3")
	fc.script = func(host string, body map[string]any) peersmod.CallResult {
		return rootsOK(body, "unchanged", fmt.Sprint(body["path"]), nil, 1)
	}
	f.m.drainRootsHost("hostA")
	var ids []any
	for _, c := range fc.callsTo("hostA") {
		ids = append(ids, c["id"])
	}
	if fmt.Sprint(ids) != "[c1 c2 c3]" {
		t.Fatalf("order = %v", ids)
	}
	for _, id := range []string{"c1", "c2", "c3"} {
		if f.changeRow(id).State != RootChangeDone {
			t.Fatalf("%s = %+v", id, f.changeRow(id))
		}
	}
}

// A head that cannot be delivered stops its host for this pass: the later rows are not sent ahead of it, and when it goes through
// they follow in order.
// Mutation gate: go on to the next row after a failed head → the order assertion is red.
func TestRootsReconciler_AFailedHeadKeepsTheHostsOrder(t *testing.T) {
	f, fc := rootsFixture(t, "hostA")
	f.change("c1", "hostA", "add", "/w/1")
	f.change("c2", "hostA", "add", "/w/2")
	fail := true
	fc.script = func(host string, body map[string]any) peersmod.CallResult {
		if fail {
			return peersmod.CallResult{Class: peersmod.ClassTransient, Err: fmt.Errorf("timeout")}
		}
		return rootsOK(body, "added", fmt.Sprint(body["path"]), []string{fmt.Sprint(body["path"])}, 2)
	}
	f.m.drainRootsHost("hostA")
	if n := len(fc.callsTo("hostA")); n != 1 {
		t.Fatalf("%d calls, want 1 (the head only)", n)
	}
	if f.changeRow("c1").State != RootChangePending || f.changeRow("c2").State != RootChangePending {
		t.Fatal("a row moved on a failed head")
	}
	fail = false
	f.m.drainRootsHost("hostA")
	calls := fc.callsTo("hostA")
	if len(calls) != 3 || calls[1]["id"] != "c1" || calls[2]["id"] != "c2" {
		t.Fatalf("calls = %+v, want c1 (failed), c1, c2", calls)
	}
}

// Every JSON refusal ends that row as refused with its code and detail, and the host's next row still goes.
func TestRootsReconciler_EachJSONRefusalRefusesTheRowAndTheNextOneGoes(t *testing.T) {
	for _, c := range []struct {
		res    peersmod.CallResult
		status string
	}{
		{refusedWith("host_not_allowed", ""), RootsStatusConsentOff},
		{refusedWith("bad_root", "not_found"), RootsStatusOK},
		{refusedWith("bad_request", "path"), RootsStatusOK},
		{refusedWith("roots_in_use", "_abc123,_def456"), RootsStatusOK},
		{peersmod.CallResult{Class: peersmod.ClassWrongHost, Status: 409, Code: "wrong_host"}, RootsStatusOK},
	} {
		f, fc := rootsFixture(t, "hostA")
		f.change("c1", "hostA", "remove", "/w/1")
		f.change("c2", "hostA", "add", "/w/2")
		n := 0
		fc.script = func(host string, body map[string]any) peersmod.CallResult {
			n++
			if n == 1 {
				return c.res
			}
			return rootsOK(body, "added", "/w/2", []string{"/w/2"}, 3)
		}
		f.m.drainRootsHost("hostA")
		r := f.changeRow("c1")
		if r.State != RootChangeRefused || r.Code != c.res.Code || r.Detail != c.res.Detail {
			t.Fatalf("%s: row = %+v", c.res.Code, r)
		}
		if f.changeRow("c2").State != RootChangeDone {
			t.Fatalf("%s: the next row did not go: %+v", c.res.Code, f.changeRow("c2"))
		}
		if c.status == RootsStatusConsentOff {
			// the last result is the second row's success; consent_off is the status while the host keeps refusing
			f2, fc2 := rootsFixture(t, "hostA")
			f2.change("c9", "hostA", "add", "/w/9")
			fc2.script = func(string, map[string]any) peersmod.CallResult { return c.res }
			f2.m.drainRootsHost("hostA")
			if got := f2.m.RootsHostStatus("hostA"); got != RootsStatusConsentOff {
				t.Fatalf("status = %q, want consent_off", got)
			}
		}
	}
}

// Everything else leaves the row pending, stops the host for this pass and names the class.
func TestRootsReconciler_NonSettlingClassesKeepTheRowPending(t *testing.T) {
	for _, c := range []struct {
		name   string
		res    peersmod.CallResult
		status string
	}{
		{"transport", peersmod.CallResult{Class: peersmod.ClassTransient, Err: fmt.Errorf("refused")}, RootsStatusOffline},
		{"5xx", peersmod.CallResult{Class: peersmod.ClassTransient, Status: 503}, RootsStatusOffline},
		{"429", peersmod.CallResult{Class: peersmod.ClassTransient, Status: 429}, RootsStatusOffline},
		{"404", peersmod.CallResult{Class: peersmod.ClassUnsupported, Status: 404}, RootsStatusUnsupported},
		{"401", peersmod.CallResult{Class: peersmod.ClassUnauthorized, Status: 401}, RootsStatusUnauthorized},
	} {
		f, fc := rootsFixture(t, "hostA")
		f.change("c1", "hostA", "add", "/w/1")
		f.change("c2", "hostA", "add", "/w/2")
		fc.script = func(string, map[string]any) peersmod.CallResult { return c.res }
		f.m.drainRootsHost("hostA")
		if f.changeRow("c1").State != RootChangePending || f.changeRow("c2").State != RootChangePending {
			t.Fatalf("%s: a row left pending", c.name)
		}
		if n := len(fc.callsTo("hostA")); n != 1 {
			t.Fatalf("%s: %d calls, want 1", c.name, n)
		}
		if got := f.m.RootsHostStatus("hostA"); got != c.status {
			t.Fatalf("%s: status = %q, want %q", c.name, got, c.status)
		}
	}
}

func TestRootsReconciler_AHostWithoutTheCapabilityIsSkipped(t *testing.T) {
	f, fc := rootsFixture(t)
	fc.caps["hostA"] = ipeers.TeamCaps{Kinds: allKinds, AllowTeam: true} // no roots_change
	f.change("c1", "hostA", "add", "/w/1")
	f.m.drainRootsHost("hostA")
	if n := len(fc.callsTo("hostA")); n != 0 {
		t.Fatalf("%d calls to a host that does not announce roots_change", n)
	}
	if f.changeRow("c1").State != RootChangePending || f.m.RootsHostStatus("hostA") != RootsStatusUnsupported {
		t.Fatalf("row %+v, status %q", f.changeRow("c1"), f.m.RootsHostStatus("hostA"))
	}
	fc.capsErr = fmt.Errorf("unreachable") // capabilities that cannot be read: offline
	f.clock.Add(60_000)                    // past the caps cache
	f.m.drainRootsHost("hostA")
	if f.m.RootsHostStatus("hostA") != RootsStatusOffline {
		t.Fatalf("status = %q, want offline", f.m.RootsHostStatus("hostA"))
	}
}

// A host that is no longer paired: its rows are dropped (through unpairHost, in its transaction) and nothing is sent to it.
func TestRootsReconciler_AnUnpairedHostsRowsAreDropped(t *testing.T) {
	f, fc := rootsFixture(t, "hostA", "hostB")
	fc.paired = map[string]bool{"hostA": false, "hostB": true}
	f.change("c1", "hostA", "add", "/w/1")
	f.change("c2", "hostB", "add", "/w/2")
	f.m.drainRootsHost("hostA")
	if f.changeRow("c1").State != RootChangeDropped {
		t.Fatalf("row = %+v", f.changeRow("c1"))
	}
	if n := len(fc.callsTo("hostA")); n != 0 {
		t.Fatalf("%d calls to an unpaired host", n)
	}
	if f.changeRow("c2").State != RootChangePending {
		t.Fatal("another host's row was dropped")
	}
}

// The liveness tick's scan sees a host that has only root changes pending.
func TestRootsReconciler_ScanUnpairedSeesAHostWithOnlyRootChanges(t *testing.T) {
	f, fc := rootsFixture(t, "hostC")
	fc.paired = map[string]bool{"hostC": false}
	f.change("c1", "hostC", "add", "/w/1")
	f.m.scanUnpaired()
	if f.changeRow("c1").State != RootChangeDropped {
		t.Fatalf("row = %+v", f.changeRow("c1"))
	}
}

// The row and the cache are one transaction: a cache write that fails leaves the row pending (it is sent again; the request converges).
func TestRootsReconciler_ACacheFailureRollsTheRowBack(t *testing.T) {
	f, fc := rootsFixture(t, "hostA")
	f.change("c1", "hostA", "add", "/w/1")
	fc.script = func(host string, body map[string]any) peersmod.CallResult {
		return rootsOK(body, "added", "/w/1", []string{"/w/1"}, 2)
	}
	f.m.store.failBeforeRootsCache = func() error { return fmt.Errorf("disk full") }
	f.m.drainRootsHost("hostA")
	f.m.store.failBeforeRootsCache = nil
	if f.changeRow("c1").State != RootChangePending {
		t.Fatalf("row = %+v, want it still pending", f.changeRow("c1"))
	}
	f.m.drainRootsHost("hostA")
	if f.changeRow("c1").State != RootChangeDone {
		t.Fatalf("the retry did not settle: %+v", f.changeRow("c1"))
	}
}

// An answer that is not for this request (another id, unreadable) is not a settle.
func TestRootsReconciler_AnAnswerForAnotherRequestIsNotASettle(t *testing.T) {
	f, fc := rootsFixture(t, "hostA")
	f.change("c1", "hostA", "add", "/w/1")
	fc.script = func(host string, body map[string]any) peersmod.CallResult {
		other := map[string]any{"id": "someone-else", "to_host_id": host, "op": "add"}
		return rootsOK(other, "added", "/w/1", []string{"/w/1"}, 2)
	}
	f.m.drainRootsHost("hostA")
	if f.changeRow("c1").State != RootChangePending {
		t.Fatalf("row = %+v", f.changeRow("c1"))
	}
	if _, ok, _ := f.m.store.HostRootsSeen("hostA"); ok {
		t.Fatal("the cache took an answer for another request")
	}
	fc.script = func(string, map[string]any) peersmod.CallResult {
		return peersmod.CallResult{Class: peersmod.ClassDone, Status: 200, Body: json.RawMessage(`{not json`)}
	}
	f.m.drainRootsHost("hostA")
	if f.changeRow("c1").State != RootChangePending {
		t.Fatalf("row = %+v", f.changeRow("c1"))
	}
}

// A 200 that is not the whole answer to the request settles nothing and caches nothing (codex): only an id and a host, no grant after
// the request, another op, an unknown result.
// Mutation gate: accept a body with only id and host_id → the row is done with an empty cache (red).
func TestRootsReconciler_AnIncompleteSuccessBodyIsNotASettle(t *testing.T) {
	for name, body := range map[string]string{
		"only id and host": `{"id":"c1","host_id":"hostA"}`,
		"no grant":         `{"id":"c1","host_id":"hostA","op":"add","result":"added","path":"/w"}`,
		"null roots":       `{"id":"c1","host_id":"hostA","op":"add","result":"added","path":"/w","team_roots":null,"team_roots_rev":3}`,
		"no rev":           `{"id":"c1","host_id":"hostA","op":"add","result":"added","path":"/w","team_roots":["/w"]}`,
		"another op":       `{"id":"c1","host_id":"hostA","op":"remove","result":"removed","path":"/w","team_roots":[],"team_roots_rev":3}`,
		"unknown result":   `{"id":"c1","host_id":"hostA","op":"add","result":"maybe","path":"/w","team_roots":["/w"],"team_roots_rev":3}`,
		"another host":     `{"id":"c1","host_id":"hostB","op":"add","result":"added","path":"/w","team_roots":["/w"],"team_roots_rev":3}`,
	} {
		f, fc := rootsFixture(t, "hostA")
		f.change("c1", "hostA", "add", "/w")
		raw := body
		fc.script = func(string, map[string]any) peersmod.CallResult {
			return peersmod.CallResult{Class: peersmod.ClassDone, Status: 200, Body: json.RawMessage(raw)}
		}
		f.m.drainRootsHost("hostA")
		if f.changeRow("c1").State != RootChangePending {
			t.Fatalf("%s: row = %+v, want it pending", name, f.changeRow("c1"))
		}
		if _, ok, _ := f.m.store.HostRootsSeen("hostA"); ok {
			t.Fatalf("%s: the cache took an unusable answer", name)
		}
	}
	// an empty grant is a whole answer: "team_roots":[] with rev 0
	f, fc := rootsFixture(t, "hostA")
	f.change("c1", "hostA", "remove", "/w")
	fc.script = func(string, map[string]any) peersmod.CallResult {
		return peersmod.CallResult{Class: peersmod.ClassDone, Status: 200, Body: json.RawMessage(`{"id":"c1","host_id":"hostA","op":"remove","result":"unchanged","path":"","team_roots":[],"team_roots_rev":0}`)}
	}
	f.m.drainRootsHost("hostA")
	if f.changeRow("c1").State != RootChangeDone {
		t.Fatalf("row = %+v, an empty grant at rev 0 is a whole answer", f.changeRow("c1"))
	}
}

// A change that arrives after a worker's last empty look but before it gave its host back must not wait for the next period: the worker
// looks once more after giving the host back and wakes the coordinator.
// Mutation gate: drop that second look → the signal is not raised (red).
func TestRootsReconciler_AChangeArrivingAsAWorkerEndsWakesTheCoordinator(t *testing.T) {
	f, fc := rootsFixture(t, "hostA")
	f.change("c1", "hostA", "add", "/w/1")
	fc.script = func(host string, body map[string]any) peersmod.CallResult {
		return rootsOK(body, "added", fmt.Sprint(body["path"]), []string{fmt.Sprint(body["path"])}, 1)
	}
	f.m.roots.running = map[string]bool{"hostA": true}
	f.m.rootsAfterDrainEmpty = func() {
		f.change("c2", "hostA", "add", "/w/2") // arrives after the drain's last look
		f.m.kickRoots()
		f.m.rootsPass() // the coordinator consumes it, finds the host running and skips it
		select {
		case <-f.m.rootsSig:
		default:
		}
	}
	f.m.rootsWorker("hostA")
	f.m.rootsAfterDrainEmpty = nil
	select {
	case <-f.m.rootsSig:
	default:
		t.Fatal("the worker went away without waking the coordinator for the change that arrived as it ended")
	}
	if f.changeRow("c2").State != RootChangePending {
		t.Fatalf("c2 = %+v", f.changeRow("c2"))
	}
}

// A host that stopped on a failure is NOT woken again at once (that would be a tight loop on an offline host).
func TestRootsReconciler_AFailingHostIsNotRewokenByItsWorker(t *testing.T) {
	f, fc := rootsFixture(t, "hostA")
	f.change("c1", "hostA", "add", "/w/1")
	fc.script = func(string, map[string]any) peersmod.CallResult {
		return peersmod.CallResult{Class: peersmod.ClassTransient, Err: fmt.Errorf("down")}
	}
	f.m.roots.running = map[string]bool{"hostA": true}
	f.m.rootsWorker("hostA")
	select {
	case <-f.m.rootsSig:
		t.Fatal("a worker that stopped on a failure raised the signal")
	default:
	}
}

// Two hosts do not wait for each other: host A's call hangs (as a call to an offline host can for ~12 s), host B settles meanwhile; and a
// host has at most one call in flight, however many passes run.
// Mutation gate: drain the hosts one after the other on one goroutine → B waits for A (red).
func TestRootsReconciler_OneBlockedHostDoesNotDelayAnother(t *testing.T) {
	f, fc := rootsFixture(t, "hostA", "hostB")
	f.change("a1", "hostA", "add", "/w/a1")
	f.change("a2", "hostA", "add", "/w/a2")
	f.change("b1", "hostB", "add", "/w/b1")
	release := make(chan struct{})
	var mu sync.Mutex
	inFlightA, maxInFlightA := 0, 0
	fc.script = func(host string, body map[string]any) peersmod.CallResult {
		if host == "hostA" {
			mu.Lock()
			inFlightA++
			if inFlightA > maxInFlightA {
				maxInFlightA = inFlightA
			}
			mu.Unlock()
			<-release
			mu.Lock()
			inFlightA--
			mu.Unlock()
		}
		return rootsOK(body, "added", fmt.Sprint(body["path"]), []string{fmt.Sprint(body["path"])}, 1)
	}
	f.m.rootsPass()
	waitFor(t, func() bool { return f.changeRow("b1").State == RootChangeDone })
	f.m.rootsPass() // another pass while A is still in its call: no second worker for A
	f.m.rootsPass()
	time.Sleep(50 * time.Millisecond)
	if n := len(fc.callsTo("hostA")); n != 1 {
		t.Fatalf("%d calls to hostA in flight/made, want 1", n)
	}
	close(release)
	waitFor(t, func() bool {
		return f.changeRow("a1").State == RootChangeDone && f.changeRow("a2").State == RootChangeDone
	})
	mu.Lock()
	defer mu.Unlock()
	if maxInFlightA != 1 {
		t.Fatalf("up to %d calls to hostA at once", maxInFlightA)
	}
}

// The coordinator: started with the module, woken by a new row, joined at stop.
func TestRootsReconciler_StartWakeStop(t *testing.T) {
	f, fc := rootsFixture(t, "hostA")
	fc.script = func(host string, body map[string]any) peersmod.CallResult {
		return rootsOK(body, "added", fmt.Sprint(body["path"]), []string{fmt.Sprint(body["path"])}, 1)
	}
	f.m.startRootsReconciler()
	f.change("c1", "hostA", "add", "/w/1")
	f.m.kickRoots()
	waitFor(t, func() bool { return f.changeRow("c1").State == RootChangeDone })
	f.m.stopCancel()
	done := make(chan struct{})
	go func() { f.m.sweepWG.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("the reconciler did not stop")
	}
}

func TestRootsReconciler_NoCallerNoReconciler(t *testing.T) {
	f := newFixture(t)
	f.m.cmdCaller = nil
	f.m.startRootsReconciler() // must neither panic nor start anything
	f.m.kickRoots()
}

// ---- the 401 owner ----

// A 401 stamps the head row (kept in the database, so it survives a restart); any other answer clears it; ten minutes of 401 end the
// relation (unpairHost) and drop the host's root rows — with no team command pending at all.
func TestRootsReconciler_A401IsStampedAndTenMinutesUnpairTheHost(t *testing.T) {
	f, fc := rootsFixture(t, "hostA")
	f.change("c1", "hostA", "add", "/w/1")
	f.change("c2", "hostA", "add", "/w/2")
	answer := peersmod.CallResult{Class: peersmod.ClassUnauthorized, Status: 401}
	fc.script = func(string, map[string]any) peersmod.CallResult { return answer }
	f.m.drainRootsHost("hostA")
	first := f.clock.Load()
	if c := f.changeRow("c1"); c.First401At != first || c.State != RootChangePending {
		t.Fatalf("row = %+v, want first_401_at %d", c, first)
	}
	// "restart": the reconciler keeps no 401 state of its own, so a fresh one reads the stamp from the database
	f.m.roots.status, f.m.roots.caps = nil, nil
	f.clock.Add(5 * 60_000)
	f.m.drainRootsHost("hostA")
	if c := f.changeRow("c1"); c.First401At != first {
		t.Fatalf("row = %+v, the run must keep its first 401", c)
	}
	if got := f.m.RootsHostStatus("hostA"); got != RootsStatusUnauthorized {
		t.Fatalf("status = %q", got)
	}
	f.clock.Add(5*60_000 + 1)
	f.m.drainRootsHost("hostA")
	for _, id := range []string{"c1", "c2"} {
		if f.changeRow(id).State != RootChangeDropped {
			t.Fatalf("%s = %+v, want dropped after ten minutes of 401", id, f.changeRow(id))
		}
	}
}

// Mutation gate: do not clear the stamp on a non-401 → the later 401 run is counted from the old stamp (red).
func TestRootsReconciler_AnyOtherAnswerClearsThe401Stamp(t *testing.T) {
	f, fc := rootsFixture(t, "hostA")
	f.change("c1", "hostA", "add", "/w/1")
	answer := peersmod.CallResult{Class: peersmod.ClassUnauthorized, Status: 401}
	fc.script = func(string, map[string]any) peersmod.CallResult { return answer }
	f.m.drainRootsHost("hostA")
	if f.changeRow("c1").First401At == 0 {
		t.Fatal("not stamped")
	}
	answer = peersmod.CallResult{Class: peersmod.ClassTransient, Status: 503}
	f.clock.Add(9 * 60_000)
	f.m.drainRootsHost("hostA")
	if c := f.changeRow("c1"); c.First401At != 0 {
		t.Fatalf("row = %+v, want the stamp cleared by the 503", c)
	}
	answer = peersmod.CallResult{Class: peersmod.ClassUnauthorized, Status: 401}
	f.clock.Add(2 * 60_000) // 11 minutes after the first 401, but only a moment after this run began
	f.m.drainRootsHost("hostA")
	if c := f.changeRow("c1"); c.State != RootChangePending || c.First401At == 0 {
		t.Fatalf("row = %+v: a new run of 401s must start counting again", c)
	}
}

// The relation ends for the host, not only for its root rows: its commands are dropped by the same unpairHost.
func TestRootsReconciler_TheUnpairAlsoDropsTheHostsCommands(t *testing.T) {
	f, fc := rootsFixture(t, "hostA")
	f.change("c1", "hostA", "add", "/w/1")
	f.enqueue(f.cmd("cmd1", CmdRelease, "hostA", "mk1"))
	fc.script = func(string, map[string]any) peersmod.CallResult {
		return peersmod.CallResult{Class: peersmod.ClassUnauthorized, Status: 401}
	}
	f.m.drainRootsHost("hostA")
	f.clock.Add(10*60_000 + 1)
	f.m.drainRootsHost("hostA")
	if f.changeRow("c1").State != RootChangeDropped || f.cmdState("cmd1").State != cmdDropped {
		t.Fatalf("row %+v, command %+v", f.changeRow("c1"), f.cmdState("cmd1"))
	}
}
