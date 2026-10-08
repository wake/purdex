package agent

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/wake/purdex/internal/store"
)

// TestLegacyRowsReadCost measures the agent_events listing that sendSnapshot
// does inside the emit slot (U1-2b-3). Informational: it logs the time and
// fails only if a listing of 200 rows takes 100 ms, which would put the hold
// near the 250 ms log threshold.
func TestLegacyRowsReadCost(t *testing.T) {
	events, err := store.OpenAgentEvent(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer events.Close()
	for i := 0; i < 200; i++ {
		if err := events.Set(fmt.Sprintf("s%d", i), "PdxStop", json.RawMessage(`{}`), "cc", int64(i)); err != nil {
			t.Fatal(err)
		}
	}
	var worst time.Duration
	for i := 0; i < 20; i++ {
		t0 := time.Now()
		if _, err := events.ListAll(); err != nil {
			t.Fatal(err)
		}
		if d := time.Since(t0); d > worst {
			worst = d
		}
	}
	t.Logf("agent_events ListAll, 200 rows, worst of 20: %v", worst)
	if worst > 100*time.Millisecond {
		t.Fatalf("listing 200 legacy rows took %v", worst)
	}
}
