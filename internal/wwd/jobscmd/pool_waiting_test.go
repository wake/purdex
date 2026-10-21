package jobscmd

import (
	"strings"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

// #3941: a host whose worker has queued jobs waiting for it gets one line under the table, for the watcher: how many, how long the oldest has
// waited, and whether the fast lane is giving way to it.
func TestWWDPoolTellsWhatWaitsForAWorker(t *testing.T) {
	waiting := func(count int, age time.Duration, yielding bool) *wire.Waiting {
		return &wire.Waiting{Count: count, OldestID: "j_1", OldestSince: t0.Add(-age), ObservedAt: t0, Yielding: yielding}
	}
	host := func(alias string, w *wire.Waiting) wire.Peer {
		return wire.Peer{Info: wire.Info{Alias: alias}, Reachable: true, Facts: &wire.Facts{At: t0, Worker: wire.FactsWorker{Enabled: true, Waiting: w}}}
	}
	f := &fakeAPI{pool: wire.Pool{Peers: []wire.Peer{
		host("air26", waiting(2, 7*time.Minute+20*time.Second, true)),
		host("mlab", nil),
		host("mserv", waiting(1, 40*time.Minute, false)),
		{Info: wire.Info{Alias: "air19"}, Reachable: false},
	}}}
	code, out, _ := runWWD(f, "pool")
	if code != 0 {
		t.Fatalf("code %d", code)
	}
	if !strings.Contains(out, "air26: 等候中 2 件（最久 7 分）快車道讓位中") {
		t.Errorf("a yielding host:\n%s", out)
	}
	if !strings.Contains(out, "mserv: 等候中 1 件（最久 40 分）") || strings.Contains(out, "mserv: 等候中 1 件（最久 40 分）快車道") {
		t.Errorf("a host whose fast lane does not give way:\n%s", out)
	}
	f.pool.Peers[2].Facts.Worker.Waiting.YieldOver = true
	_, out, _ = runWWD(f, "pool")
	if !strings.Contains(out, "mserv: 等候中 1 件（最久 40 分）已超過讓位上限，快車道不再讓") {
		t.Errorf("past the cap:\n%s", out)
	}
	if strings.Contains(out, "mlab:") || strings.Contains(out, "air19:") {
		t.Errorf("a host with nothing waiting has no line:\n%s", out)
	}
	if strings.Count(out, "等候中") != 2 {
		t.Errorf("one line each:\n%s", out)
	}
}
