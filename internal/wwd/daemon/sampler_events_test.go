package daemon

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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

// With no owner rule in force the owner's reading is still in the facts (the monitoring page shows whether the owner is at the machine),
// and it is never counted: headroom is what it is without an owner, with the rule empty (#3682, spec §3.2). With the rule in force the
// same reading takes the headroom away.
func TestTheOwnerReadingIsReportedWithoutARuleAndOnlyCountedWithOne(t *testing.T) {
	level := 1
	active := &wire.OwnerState{Active: true, IdleS: 3, Reason: "hid"}

	noRule := NewSampler(SamplerOptions{HostID: "h1", Alias: "air26", Sources: fakeSources(&level, active), Slots: wire.Slots{Heavy: 1, Light: 2}, WorkerEnabled: true})
	noRule.Tick(s0)
	f, _ := noRule.Latest()
	require.NotNil(t, f.Owner, "the reading is reported with no rule")
	assert.True(t, f.Owner.Active)
	assert.Equal(t, "hid", f.Owner.Reason)
	assert.Equal(t, wire.Slots{Heavy: 1, Light: 2}, f.Derived.Headroom, "and not counted")

	withRule := NewSampler(SamplerOptions{HostID: "h1", Alias: "air26", Sources: fakeSources(&level, active), Slots: wire.Slots{Heavy: 1, Light: 2}, WorkerEnabled: true, OwnerRule: "hid<5m"})
	withRule.Tick(s0)
	f, _ = withRule.Latest()
	require.NotNil(t, f.Owner)
	assert.Equal(t, wire.Slots{}, f.Derived.Headroom, "with the rule the same reading takes the headroom away")
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
