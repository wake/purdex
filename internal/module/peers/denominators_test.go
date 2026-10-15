package peers

import (
	"testing"

	"github.com/wake/purdex/internal/execstat"
)

// One local inventory is one peers-envelope event: the unit the probe cost of the peer list is divided by.
func TestLocalEnvelopeCountsDenominator(t *testing.T) {
	execstat.ResetTags()
	t.Cleanup(execstat.ResetTags)
	total := func() (n int64) {
		for _, s := range execstat.Events() {
			if s.Name == "peers-envelope" {
				n = s.Total
			}
		}
		return n
	}

	s := newSendEnv(t, envOpts{})
	s.local()
	if got := total(); got != 1 {
		t.Fatalf("after one inventory: peers-envelope = %d, want 1", got)
	}
	s.local()
	if got := total(); got != 2 {
		t.Fatalf("after two inventories: peers-envelope = %d, want 2", got)
	}
}
