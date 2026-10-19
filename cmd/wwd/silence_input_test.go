package main

import (
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/advisor"
	"github.com/wake/purdex/internal/wwd/daemon"
)

// The silence rules do not wait for a facts sample: their input needs no sampler, so a host whose sampler keeps failing still tells that a
// peer or the Purdex daemon is gone.
func TestSilenceInputNeedsNoFacts(t *testing.T) {
	since := time.Now().Add(-10 * time.Minute)
	seen := time.Now().Add(-10 * time.Minute)
	in, ok := silenceInput(advisorInputs{
		Downs:       func() []daemon.PeerDown { return []daemon.PeerDown{{Alias: "mserv", Since: since}} },
		PartnerSeen: func() time.Time { return seen },
	})()
	if !ok {
		t.Fatal("the input must be available without facts")
	}
	if v, err := (advisor.PeerSilence{Alias: "mserv"}).Eval(in); err != nil || !v.Holds {
		t.Fatalf("peer silence: %+v (%v)", v, err)
	}
	if v, err := (advisor.PartnerDown{}).Eval(in); err != nil || !v.Holds {
		t.Fatalf("partner: %+v (%v)", v, err)
	}
}
