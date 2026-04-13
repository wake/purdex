package probe_test

import (
	"testing"

	"github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/agent/probe"
)

type fakeReadinessChecker struct {
	status agent.Status
}

func (f *fakeReadinessChecker) CheckReadiness(string) probe.ReadinessResult {
	return probe.ReadinessResult{Status: f.status}
}

func TestCheckReadiness_Registered(t *testing.T) {
	p := probe.New(nil)
	p.RegisterReadiness("cc", &fakeReadinessChecker{status: agent.StatusIdle})

	result, ok := p.CheckReadiness("cc", "sess:")
	if !ok {
		t.Fatal("expected ok for registered checker")
	}
	if result.Status != agent.StatusIdle {
		t.Fatalf("expected idle, got %s", result.Status)
	}
}

func TestCheckReadiness_Unregistered(t *testing.T) {
	p := probe.New(nil)

	_, ok := p.CheckReadiness("unknown", "sess:")
	if ok {
		t.Fatal("expected not ok for unregistered checker")
	}
}
