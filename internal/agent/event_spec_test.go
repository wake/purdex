package agent

import "testing"

var lookupTestCatalog = []HookEventSpec{
	{
		Name:         "SessionStart",
		PurdexName:   "PdxSessionStart",
		UpstreamKeys: []string{"SessionStart"},
		Lifecycle:    LifecycleSessionStart,
	},
	{
		Name:         "PermissionRequest",
		PurdexName:   "PdxPermissionRequest",
		UpstreamKeys: []string{"permission.asked", "question.asked"},
		Lifecycle:    LifecycleNone,
	},
	{
		Name:         "Stop",
		PurdexName:   "PdxStop",
		UpstreamKeys: []string{"Stop"},
		Lifecycle:    LifecycleStop,
	},
}

func TestLookupByPurdexName_Found(t *testing.T) {
	spec, ok := LookupByPurdexName(lookupTestCatalog, "PdxStop")
	if !ok {
		t.Fatal("expected ok=true for PdxStop")
	}
	if spec.PurdexName != "PdxStop" {
		t.Errorf("got PurdexName=%q want PdxStop", spec.PurdexName)
	}
}

func TestLookupByPurdexName_NotFound(t *testing.T) {
	if _, ok := LookupByPurdexName(lookupTestCatalog, "PdxNotInCatalog"); ok {
		t.Error("expected ok=false for unknown PurdexName")
	}
}

func TestLookupByPurdexName_EmptyName(t *testing.T) {
	if _, ok := LookupByPurdexName(lookupTestCatalog, ""); ok {
		t.Error("empty PurdexName must not match any catalog entry")
	}
}

func TestLookupByUpstreamKey_FoundSingle(t *testing.T) {
	spec, ok := LookupByUpstreamKey(lookupTestCatalog, "SessionStart")
	if !ok {
		t.Fatal("expected ok=true for SessionStart upstream key")
	}
	if spec.PurdexName != "PdxSessionStart" {
		t.Errorf("got PurdexName=%q want PdxSessionStart", spec.PurdexName)
	}
}

func TestLookupByUpstreamKey_FoundMulti(t *testing.T) {
	spec, ok := LookupByUpstreamKey(lookupTestCatalog, "question.asked")
	if !ok {
		t.Fatal("expected ok=true for question.asked")
	}
	if spec.PurdexName != "PdxPermissionRequest" {
		t.Errorf("got PurdexName=%q want PdxPermissionRequest", spec.PurdexName)
	}
}

func TestLookupByUpstreamKey_NotFound(t *testing.T) {
	if _, ok := LookupByUpstreamKey(lookupTestCatalog, "NotAnUpstreamKey"); ok {
		t.Error("expected ok=false for unknown upstream key")
	}
}

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
