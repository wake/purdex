package wire

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// facts.worker.waiting (#3941): the queued jobs this host's worker would take, told by the worker so that the fast lane can read them from
// the record and ask no other host. A host that has none, or an older writer, has no key.
func TestFactsWorkerWaiting_AbsentWhenNone(t *testing.T) {
	b, err := json.Marshal(FactsWorker{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "waiting") {
		t.Errorf("a worker with nothing waiting writes %s", b)
	}
}

func TestFactsWorkerWaiting_RoundTrips(t *testing.T) {
	at := time.Date(2026, 10, 21, 18, 0, 0, 0, time.UTC)
	in := FactsWorker{Enabled: true, Waiting: &Waiting{Count: 2, OldestID: "j_1", OldestOrigin: "mlab", OldestSince: at, ObservedAt: at.Add(time.Minute), Yielding: true}}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out FactsWorker
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out.Waiting == nil || *out.Waiting != *in.Waiting {
		t.Errorf("got %+v, want %+v", out.Waiting, in.Waiting)
	}
}

func TestFactsValidate_RefusesANegativeWaitingCount(t *testing.T) {
	f := Facts{V: FactsProto, Derived: Derived{Zone: ZoneNormal}}
	f.Worker.Waiting = &Waiting{Count: -1}
	if err := f.Validate(); err == nil || !strings.Contains(err.Error(), "waiting") {
		t.Errorf("err %v", err)
	}
	f.Worker.Waiting = &Waiting{Count: 1, OldestID: "j_1"}
	if err := f.Validate(); err != nil {
		t.Errorf("a valid one: %v", err)
	}
}
