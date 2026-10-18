package resourcesmod

import (
	"context"
	"testing"

	"github.com/wake/purdex/internal/execstat"
	"github.com/wake/purdex/internal/resources"
)

// A tick publishes the lease mode as a text gauge: the sampler's own fork cost depends on it, so a reading of the counters
// needs to know which mode it was taken in.
func TestTickPublishesModeGauge(t *testing.T) {
	execstat.ResetTags()
	t.Cleanup(execstat.ResetTags)
	f := newStoreFix(t)
	f.m.sampler = &fakeSampler{fn: func(context.Context, int) (resources.HostRaw, []resources.Proc, error) {
		return goodRaw(), nil, nil
	}}
	f.m.tick(context.Background())

	var got *execstat.Gauge
	for _, g := range execstat.Gauges() {
		if g.Name == "resources-mode" {
			g := g
			got = &g
		}
	}
	if got == nil {
		t.Fatal("no resources-mode gauge after a tick")
	}
	if got.Text != f.m.mode {
		t.Fatalf("resources-mode = %q, want the module's mode %q", got.Text, f.m.mode)
	}
}
