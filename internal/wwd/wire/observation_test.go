package wire

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func obs() Observation {
	end := time.Date(2026, 10, 18, 13, 5, 0, 0, time.UTC)
	return Observation{
		ID: "o_1760792400000_4242", Host: "mlab", Session: "_abc123", Via: ObsViaCodex,
		Command: "pnpm vitest run --maxWorkers=3", Signature: "pnpm vitest run",
		StartedAt: time.Date(2026, 10, 18, 13, 0, 0, 0, time.UTC), EndedAt: &end,
		CPUSeconds: 212.5, CPUPeak: 640, MemPeakMB: 1800, ProcsPeak: 11,
	}
}

func TestObservationRoundTripsAndValidates(t *testing.T) {
	o := obs()
	b, err := json.Marshal(o)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Decode[Observation](b)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != o.ID || got.Via != ObsViaCodex || got.EndedAt == nil || !got.EndedAt.Equal(*o.EndedAt) || got.CPUSeconds != 212.5 {
		t.Fatalf("%+v", got)
	}
	// a tree still running has a null ended_at, and says so
	o.EndedAt = nil
	b, _ = json.Marshal(o)
	if !strings.Contains(string(b), `"ended_at":null`) {
		t.Fatalf("a running tree has ended_at null: %s", b)
	}
	if _, err := Decode[Observation](b); err != nil {
		t.Fatal(err)
	}
}

func TestObservationValidate(t *testing.T) {
	for name, mut := range map[string]func(*Observation){
		"no id":         func(o *Observation) { o.ID = "" },
		"bad via":       func(o *Observation) { o.Via = "docker" },
		"no signature":  func(o *Observation) { o.Signature = "" },
		"negative cpu":  func(o *Observation) { o.CPUSeconds = -1 },
		"negative peak": func(o *Observation) { o.CPUPeak = -1 },
		"negative mem":  func(o *Observation) { o.MemPeakMB = -1 },
		"ended before it began": func(o *Observation) {
			e := o.StartedAt.Add(-time.Second)
			o.EndedAt = &e
		},
		"command too long": func(o *Observation) { o.Command = strings.Repeat("x", MaxObservationCommand+1) },
	} {
		o := obs()
		mut(&o)
		if err := o.Validate(); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	if err := (obs()).Validate(); err != nil {
		t.Fatal(err)
	}
	for _, via := range []string{ObsViaSession, ObsViaCodex} {
		o := obs()
		o.Via = via
		if err := o.Validate(); err != nil {
			t.Errorf("via %q: %v", via, err)
		}
	}
}
