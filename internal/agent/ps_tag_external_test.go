package agent_test

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/execstat"
)

// A caller outside the agent package that forks ps through agent.RunPS (the probe's process-table walk does) is the name in the tag:
// the real fork is counted under "agent.ps.<what>@<that function>", and not under the old single "agent.ps".
func TestRunPSIsCountedUnderWhatItAskedAndWhoAsked(t *testing.T) {
	before := tagTotals()
	if _, err := agent.RunPS(context.Background(), "-p", strconv.Itoa(os.Getpid()), "-o", "ppid="); err != nil {
		t.Skipf("ps is not available: %v", err)
	}
	after := tagTotals()
	var got []string
	for name, n := range after {
		if n > before[name] {
			got = append(got, name)
		}
	}
	if len(got) != 1 {
		t.Fatalf("one fork was counted under %d tags: %v", len(got), got)
	}
	if !strings.HasPrefix(got[0], "agent.ps.ppid@") || !strings.Contains(got[0], "TestRunPSIsCountedUnderWhatItAskedAndWhoAsked") {
		t.Fatalf("tag = %q, want agent.ps.ppid@agent_test.TestRunPS…", got[0])
	}
	if _, old := after["agent.ps"]; old {
		t.Errorf("the old single tag agent.ps is still counted")
	}
}

func tagTotals() map[string]int64 {
	out := map[string]int64{}
	for _, t := range execstat.Tags() {
		out[t.Name] = t.Total
	}
	return out
}
