package agent

import (
	"testing"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/module/session"
)

// TestEmitBaseline_ProbeNotRecordedWhenBroadcastFailed: the probe's frame
// goes nowhere when the session has no code, so it is no baseline either.
func TestEmitBaseline_ProbeNotRecordedWhenBroadcastFailed(t *testing.T) {
	r := newProbeModRig(t)
	r.m.sessions = &fakeSessionProvider{sessions: []session.SessionInfo{}}

	if !r.probe(agentpkg.StatusRunning) {
		t.Fatalf("setup: the probe was refused (drops %v)", r.drops)
	}
	if got := r.drain(t); len(got) != 0 {
		t.Fatalf("setup: emitted without a code: %+v", got)
	}
	if d, ok := lastEmittedDigest(r.m, "work"); ok {
		t.Fatalf("a probe frame that never went out became the baseline: %+v", d)
	}
}
