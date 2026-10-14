package peers

import (
	"context"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wake/purdex/internal/config"
	ipeers "github.com/wake/purdex/internal/peers"
)

// snapRow is a remote row pinned to one live cc entry (the only kind that
// carries a name), already in the shape normalizeRemoteRows leaves it.
func snapRow(name, procStart string) ipeers.PeerRecord {
	return ipeers.PeerRecord{
		Name: name, Ref: "_k3m9qz", RowKind: "session",
		Agent: &ipeers.AgentInfo{Type: "cc", PID: 4242, ProcStart: procStart},
	}
}

const (
	snapStartA = "Sun Sep 13 15:22:36 2026"
	snapStartB = "Mon Sep 14 09:00:00 2026"
)

func mustStart(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := ipeers.ParseProcStart(s)
	if err != nil {
		t.Fatal(err)
	}
	return ts
}

func TestRemoteNameSnapshot_AnAnswerRecordsTheNamesItsLiveRowsHold(t *testing.T) {
	t0 := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	s := newRemoteNameSnapshot()
	s.record("h1", []ipeers.PeerRecord{snapRow("purdex-adam", snapStartA), snapRow("purdex-3f-vq", snapStartA)}, ipeers.AddressVersionV5, t0)
	got := s.held(t0)
	if len(got) != 1 || !got["adam"].Equal(mustStart(t, snapStartA)) {
		t.Fatalf("held = %v, want only adam started %s", got, snapStartA)
	}
	// A second answer replaces the host's set.
	s.record("h1", []ipeers.PeerRecord{snapRow("lead-emma", snapStartB)}, ipeers.AddressVersionV5, t0.Add(30*time.Second))
	got = s.held(t0.Add(30 * time.Second))
	if _, still := got["adam"]; still || !got["emma"].Equal(mustStart(t, snapStartB)) {
		t.Fatalf("held = %v, want only emma", got)
	}
}

func TestRemoteNameSnapshot_AHostThatStopsAnsweringHoldsItsLastSetForFiveMinutesThenNothing(t *testing.T) {
	t0 := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	s := newRemoteNameSnapshot()
	s.record("h1", []ipeers.PeerRecord{snapRow("purdex-adam", snapStartA)}, ipeers.AddressVersionV5, t0)
	if len(s.held(t0.Add(remoteNamesTTL-time.Second))) != 1 {
		t.Fatal("a name vanished before the 5 minutes were up")
	}
	if got := s.held(t0.Add(remoteNamesTTL)); len(got) != 0 {
		t.Fatalf("held = %v after the TTL, want none", got)
	}
}

func TestRemoteNameSnapshot_RowsThatMustNotHoldANameDoNot(t *testing.T) {
	t0 := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	dead := snapRow("purdex-adam", snapStartA)
	dead.Agent.PID = 0
	notAddressable := snapRow("purdex-emma", snapStartA)
	notAddressable.Agent.Type = "proxy"
	unroutable := snapRow("Purdex Kenji", snapStartA)
	notPool := snapRow("purdex-3f-vq", snapStartA)
	s := newRemoteNameSnapshot()
	s.record("h1", []ipeers.PeerRecord{dead, notAddressable, unroutable, notPool}, ipeers.AddressVersionV5, t0)
	if got := s.held(t0); len(got) != 0 {
		t.Fatalf("held = %v, want none", got)
	}
	// A pre-v5 daemon names by registry name: whatever it prints is not a
	// pool name this host can trust.
	s.record("h2", []ipeers.PeerRecord{snapRow("purdex-yuki", snapStartA)}, ipeers.AddressVersionV5-1, t0)
	if got := s.held(t0); len(got) != 0 {
		t.Fatalf("held = %v for a pre-v5 answer, want none", got)
	}
}

func TestRemoteNameSnapshot_TwoHoldersOfOneNameKeepTheEarlierStart(t *testing.T) {
	t0 := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	s := newRemoteNameSnapshot()
	s.record("h1", []ipeers.PeerRecord{snapRow("a-adam", snapStartB)}, ipeers.AddressVersionV5, t0)
	s.record("h2", []ipeers.PeerRecord{snapRow("b-adam", snapStartA)}, ipeers.AddressVersionV5, t0)
	if got := s.held(t0)["adam"]; !got.Equal(mustStart(t, snapStartA)) {
		t.Fatalf("adam start = %s, want the earlier %s", got, snapStartA)
	}
}

// Two rows of one answer holding one name keep the earlier start, whichever
// order the rows come in.
func TestRemoteNameSnapshot_TwoRowsOfOneAnswerKeepTheEarlierStartInEitherOrder(t *testing.T) {
	t0 := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	early, late := snapRow("a-adam", snapStartA), snapRow("b-adam", snapStartB)
	for _, rows := range [][]ipeers.PeerRecord{{early, late}, {late, early}} {
		s := newRemoteNameSnapshot()
		s.record("h1", rows, ipeers.AddressVersionV5, t0)
		if got := s.held(t0)["adam"]; !got.Equal(mustStart(t, snapStartA)) {
			t.Fatalf("adam start = %s, want the earlier %s", got, snapStartA)
		}
	}
}

// A holder whose proc_start is missing or unparsable counts from the first
// time this daemon saw it, and keeps that time while it stays in the answers.
func TestRemoteNameSnapshot_AnUnparsableProcStartCountsFromTheFirstSighting(t *testing.T) {
	t0 := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	s := newRemoteNameSnapshot()
	row := snapRow("purdex-adam", "not a time")
	s.record("h1", []ipeers.PeerRecord{row}, ipeers.AddressVersionV5, t0)
	s.record("h1", []ipeers.PeerRecord{row}, ipeers.AddressVersionV5, t0.Add(time.Minute))
	if got := s.held(t0.Add(time.Minute))["adam"]; !got.Equal(t0) {
		t.Fatalf("start = %s, want the first sighting %s", got, t0)
	}
	// Gone for one answer, then back: a new holder, a new first sighting.
	s.record("h1", nil, ipeers.AddressVersionV5, t0.Add(2*time.Minute))
	s.record("h1", []ipeers.PeerRecord{row}, ipeers.AddressVersionV5, t0.Add(3*time.Minute))
	if got := s.held(t0.Add(3 * time.Minute))["adam"]; !got.Equal(t0.Add(3 * time.Minute)) {
		t.Fatalf("start = %s, want the new sighting", got)
	}
}

func TestRemoteNameSnapshot_PruneForgetsHostsThatAreNoLongerConfigured(t *testing.T) {
	t0 := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	s := newRemoteNameSnapshot()
	s.record("h1", []ipeers.PeerRecord{snapRow("a-adam", snapStartA)}, ipeers.AddressVersionV5, t0)
	s.record("h2", []ipeers.PeerRecord{snapRow("b-emma", snapStartA)}, ipeers.AddressVersionV5, t0)
	s.prune(map[string]bool{"h2": true})
	got := s.held(t0)
	if _, gone := got["adam"]; gone || len(got) != 1 {
		t.Fatalf("held = %v, want only h2's emma", got)
	}
}

// poolFetchModule is a Module literal with just what fetchHostResult needs.
func poolFetchModule(fetch fetchFunc, now func() time.Time) *Module {
	return &Module{
		fetch:       fetch,
		client:      &http.Client{},
		now:         now,
		logf:        func(string, ...any) {},
		remoteNames: newRemoteNameSnapshot(),
	}
}

func poolHost() config.PeerHost {
	return config.PeerHost{Alias: "air", HostID: "host-air", URL: "http://air.invalid", Token: "tok"}
}

func okEnvelope(rows ...ipeers.PeerRecord) ipeers.Envelope {
	return ipeers.Envelope{HostID: "host-air", OK: true, AddressVersion: ipeers.AddressVersionV5, Peers: rows}
}

func TestFetchHostResult_AnOKAnswerFeedsTheSnapshotFromTheNormalizedRows(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	forgedDead := snapRow("evil-adam", snapStartA)
	forgedDead.Agent.PID = 0 // a dead row's name is cleared by normalizeRemoteRows
	forgedProxy := snapRow("evil-emma", snapStartA)
	forgedProxy.Agent.Type = "proxy"
	forgedBad := snapRow("Evil Kenji", snapStartA)
	m := poolFetchModule(func(context.Context, *http.Client, string, string) (ipeers.Envelope, error) {
		return okEnvelope(snapRow("purdex-yuki", snapStartA), forgedDead, forgedProxy, forgedBad), nil
	}, func() time.Time { return now })
	res := m.fetchHostResult(context.Background(), poolHost())
	if !res.OK {
		t.Fatalf("result not OK: %+v", res)
	}
	got := m.remoteNames.held(now)
	if len(got) != 1 || got["yuki"].IsZero() {
		t.Fatalf("held = %v, want only yuki: a row normalizeRemoteRows clears must not hold", got)
	}
}

func TestFetchHostResult_AFailedAnswerKeepsThePreviousSet(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	var fail atomic.Bool
	m := poolFetchModule(func(context.Context, *http.Client, string, string) (ipeers.Envelope, error) {
		if fail.Load() {
			return ipeers.Envelope{}, context.DeadlineExceeded
		}
		return okEnvelope(snapRow("purdex-adam", snapStartA)), nil
	}, func() time.Time { return now })
	m.fetchHostResult(context.Background(), poolHost())
	fail.Store(true)
	if res := m.fetchHostResult(context.Background(), poolHost()); res.OK {
		t.Fatal("the failing fetch reported OK")
	}
	// An ok=false answer and a host_id mismatch are failures too.
	m.fetch = func(context.Context, *http.Client, string, string) (ipeers.Envelope, error) {
		e := okEnvelope()
		e.OK = false
		return e, nil
	}
	m.fetchHostResult(context.Background(), poolHost())
	m.fetch = func(context.Context, *http.Client, string, string) (ipeers.Envelope, error) {
		e := okEnvelope()
		e.HostID = "some-other-host"
		return e, nil
	}
	m.fetchHostResult(context.Background(), poolHost())
	if _, held := m.remoteNames.held(now.Add(time.Minute))["adam"]; !held {
		t.Fatal("a failed answer dropped the previous set")
	}
}

// A remote whose rows claim every pool name only removes candidates; nothing
// else about the snapshot changes (T4 covers the picker's fallback).
func TestRemoteNameSnapshot_ARemoteClaimingEveryPoolNameHoldsThirty(t *testing.T) {
	t0 := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	var rows []ipeers.PeerRecord
	for _, n := range ipeers.NamePool {
		rows = append(rows, snapRow("x-"+n, snapStartA))
	}
	s := newRemoteNameSnapshot()
	s.record("h1", rows, ipeers.AddressVersionV5, t0)
	if got := s.held(t0); len(got) != len(ipeers.NamePool) {
		t.Fatalf("held %d names, want %d", len(got), len(ipeers.NamePool))
	}
}

func poolRefreshModule(t *testing.T, hosts []config.PeerHost, fetch fetchFunc) *Module {
	t.Helper()
	m := newTestModule(t, newTestCoreWithHosts(t, "host-mlab", "mlab", hosts), &fakeSessions{}, &fakeOwners{}, "",
		ipeers.DefaultLiveness(), &fakeClock{times: []time.Time{time.Now()}}, 2*time.Second)
	m.fetch = fetch
	m.remoteNames = newRemoteNameSnapshot()
	m.now = time.Now
	m.namePoolRefresh = 5 * time.Millisecond
	return m
}

func TestNamePoolRefresher_FetchesEveryConfiguredHostOnATickAndStopEndsIt(t *testing.T) {
	var mu sync.Mutex
	calls := map[string]int{}
	m := poolRefreshModule(t, []config.PeerHost{
		{Alias: "air", HostID: "host-air", URL: "http://air.invalid", Token: "tok"},
		{Alias: "mini", HostID: "host-mini", URL: "http://mini.invalid", Token: "tok"},
	}, func(_ context.Context, _ *http.Client, base, _ string) (ipeers.Envelope, error) {
		mu.Lock()
		calls[base]++
		mu.Unlock()
		id := "host-air"
		if base == "http://mini.invalid" {
			id = "host-mini"
		}
		e := okEnvelope(snapRow("p-"+ipeers.NamePool[len(base)%30], snapStartA))
		e.HostID = id
		return e, nil
	})
	m.startNamePoolRefresher()
	deadline := time.Now().Add(10 * time.Second)
	for {
		mu.Lock()
		done := calls["http://air.invalid"] >= 2 && calls["http://mini.invalid"] >= 2
		mu.Unlock()
		if done {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("refresher did not fetch both hosts twice: %v", calls)
		}
		time.Sleep(2 * time.Millisecond)
	}
	if len(m.remoteNames.held(time.Now())) == 0 {
		t.Fatal("the refresher did not fill the snapshot")
	}
	if err := m.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	before := calls["http://air.invalid"]
	mu.Unlock()
	time.Sleep(60 * time.Millisecond)
	mu.Lock()
	after := calls["http://air.invalid"]
	mu.Unlock()
	if after != before {
		t.Fatalf("the refresher kept fetching after Stop: %d -> %d", before, after)
	}
}

// The first fetch happens when the refresher starts, not one period later: a
// daemon that has just started must not name conversations blind to the other
// hosts for 30 seconds (codex R1).
func TestNamePoolRefresher_FetchesOnceImmediatelyAtStart(t *testing.T) {
	got := make(chan struct{}, 4)
	m := poolRefreshModule(t, []config.PeerHost{{Alias: "air", HostID: "host-air", URL: "http://air.invalid", Token: "tok"}},
		func(context.Context, *http.Client, string, string) (ipeers.Envelope, error) {
			got <- struct{}{}
			return okEnvelope(snapRow("p-adam", snapStartA)), nil
		})
	m.namePoolRefresh = time.Hour // the ticker never fires in this test
	m.startNamePoolRefresher()
	select {
	case <-got:
	case <-time.After(10 * time.Second):
		t.Fatal("no fetch within 10s of starting a refresher with a one-hour period")
	}
	_ = m.Stop(context.Background())
}
