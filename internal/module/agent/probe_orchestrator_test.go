package agent

import (
	"sync"
	"testing"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/agent/probe"
)

// recordingProber is a test fake that captures Watch/StopWatch invocations.
// Implements the orchestrator's internal proberWatcher interface.
type recordingProber struct {
	mu        sync.Mutex
	watchOpts map[string]probe.WatchOptions
	stops     []string
}

func newRecordingProber() *recordingProber {
	return &recordingProber{watchOpts: make(map[string]probe.WatchOptions)}
}

func (r *recordingProber) Watch(target string, opts probe.WatchOptions, _ probe.ScreenChangeCallback) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.watchOpts[target] = opts
}

func (r *recordingProber) StopWatch(target string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stops = append(r.stops, target)
}

// fakeProfileProvider is a fakeAgentProvider variant that ALSO implements
// agentpkg.ProbeProfileProvider. Defined here (not in fakes_test.go) to keep
// orchestrator tests self-contained — fakes_test.go's fakeAgentProvider has no
// ProbeProfile() method, which exercises the "default profile" path.
type fakeProfileProvider struct {
	fakeAgentProvider
	profile agentpkg.ProbeProfile
}

func (p *fakeProfileProvider) ProbeProfile() agentpkg.ProbeProfile { return p.profile }

// OR1 — startWatch resolves agent's ProbeProfile and forwards options.
func TestOrchestrator_StartWatchUsesAgentProfile(t *testing.T) {
	m := newTestModule(t)
	rec := newRecordingProber()
	m.probeOrch.watcher = rec

	provider := &fakeProfileProvider{
		fakeAgentProvider: fakeAgentProvider{typeName: "fake-agent"},
		profile:           agentpkg.ProbeProfile{TopLines: 5, IdleStableTicks: 2},
	}
	m.registry.Register(provider)

	m.probeOrch.startWatch("sess", "fake-agent")

	rec.mu.Lock()
	defer rec.mu.Unlock()
	got, ok := rec.watchOpts["sess:"]
	if !ok {
		t.Fatalf("expected Watch on target %q, got %v", "sess:", rec.watchOpts)
	}
	want := probe.WatchOptions{TopLines: 5, BottomLines: 0, IdleStableTicks: 2}
	if got != want {
		t.Fatalf("WatchOptions = %+v, want %+v", got, want)
	}
}

// OR2 — when the agent does NOT implement ProbeProfileProvider, startWatch
// falls back to defaultProbeProfile. Default preserves legacy
// CapturePaneContent(target, 10) capture region (R9 fix / G5 parity gate).
func TestOrchestrator_DefaultProfileWhenAgentMissing(t *testing.T) {
	m := newTestModule(t)
	rec := newRecordingProber()
	m.probeOrch.watcher = rec

	// fakeAgentProvider does NOT implement ProbeProfileProvider.
	provider := &fakeAgentProvider{typeName: "plain-agent"}
	m.registry.Register(provider)

	m.probeOrch.startWatch("sess", "plain-agent")

	rec.mu.Lock()
	defer rec.mu.Unlock()
	got, ok := rec.watchOpts["sess:"]
	if !ok {
		t.Fatalf("expected Watch on target %q, got %v", "sess:", rec.watchOpts)
	}
	want := probe.WatchOptions{TopLines: 0, BottomLines: 10, IdleStableTicks: 3}
	if got != want {
		t.Fatalf("WatchOptions = %+v, want %+v (default profile)", got, want)
	}
}
