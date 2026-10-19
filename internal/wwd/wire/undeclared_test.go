package wire

import (
	"testing"
	"time"
)

var ut0 = time.Date(2026, 10, 18, 14, 0, 0, 0, time.UTC)

func mkObs(id, sig, session, via string, at time.Duration, cpu float64, mem int) Observation {
	end := ut0.Add(at + time.Minute)
	return Observation{ID: id, Host: "mlab", Session: session, Via: via, Command: sig + " --x", Signature: sig,
		StartedAt: ut0.Add(at), EndedAt: &end, CPUSeconds: cpu, MemPeakMB: mem, ProcsPeak: 2}
}

func TestGroupUndeclaredGroupsBySignature(t *testing.T) {
	got := GroupUndeclared([]Observation{
		mkObs("a", "pnpm vitest run", "_s1", ObsViaSession, 0, 100, 800),
		mkObs("b", "go test", "_s2", ObsViaCodex, time.Minute, 40, 1500),
		mkObs("c", "pnpm vitest run", "_s3", ObsViaCodex, 2*time.Minute, 250, 600),
	})
	if len(got) != 2 {
		t.Fatalf("two signatures: %+v", got)
	}
	g := got[0] // the biggest total CPU first
	if g.Signature != "pnpm vitest run" || g.Count != 2 || g.CPUSeconds != 350 || g.MaxMemMB != 800 {
		t.Fatalf("totals: %+v", g)
	}
	// the latest one says whose it was and how it ran
	if g.LastSession != "_s3" || g.LastVia != ObsViaCodex || !g.LastAt.Equal(ut0.Add(2*time.Minute)) {
		t.Fatalf("last: %+v", g)
	}
	if got[1].Signature != "go test" || got[1].Count != 1 || got[1].MaxMemMB != 1500 {
		t.Fatalf("second: %+v", got[1])
	}
}

func TestGroupUndeclaredOrderIsStable(t *testing.T) {
	// equal CPU: the larger memory peak first, then the signature; the same input in another order gives the same answer
	in := []Observation{
		mkObs("a", "b-cmd", "_s", ObsViaSession, 0, 10, 100),
		mkObs("b", "a-cmd", "_s", ObsViaSession, 0, 10, 100),
		mkObs("c", "c-cmd", "_s", ObsViaSession, 0, 10, 900),
	}
	rev := []Observation{in[2], in[1], in[0]}
	a, b := GroupUndeclared(in), GroupUndeclared(rev)
	want := []string{"c-cmd", "a-cmd", "b-cmd"}
	for i, w := range want {
		if a[i].Signature != w || b[i].Signature != w {
			t.Fatalf("order %v / %v, want %v", a, b, want)
		}
	}
}

func TestGroupUndeclaredLatestTieBreaksOnID(t *testing.T) {
	got := GroupUndeclared([]Observation{
		mkObs("o_2", "make", "_second", ObsViaSession, 0, 1, 1),
		mkObs("o_1", "make", "_first", ObsViaSession, 0, 1, 1),
	})
	if len(got) != 1 || got[0].LastSession != "_second" {
		t.Fatalf("the greater id is the later one: %+v", got)
	}
}

func TestGroupUndeclaredEmptyIsAnEmptyList(t *testing.T) {
	if got := GroupUndeclared(nil); got == nil || len(got) != 0 {
		t.Fatalf("an empty list (JSON []), not nil: %#v", got)
	}
}

func TestUndeclaredResponseValidate(t *testing.T) {
	r := UndeclaredResponse{Host: "mlab", Since: ut0, Groups: GroupUndeclared([]Observation{mkObs("a", "make", "_s", ObsViaSession, 0, 1, 1)})}
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	r.Groups[0].Count = 0
	if r.Validate() == nil {
		t.Fatal("a group of nothing is refused")
	}
	if (UndeclaredResponse{Host: ""}).Validate() == nil {
		t.Fatal("a response names its host")
	}
}

func TestObservationsResponseValidate(t *testing.T) {
	ok := ObservationsResponse{Host: "mlab", Since: ut0, Observations: []Observation{mkObs("a", "make", "_s", ObsViaSession, 0, 1, 1)}}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	bad := ok
	bad.Observations = []Observation{{ID: "x"}}
	if bad.Validate() == nil {
		t.Fatal("an invalid observation is refused")
	}
}
