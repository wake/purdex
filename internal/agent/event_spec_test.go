package agent

import "testing"

var lookupTestCatalog = []HookEventSpec{
	{
		PurdexName:   "PdxSessionStart",
		UpstreamKeys: []string{"SessionStart"},
		Lifecycle:    LifecycleSessionStart,
	},
	{
		PurdexName:   "PdxPermissionRequest",
		UpstreamKeys: []string{"permission.asked", "question.asked"},
		Lifecycle:    LifecycleNone,
	},
	{
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

// Post-W2 (P3-T4 ship) HookEventSpec exposes PurdexName / UpstreamKeys /
// Lifecycle as the catalog identifiers. The pre-W2 Name field has been
// removed. This test pins the zero-value behavior of every remaining field so
// catalog rows that only populate the canonical W2 fields keep compiling and
// behaving as expected.
func TestHookEventSpec_ZeroValueDefaults(t *testing.T) {
	spec := HookEventSpec{
		PurdexName:   "PdxSessionStart",
		UpstreamKeys: []string{"SessionStart"},
		Lifecycle:    LifecycleSessionStart,
		EmitsStatus:  []Status{StatusRunning},
		Description:  "test",
		FutureOnly:   false,
		Handling:     HookHandlingStatus,
	}

	if spec.PurdexName != "PdxSessionStart" {
		t.Errorf("PurdexName lost: got %q want PdxSessionStart", spec.PurdexName)
	}
	if len(spec.UpstreamKeys) != 1 || spec.UpstreamKeys[0] != "SessionStart" {
		t.Errorf("UpstreamKeys lost: got %#v want [SessionStart]", spec.UpstreamKeys)
	}
	if spec.Lifecycle != LifecycleSessionStart {
		t.Errorf("Lifecycle lost: got %v want LifecycleSessionStart", spec.Lifecycle)
	}

	// Zero-value rows: every newly added defaulted field must collapse to its
	// safe default so tests that only assert one or two fields (e.g. synthetic
	// fixtures in cc/codex hooks tests) keep passing as the catalog evolves.
	zero := HookEventSpec{}
	if zero.PurdexName != "" {
		t.Errorf("PurdexName zero value = %q; want empty", zero.PurdexName)
	}
	if zero.UpstreamKeys != nil {
		t.Errorf("UpstreamKeys zero value = %#v; want nil", zero.UpstreamKeys)
	}
	if zero.Lifecycle != LifecycleNone {
		t.Errorf("Lifecycle zero value = %v; want LifecycleNone", zero.Lifecycle)
	}
	if zero.EmitsStatus != nil {
		t.Errorf("EmitsStatus zero value = %#v; want nil", zero.EmitsStatus)
	}
	if zero.Handling != "" {
		t.Errorf("Handling zero value = %q; want empty", zero.Handling)
	}
	if zero.FutureOnly {
		t.Error("FutureOnly zero value = true; want false")
	}
}
