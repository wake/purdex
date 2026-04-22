package agent_test

import (
	"testing"

	"github.com/wake/purdex/internal/agent"
)

// supporterStub implements agent.AgentProvider and agent.StatusSupporter.
type supporterStub struct {
	fakeProvider
	supported []agent.Status
}

func (s *supporterStub) SupportedStatuses() []agent.Status { return s.supported }

func TestCoverageEmpty(t *testing.T) {
	r := agent.NewRegistry()
	rows := agent.Coverage(r)
	if rows != nil {
		t.Fatalf("expected nil for empty registry, got %#v", rows)
	}
	if len(rows) != 0 {
		t.Fatalf("expected len==0, got %d", len(rows))
	}
}

func TestCoverageNoStatusSupporter(t *testing.T) {
	r := agent.NewRegistry()
	r.Register(&fakeProvider{agentType: "cc"})

	rows := agent.Coverage(r)
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	row := rows[0]
	if row.AgentType != "cc" {
		t.Fatalf("expected AgentType=cc, got %q", row.AgentType)
	}
	if row.Declares {
		t.Fatalf("expected Declares=false, got true")
	}
	if row.Declared != nil {
		t.Fatalf("expected Declared=nil, got %#v", row.Declared)
	}
}

func TestCoverageWithStatusSupporter(t *testing.T) {
	r := agent.NewRegistry()
	r.Register(&supporterStub{
		fakeProvider: fakeProvider{agentType: "cc"},
		supported:    []agent.Status{agent.StatusRunning, agent.StatusIdle},
	})

	rows := agent.Coverage(r)
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	row := rows[0]
	if row.AgentType != "cc" {
		t.Fatalf("expected AgentType=cc, got %q", row.AgentType)
	}
	if !row.Declares {
		t.Fatalf("expected Declares=true, got false")
	}
	if len(row.Declared) != 2 {
		t.Fatalf("expected 2 statuses, got %d", len(row.Declared))
	}
	if row.Declared[0] != agent.StatusRunning || row.Declared[1] != agent.StatusIdle {
		t.Fatalf("expected [Running, Idle], got %#v", row.Declared)
	}
}

func TestCoverageDeclaredIsCopy(t *testing.T) {
	internal := []agent.Status{agent.StatusRunning, agent.StatusIdle}
	stub := &supporterStub{
		fakeProvider: fakeProvider{agentType: "cc"},
		supported:    internal,
	}
	r := agent.NewRegistry()
	r.Register(stub)

	rows := agent.Coverage(r)
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	// Mutate the returned slice; provider's internal slice must not change.
	rows[0].Declared[0] = agent.StatusError

	if stub.supported[0] != agent.StatusRunning {
		t.Fatalf("provider's internal slice was mutated: %#v", stub.supported)
	}
}

func TestCoverageEmptyDeclaration(t *testing.T) {
	r := agent.NewRegistry()
	r.Register(&supporterStub{
		fakeProvider: fakeProvider{agentType: "cc"},
		supported:    nil, // provider returns nil
	})

	rows := agent.Coverage(r)
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	row := rows[0]
	if !row.Declares {
		t.Fatalf("expected Declares=true, got false")
	}
	if row.Declared == nil {
		t.Fatalf("expected Declared to be non-nil empty slice, got nil")
	}
	if len(row.Declared) != 0 {
		t.Fatalf("expected empty Declared, got %#v", row.Declared)
	}
}

func TestCoverageSortedByAgentType(t *testing.T) {
	r := agent.NewRegistry()
	// Register in reverse alphabetical order to ensure sort is exercised.
	r.Register(&fakeProvider{agentType: "zed"})
	r.Register(&fakeProvider{agentType: "abc"})
	r.Register(&fakeProvider{agentType: "mid"})

	rows := agent.Coverage(r)
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}
	want := []string{"abc", "mid", "zed"}
	for i, w := range want {
		if rows[i].AgentType != w {
			t.Fatalf("row %d: expected AgentType=%q, got %q", i, w, rows[i].AgentType)
		}
	}
}

func TestCoverageMixed(t *testing.T) {
	r := agent.NewRegistry()
	r.Register(&supporterStub{
		fakeProvider: fakeProvider{agentType: "cc"},
		supported:    []agent.Status{agent.StatusRunning},
	})
	r.Register(&fakeProvider{agentType: "codex"})

	rows := agent.Coverage(r)
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	// Sorted: cc < codex
	if rows[0].AgentType != "cc" || rows[1].AgentType != "codex" {
		t.Fatalf("expected order [cc, codex], got [%q, %q]", rows[0].AgentType, rows[1].AgentType)
	}
	if !rows[0].Declares {
		t.Fatalf("expected cc Declares=true")
	}
	if rows[1].Declares {
		t.Fatalf("expected codex Declares=false")
	}
	if rows[1].Declared != nil {
		t.Fatalf("expected codex Declared=nil, got %#v", rows[1].Declared)
	}
}
