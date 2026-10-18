package daemon

import (
	"testing"

	"github.com/wake/purdex/internal/wwd/wire"
)

func TestSamplerPublishesEveryRecordToTheBroker(t *testing.T) {
	level := 1
	s := newSampler(&level, nil)
	d := New(Options{HostID: "h1", Alias: "mlab", Version: "v0", Token: testToken, Sampler: s})
	s.SetOnFacts(d.Events().PublishFacts)
	ch, cancel := d.Events().Subscribe(true)
	defer cancel()
	s.Tick(s0)
	e := recv(t, ch)
	if e.Type != wire.EventFacts {
		t.Fatalf("got %s", e.Type)
	}
	if _, err := wire.Decode[wire.Facts](e.Data); err != nil {
		t.Fatal(err)
	}
}

func TestFactsCarryTheOwnerRuleText(t *testing.T) {
	level := 1
	s := NewSampler(SamplerOptions{
		HostID: "h1", Alias: "air26", Sources: fakeSources(&level, nil), Slots: wire.Slots{Heavy: 1, Light: 2},
		WorkerEnabled: true, OwnerRule: "hid<5m",
	})
	s.Tick(s0)
	if f, _ := s.Latest(); f.Worker.OwnerRule != "hid<5m" {
		t.Fatalf("owner_rule %q", f.Worker.OwnerRule)
	}
	level = 1
	s2 := newSampler(&level, nil)
	s2.Tick(s0)
	if f, _ := s2.Latest(); f.Worker.OwnerRule != "" {
		t.Fatalf("owner_rule %q without a rule", f.Worker.OwnerRule)
	}
}
