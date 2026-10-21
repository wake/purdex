package pick

import (
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

// ---- a slower host leaves a job to a faster one that could take it right now (#3904) ----

// fastHost is a peer of the pool: it answers, its worker is on, it announces its speed and labels, and it has a heavy slot free.
func fastHost(alias string, speed float64, labels ...string) wire.Peer {
	if len(labels) == 0 {
		labels = []string{"go", "node"}
	}
	p := wire.Peer{Reachable: true, Info: wire.Info{Alias: alias, Labels: labels, Worker: wire.InfoWorker{Enabled: true, SpeedFactor: speed}}, Facts: &wire.Facts{}}
	p.Facts.Derived.Headroom = wire.Slots{Heavy: 1, Light: 1}
	return p
}

func waitingJob(age time.Duration) wire.Job {
	j := job("j", wire.ClassMerge, age)
	j.Labels = []string{"go"}
	return j
}

const patience = 2 * time.Minute

func TestFasterHostIsNamedWhenItCouldTakeTheJobNow(t *testing.T) {
	got, ok := FasterHost(waitingJob(time.Second), 3, []wire.Peer{fastHost("air26", 1)}, t0, patience)
	if !ok || got != "air26" {
		t.Fatalf("got %q %v: a host three times faster with a free slot should have it", got, ok)
	}
}

func TestFasterHostIsNotNamedWhen(t *testing.T) {
	full := fastHost("air26", 1)
	full.Facts.Derived.Headroom.Heavy = 0
	down := fastHost("air26", 1)
	down.Reachable = false
	workerOff := fastHost("air26", 1)
	workerOff.Info.Worker.Enabled = false
	noFacts := fastHost("air26", 1)
	noFacts.Facts = nil
	wrongLabels := fastHost("air26", 1, "linux")
	explicitOnly := fastHost("air26", 1)
	explicitOnly.Info.Worker.ExplicitLabelsOnly = true // the job names no os:, task: or host: label
	unknown := fastHost("air26", 0)                    // a wwd that predates the field: its speed is not known
	same := fastHost("air26", 3)
	slower := fastHost("air26", 4)

	cases := map[string]struct {
		peers []wire.Peer
		age   time.Duration
		self  float64
	}{
		"it has no heavy slot free":              {[]wire.Peer{full}, time.Second, 3},
		"it is unreachable":                      {[]wire.Peer{down}, time.Second, 3},
		"its worker is off":                      {[]wire.Peer{workerOff}, time.Second, 3},
		"its facts have not come":                {[]wire.Peer{noFacts}, time.Second, 3},
		"the labels are not its":                 {[]wire.Peer{wrongLabels}, time.Second, 3},
		"it takes only explicitly labelled":      {[]wire.Peer{explicitOnly}, time.Second, 3},
		"its speed is not known":                 {[]wire.Peer{unknown}, time.Second, 3},
		"it is as fast as this host":             {[]wire.Peer{same}, time.Second, 3},
		"it is slower than this host":            {[]wire.Peer{slower}, time.Second, 3},
		"the job has waited as long as patience": {[]wire.Peer{fastHost("air26", 1)}, patience, 3},
		"the job has waited longer":              {[]wire.Peer{fastHost("air26", 1)}, patience + time.Minute, 3},
		"there is no other host":                 {nil, time.Second, 3},
	}
	for name, c := range cases {
		if got, ok := FasterHost(waitingJob(c.age), c.self, c.peers, t0, patience); ok {
			t.Errorf("%s: left to %q", name, got)
		}
	}
}

func TestFasterHostNeedsPatienceAbove0(t *testing.T) {
	if _, ok := FasterHost(waitingJob(time.Second), 3, []wire.Peer{fastHost("air26", 1)}, t0, 0); ok {
		t.Error("patience 0 switches the whole thing off")
	}
}

// A slow host with no speed set counts as 1 (the reference), so a peer must announce less than 1 to be faster.
func TestFasterHostSelfSpeedUnsetIsOne(t *testing.T) {
	if _, ok := FasterHost(waitingJob(time.Second), 0, []wire.Peer{fastHost("air26", 1)}, t0, patience); ok {
		t.Error("equal speeds leave nothing to either")
	}
	if _, ok := FasterHost(waitingJob(time.Second), 0, []wire.Peer{fastHost("big", 0.5)}, t0, patience); !ok {
		t.Error("a peer at 0.5 is faster than a host at the default 1")
	}
}

func TestFasterHostAnyOneOfSeveralIsEnough(t *testing.T) {
	full := fastHost("a", 1)
	full.Facts.Derived.Headroom.Heavy = 0
	got, ok := FasterHost(waitingJob(time.Second), 3, []wire.Peer{full, fastHost("b", 2)}, t0, patience)
	if !ok || got != "b" {
		t.Errorf("got %q %v: the first host that could take it is named", got, ok)
	}
}

// A selective hold on the faster host leaves room only for the jobs it lists: the same reading the worker has (derived.HeavyRoomFor).
func TestFasterHostReadsTheSelectiveHoldsOfThePeer(t *testing.T) {
	p := fastHost("air26", 1)
	p.Facts.Derived.Headroom.Heavy = 0
	p.Facts.Derived.Zone = wire.ZoneNormal
	p.Facts.Worker.Enabled, p.Facts.Worker.Slots.Heavy = true, 1
	p.Facts.Exception = []wire.FactExc{{Name: "dev", Allow: &wire.HoldAllow{Classes: []wire.JobClass{wire.ClassRelease}}}}
	if _, ok := FasterHost(waitingJob(time.Second), 3, []wire.Peer{p}, t0, patience); ok {
		t.Error("a merge job is not one the hold lists")
	}
	rel := waitingJob(time.Second)
	rel.Class = wire.ClassRelease
	if _, ok := FasterHost(rel, 3, []wire.Peer{p}, t0, patience); !ok {
		t.Error("a release job is")
	}
}
