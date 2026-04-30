package agent_test

import (
	"reflect"
	"testing"

	"github.com/wake/purdex/internal/agent"
)

// TestProbeIntent_StructFields verifies that the Signal struct exposes all
// four fields W6-3+W6-4 detectors need (Kind / PaneAlive / PaneID / SenderPID)
// with the documented types. The test uses reflect so an accidental field
// rename / removal surfaces here instead of as a downstream compile error
// in detector / dispatcher code.
func TestProbeIntent_StructFields(t *testing.T) {
	sig := agent.Signal{
		Kind:      agent.ProbeIntentKindProcessDead,
		PaneAlive: true,
		PaneID:    "%5",
		SenderPID: 12345,
	}

	cases := []struct {
		name     string
		wantKind reflect.Kind
	}{
		{"Kind", reflect.String},
		{"PaneAlive", reflect.Bool},
		{"PaneID", reflect.String},
		{"SenderPID", reflect.Int},
	}

	v := reflect.ValueOf(sig)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := v.FieldByName(tc.name)
			if !f.IsValid() {
				t.Fatalf("Signal is missing field %q", tc.name)
			}
			if f.Kind() != tc.wantKind {
				t.Fatalf("Signal.%s kind = %v, want %v", tc.name, f.Kind(), tc.wantKind)
			}
		})
	}

	// Verify ProbeIntent struct has the three documented fields with the
	// expected discriminator / status-set / mapper layout.
	intent := agent.ProbeIntent{
		Kind:          agent.ProbeIntentKindProcessDead,
		OnEntryStatus: []agent.Status{agent.StatusRunning, agent.StatusWaiting},
		OnSignal:      func(agent.Signal) agent.Status { return agent.StatusError },
	}
	if intent.Kind != agent.ProbeIntentKindProcessDead {
		t.Fatalf("ProbeIntent.Kind = %q, want %q", intent.Kind, agent.ProbeIntentKindProcessDead)
	}
	if len(intent.OnEntryStatus) != 2 {
		t.Fatalf("ProbeIntent.OnEntryStatus len = %d, want 2", len(intent.OnEntryStatus))
	}
	if intent.OnSignal == nil {
		t.Fatal("ProbeIntent.OnSignal must be assignable")
	}
	if got := intent.OnSignal(agent.Signal{Kind: agent.ProbeIntentKindProcessDead}); got != agent.StatusError {
		t.Fatalf("ProbeIntent.OnSignal() = %q, want %q", got, agent.StatusError)
	}
}

// TestProbeIntentKind_ProcessDead verifies the lone Kind constant value lands
// at the documented "process_dead" string. Detector / dispatcher / drift test
// rely on this exact value; the spec calls it out as a stable identifier.
func TestProbeIntentKind_ProcessDead(t *testing.T) {
	if got, want := string(agent.ProbeIntentKindProcessDead), "process_dead"; got != want {
		t.Fatalf("ProbeIntentKindProcessDead = %q, want %q", got, want)
	}
}

// TestProbeIntentProvider_OptionalNil asserts that a provider which does NOT
// implement ProbeIntentProvider still satisfies AgentProvider. This guards
// the optional-capability contract documented in §3.1: providers without
// ProbeIntents stay backward compatible with pre-W6-3 behavior.
func TestProbeIntentProvider_OptionalNil(t *testing.T) {
	var p agent.AgentProvider = &fakeProvider{agentType: "cc"}

	// fakeProvider does NOT implement ProbeIntentProvider, so the type
	// assertion must fail.
	if _, ok := p.(agent.ProbeIntentProvider); ok {
		t.Fatal("fakeProvider unexpectedly satisfies ProbeIntentProvider; optional capability contract broken")
	}
}

// TestProbeIntentProvider_PositiveImplementation verifies that a fake which
// DOES implement ProbeIntentProvider type-asserts successfully and returns the
// declared intents. Companion to TestProbeIntentProvider_OptionalNil.
func TestProbeIntentProvider_PositiveImplementation(t *testing.T) {
	intents := []agent.ProbeIntent{
		{
			Kind:          agent.ProbeIntentKindProcessDead,
			OnEntryStatus: []agent.Status{agent.StatusRunning},
			OnSignal:      func(agent.Signal) agent.Status { return agent.StatusError },
		},
	}
	var p agent.AgentProvider = &fakeProbeIntentProvider{
		fakeProvider: fakeProvider{agentType: "codex"},
		intents:      intents,
	}

	pp, ok := p.(agent.ProbeIntentProvider)
	if !ok {
		t.Fatal("fakeProbeIntentProvider does not satisfy ProbeIntentProvider")
	}
	got := pp.ProbeIntents()
	if len(got) != 1 {
		t.Fatalf("ProbeIntents len = %d, want 1", len(got))
	}
	if got[0].Kind != agent.ProbeIntentKindProcessDead {
		t.Fatalf("ProbeIntents[0].Kind = %q, want %q", got[0].Kind, agent.ProbeIntentKindProcessDead)
	}
}

// fakeProbeIntentProvider embeds fakeProvider and additionally implements
// ProbeIntentProvider, so test cases can verify the optional capability
// type-assertion path. Defined in this file to keep the test scope local.
type fakeProbeIntentProvider struct {
	fakeProvider
	intents []agent.ProbeIntent
}

func (f *fakeProbeIntentProvider) ProbeIntents() []agent.ProbeIntent {
	return f.intents
}
