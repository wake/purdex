package agent

import "testing"

// Pre-W2 catalog entries do not yet populate PurdexName / UpstreamKeys /
// Lifecycle. New fields must default to zero values without disturbing the
// legacy Name field, so existing cc / codex / opencode literals compile and
// behave unchanged until Phase 1 cc + Phase 2 codex + Phase 3 opencode
// migrations land.
func TestHookEventSpec_NewFieldsZeroValueBackwardCompat(t *testing.T) {
	spec := HookEventSpec{
		Name:        "SessionStart",
		EmitsStatus: []Status{StatusRunning},
		Description: "test",
		FutureOnly:  false,
		Handling:    HookHandlingStatus,
	}

	if spec.Name != "SessionStart" {
		t.Errorf("Name lost: got %q want %q", spec.Name, "SessionStart")
	}
	if spec.PurdexName != "" {
		t.Errorf("PurdexName zero value should be empty string; got %q", spec.PurdexName)
	}
	if spec.UpstreamKeys != nil {
		t.Errorf("UpstreamKeys zero value should be nil; got %#v", spec.UpstreamKeys)
	}
	if spec.Lifecycle != LifecycleNone {
		t.Errorf("Lifecycle zero value should be LifecycleNone; got %v", spec.Lifecycle)
	}
}
