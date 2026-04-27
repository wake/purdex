package opencode_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/agent/opencode"
)

// expectedOpenCodeInstallableNames lists the 8 installable Purdex Names. These
// are the catalog entries the plugin template's emit('Name', ...) calls bind
// to. Non-installable entries (ignored/unsupported) use upstream-key naming
// per plan v1.3 §2.2 bipartite policy and are validated separately by HC5.
var expectedOpenCodeInstallableNames = []string{
	"SessionStart",
	"UserPromptSubmit",
	"SubagentStart",
	"SubagentStop",
	"PermissionRequest",
	"Stop",
	"StopFailure",
	"SessionEnd",
}

// installableNameSet exposes the installable name set as a map for collision
// checks (HC5b) and other tests.
func installableNameSet() map[string]bool {
	out := make(map[string]bool, len(expectedOpenCodeInstallableNames))
	for _, n := range expectedOpenCodeInstallableNames {
		out[n] = true
	}
	return out
}

// TestOpenCodeEvents_TotalCatalogCount locks the catalog at 65 entries
// (8 installable + 20 unsupported + 37 ignored) per audit §4.6.
func TestOpenCodeEvents_TotalCatalogCount(t *testing.T) {
	p := opencode.NewProvider()
	events := p.Events()
	if len(events) != 65 {
		t.Fatalf("opencode Events count = %d, want 65 (audit §4.6: 8+20+37)", len(events))
	}
}

// TestOpenCodeEvents_InstallableNamesMatchExpected asserts the installable
// Purdex Name set matches the 8 expected names (plan §1.4 / audit §4.1).
func TestOpenCodeEvents_InstallableNamesMatchExpected(t *testing.T) {
	p := opencode.NewProvider()

	got := make(map[string]bool)
	for _, e := range p.Events() {
		if !agent.IsInstallableHookSpec(e) {
			continue
		}
		if got[e.Name] {
			t.Errorf("opencode installable contains duplicate Name %q", e.Name)
		}
		got[e.Name] = true
	}
	want := installableNameSet()
	for n := range want {
		if !got[n] {
			t.Errorf("opencode installable missing required Name %q", n)
		}
	}
	for n := range got {
		if !want[n] {
			t.Errorf("opencode installable contains unexpected Name %q", n)
		}
	}
}

// TestOpenCodeEvents_DetailOnlyHaveEmptyEmitsStatus guards the empty-slice
// convention for detail-only SubagentStart/Stop installable entries.
func TestOpenCodeEvents_DetailOnlyHaveEmptyEmitsStatus(t *testing.T) {
	p := opencode.NewProvider()
	for _, e := range p.Events() {
		if e.Name != "SubagentStart" && e.Name != "SubagentStop" {
			continue
		}
		if e.EmitsStatus == nil {
			t.Errorf("opencode %s EmitsStatus is nil; want non-nil empty slice", e.Name)
		}
		if len(e.EmitsStatus) != 0 {
			t.Errorf("opencode %s EmitsStatus = %v, want empty", e.Name, e.EmitsStatus)
		}
	}
}

// TestOpenCodeEvents_DescriptionsNonEmpty asserts every Description is
// non-empty, ≤70 chars (plan §2.2), no trailing period, and emoji-free.
func TestOpenCodeEvents_DescriptionsNonEmpty(t *testing.T) {
	p := opencode.NewProvider()
	for _, e := range p.Events() {
		if strings.TrimSpace(e.Description) == "" {
			t.Errorf("opencode %s Description is empty", e.Name)
			continue
		}
		if len(e.Description) > 70 {
			t.Errorf("opencode %s Description %d chars > 70 (plan §2.2): %q", e.Name, len(e.Description), e.Description)
		}
		if strings.HasSuffix(strings.TrimSpace(e.Description), ".") {
			t.Errorf("opencode %s Description ends with period (plan §2.2): %q", e.Name, e.Description)
		}
		for _, r := range e.Description {
			if r >= 0x1F300 && r <= 0x1FAFF {
				t.Errorf("opencode %s Description contains emoji rune %U: %q", e.Name, r, e.Description)
				break
			}
		}
	}
}

// TestOpenCodeEvents_FreshSliceDefensiveCopy guards the defensive-copy
// convention.
func TestOpenCodeEvents_FreshSliceDefensiveCopy(t *testing.T) {
	p := opencode.NewProvider()
	first := p.Events()
	if len(first) == 0 {
		t.Fatal("opencode Events returned empty slice")
	}
	first[0].Name = "__mutated__"
	if len(first[0].EmitsStatus) > 0 {
		first[0].EmitsStatus[0] = agent.Status("__mutated__")
	}
	second := p.Events()
	if second[0].Name == "__mutated__" {
		t.Errorf("opencode Events shares backing array; second call first Name mutated to %q", second[0].Name)
	}
	if len(second[0].EmitsStatus) > 0 && second[0].EmitsStatus[0] == "__mutated__" {
		t.Errorf("opencode Events shares EmitsStatus backing array across calls")
	}
}

// --- HC5 series: catalog ↔ frozen 1.14.23 manifest bidirectional checks ---

// frozenManifest is the JSON shape from testdata/opencode-1.14.23-manifest.json.
type frozenManifest struct {
	Tag             string `json:"tag"`
	CommitSha       string `json:"commitSha"`
	CatalogSummary  struct {
		BusEvents    int `json:"busEvents"`
		StrongHooks  int `json:"strongHooks"`
		Installable  int `json:"installable"`
		Ignored      int `json:"ignored"`
		Unsupported  int `json:"unsupported"`
	} `json:"catalogSummary"`
}

// frozenEvents is the JSON shape from testdata/opencode-1.14.23-events.json.
type frozenEvents struct {
	Version     string             `json:"version"`
	Tag         string             `json:"tag"`
	BusEvents   []frozenEventEntry `json:"busEvents"`
	StrongHooks []frozenEventEntry `json:"strongHooks"`
}

type frozenEventEntry struct {
	UpstreamKey string `json:"upstreamKey"`
	Purdex      struct {
		Kind               string                 `json:"kind"`
		PurdexEventName    string                 `json:"purdexEventName,omitempty"`
		Reason             string                 `json:"reason,omitempty"`
		TemplateConsumesAt string                 `json:"templateConsumesAt,omitempty"`
		StalenessPolicy    *frozenStalenessPolicy `json:"stalenessPolicy,omitempty"`
	} `json:"purdex"`
}

// frozenStalenessPolicy mirrors the purdex.stalenessPolicy block in
// testdata/opencode-1.14.23-events.json. Pointer indirection on the parent
// struct lets absence ↔ nil so HC5e can iterate only the policied entries.
type frozenStalenessPolicy struct {
	State                   string `json:"state,omitempty"`
	Decision                string `json:"decision"`
	SwitchTarget            string `json:"switchTarget,omitempty"`
	SwitchTargetUpstreamKey string `json:"switchTargetUpstreamKey,omitempty"`
	Rationale               string `json:"rationale,omitempty"`
	DedupRequired           bool   `json:"dedupRequired,omitempty"`
}

func loadFrozenManifest(t *testing.T) frozenManifest {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "opencode-1.14.23-manifest.json"))
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	var m frozenManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	return m
}

func loadFrozenEvents(t *testing.T) frozenEvents {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "opencode-1.14.23-events.json"))
	if err != nil {
		t.Fatalf("load events.json: %v", err)
	}
	var e frozenEvents
	if err := json.Unmarshal(raw, &e); err != nil {
		t.Fatalf("parse events.json: %v", err)
	}
	return e
}

// catalogIndex returns a Name -> spec map for opencodeEventSpecs.
func catalogIndex(specs []agent.HookEventSpec) map[string]agent.HookEventSpec {
	out := make(map[string]agent.HookEventSpec, len(specs))
	for _, spec := range specs {
		out[spec.Name] = spec
	}
	return out
}

// HC5: bidirectional exact set assertion against frozen 1.14.23 manifest
// (plan v1.3 §3 H3-1).
//
// Rules:
//  (a) For each events.json entry e:
//      - kind == "installable": purdexEventName must exact-match an
//        opencodeEventSpecs entry whose Handling resolves to status/detail.
//      - kind == "ignored" or "unsupported": upstreamKey must exact-match
//        an opencodeEventSpecs entry whose Handling matches.
//      - kind == "core-mechanism" (event hook): no events.go counterpart
//        expected (skip — it's the messaging mechanism itself).
//  (b) For each opencodeEventSpecs entry g:
//      - If installable: there must exist >=1 events.json entry mapping to
//        it via purdexEventName.
//      - Otherwise: there must exist exactly 1 events.json entry whose
//        upstreamKey == g.Name and whose kind matches g.Handling.
//  (c) No events.go-only entries (every events.go entry must have a
//      manifest counterpart).
func TestOpenCodeEvents_ClassifyAgainstFrozenManifest(t *testing.T) {
	p := opencode.NewProvider()
	specs := p.Events()
	specsByName := catalogIndex(specs)
	frozen := loadFrozenEvents(t)

	// Track which events.go entries get matched by events.json (for rule b/c).
	matchedSpecs := make(map[string]int, len(specs))

	check := func(prefix string, entries []frozenEventEntry) {
		for _, e := range entries {
			if e.UpstreamKey == "" {
				t.Errorf("%s entry has empty upstreamKey", prefix)
				continue
			}
			switch e.Purdex.Kind {
			case "installable":
				if e.Purdex.PurdexEventName == "" {
					t.Errorf("%s[%s] kind=installable but purdexEventName is empty", prefix, e.UpstreamKey)
					continue
				}
				spec, ok := specsByName[e.Purdex.PurdexEventName]
				if !ok {
					t.Errorf("%s[%s] purdexEventName=%q has no opencodeEventSpecs entry", prefix, e.UpstreamKey, e.Purdex.PurdexEventName)
					continue
				}
				h := agent.EffectiveHookHandling(spec)
				if h != agent.HookHandlingStatus && h != agent.HookHandlingDetail {
					t.Errorf("%s[%s] purdexEventName=%q resolves to handling=%q; want status/detail", prefix, e.UpstreamKey, e.Purdex.PurdexEventName, h)
				}
				matchedSpecs[e.Purdex.PurdexEventName]++
			case "ignored":
				spec, ok := specsByName[e.UpstreamKey]
				if !ok {
					t.Errorf("%s[%s] kind=ignored but no opencodeEventSpecs entry with Name=%q", prefix, e.UpstreamKey, e.UpstreamKey)
					continue
				}
				if agent.EffectiveHookHandling(spec) != agent.HookHandlingIgnored {
					t.Errorf("%s[%s] kind=ignored but opencodeEventSpecs[%q].Handling=%q", prefix, e.UpstreamKey, e.UpstreamKey, agent.EffectiveHookHandling(spec))
				}
				matchedSpecs[e.UpstreamKey]++
			case "unsupported":
				spec, ok := specsByName[e.UpstreamKey]
				if !ok {
					t.Errorf("%s[%s] kind=unsupported but no opencodeEventSpecs entry with Name=%q", prefix, e.UpstreamKey, e.UpstreamKey)
					continue
				}
				if agent.EffectiveHookHandling(spec) != agent.HookHandlingUnsupported {
					t.Errorf("%s[%s] kind=unsupported but opencodeEventSpecs[%q].Handling=%q", prefix, e.UpstreamKey, e.UpstreamKey, agent.EffectiveHookHandling(spec))
				}
				matchedSpecs[e.UpstreamKey]++
			case "core-mechanism":
				// Event hook (Bus fan-out) — no events.go counterpart by design.
			default:
				t.Errorf("%s[%s] unknown kind=%q (want installable/ignored/unsupported/core-mechanism)", prefix, e.UpstreamKey, e.Purdex.Kind)
			}
		}
	}

	check("busEvents", frozen.BusEvents)
	check("strongHooks", frozen.StrongHooks)

	// Rule (b)/(c): every events.go entry must have at least one matching
	// events.json entry (no events.go-only entries).
	for _, spec := range specs {
		if matchedSpecs[spec.Name] == 0 {
			t.Errorf("opencodeEventSpecs[%q] has no matching events.json entry (events.go-only forbidden by HC5 rule c)", spec.Name)
		}
	}
}

// HC5b: collision check + non-installable explicit Handling.
// Plan v1.3 §3 + §2.2:
//   (a) ignored/unsupported entries set Handling explicitly (not blank);
//   (b) ignored/unsupported entry Names must NOT collide with the 8
//       installable Purdex Names (bipartite naming policy).
func TestOpenCodeEvents_NonInstallableHaveExplicitHandlingAndNoNameCollision(t *testing.T) {
	p := opencode.NewProvider()
	specs := p.Events()
	installable := installableNameSet()
	for _, spec := range specs {
		if agent.IsInstallableHookSpec(spec) {
			continue
		}
		if spec.Handling == "" {
			t.Errorf("opencode non-installable %q has blank Handling; expected explicit ignored/unsupported", spec.Name)
		}
		if installable[spec.Name] {
			t.Errorf("opencode non-installable Name %q collides with installable Purdex Name set", spec.Name)
		}
	}
}

// HC5c: non-installable entries have non-nil empty EmitsStatus slice
// (no status emission).
func TestOpenCodeEvents_NonInstallableHaveEmptyEmitsStatus(t *testing.T) {
	p := opencode.NewProvider()
	for _, spec := range p.Events() {
		if agent.IsInstallableHookSpec(spec) {
			continue
		}
		if spec.EmitsStatus == nil {
			t.Errorf("opencode non-installable %q EmitsStatus is nil; want non-nil empty slice", spec.Name)
		}
		if len(spec.EmitsStatus) != 0 {
			t.Errorf("opencode non-installable %q EmitsStatus=%v; want empty", spec.Name, spec.EmitsStatus)
		}
	}
}

// HC5d: manifest catalogSummary counts match events.json + opencodeEventSpecs.
// Plan v1.3 §3 (Round 2 M2):
//   (a) manifest.catalogSummary.busEvents == events.json.busEvents.length
//   (b) manifest.catalogSummary.strongHooks == events.json.strongHooks.length
//   (c) installable / ignored / unsupported counts match opencodeEventSpecs.
func TestOpenCodeManifestCatalogSummaryMatchesEvents(t *testing.T) {
	p := opencode.NewProvider()
	specs := p.Events()
	manifest := loadFrozenManifest(t)
	frozen := loadFrozenEvents(t)

	if got, want := manifest.CatalogSummary.BusEvents, len(frozen.BusEvents); got != want {
		t.Errorf("manifest.catalogSummary.busEvents=%d, events.json.busEvents.length=%d (HC5d a)", got, want)
	}
	if got, want := manifest.CatalogSummary.StrongHooks, len(frozen.StrongHooks); got != want {
		t.Errorf("manifest.catalogSummary.strongHooks=%d, events.json.strongHooks.length=%d (HC5d b)", got, want)
	}

	var installable, ignored, unsupported int
	for _, spec := range specs {
		switch agent.EffectiveHookHandling(spec) {
		case agent.HookHandlingStatus, agent.HookHandlingDetail:
			installable++
		case agent.HookHandlingIgnored:
			ignored++
		case agent.HookHandlingUnsupported:
			unsupported++
		}
	}
	if got, want := manifest.CatalogSummary.Installable, installable; got != want {
		t.Errorf("manifest.catalogSummary.installable=%d, opencodeEventSpecs installable count=%d (HC5d c)", got, want)
	}
	if got, want := manifest.CatalogSummary.Ignored, ignored; got != want {
		t.Errorf("manifest.catalogSummary.ignored=%d, opencodeEventSpecs ignored count=%d (HC5d c)", got, want)
	}
	if got, want := manifest.CatalogSummary.Unsupported, unsupported; got != want {
		t.Errorf("manifest.catalogSummary.unsupported=%d, opencodeEventSpecs unsupported count=%d (HC5d c)", got, want)
	}
}

// HC5e: partial-stale policy contract enforcement.
// Plan v1.3 §6 H6.4 mandates that for every events.json entry whose
// purdex.stalenessPolicy is non-null, the policy block must satisfy:
//   (a) decision ∈ {"retain", "switch", "dualSubscribe"}.
//   (d) decision == "switch" → switchTarget non-empty AND the entry resolved
//       via switchTargetUpstreamKey has templateConsumesAt non-empty
//       (i.e. switching to a target the plugin actually consumes).
//   (e) decision == "dualSubscribe" → dedupRequired == true.
// Rules (b) and (c) — audit doc rationale section presence and §7.3 retain
// residual-risk note — remain manual reviewer responsibilities and are out of
// automation scope.
//
// Codex adversarial review (review-mognq5p2-icfowm) flagged H6.4 as
// documented but not mechanically enforced; HC5e closes that gap.
func TestOpenCodeEvents_StalenessPolicyContract(t *testing.T) {
	frozen := loadFrozenEvents(t)

	// Build a (upstreamKey -> templateConsumesAt) index so we can resolve
	// switch targets without iterating twice.
	templateConsumesByKey := make(map[string]string)
	collect := func(entries []frozenEventEntry) {
		for _, e := range entries {
			templateConsumesByKey[e.UpstreamKey] = e.Purdex.TemplateConsumesAt
		}
	}
	collect(frozen.BusEvents)
	collect(frozen.StrongHooks)

	allowedDecisions := map[string]bool{
		"retain":         true,
		"switch":         true,
		"dualSubscribe":  true,
	}

	check := func(prefix string, entries []frozenEventEntry) {
		for _, e := range entries {
			pol := e.Purdex.StalenessPolicy
			if pol == nil {
				continue
			}
			// (a) decision in allowed set.
			if !allowedDecisions[pol.Decision] {
				t.Errorf("%s[%s] stalenessPolicy.decision=%q; want one of retain/switch/dualSubscribe (H6.4 a)", prefix, e.UpstreamKey, pol.Decision)
				continue
			}
			switch pol.Decision {
			case "switch":
				// (d) switchTarget non-empty and resolved switchTargetUpstreamKey
				// entry has templateConsumesAt.
				if strings.TrimSpace(pol.SwitchTarget) == "" {
					t.Errorf("%s[%s] decision=switch but switchTarget is empty (H6.4 d)", prefix, e.UpstreamKey)
				}
				if strings.TrimSpace(pol.SwitchTargetUpstreamKey) == "" {
					t.Errorf("%s[%s] decision=switch but switchTargetUpstreamKey is empty; cannot resolve templateConsumesAt (H6.4 d)", prefix, e.UpstreamKey)
					continue
				}
				targetConsumes, ok := templateConsumesByKey[pol.SwitchTargetUpstreamKey]
				if !ok {
					t.Errorf("%s[%s] decision=switch switchTargetUpstreamKey=%q has no matching events.json entry (H6.4 d)", prefix, e.UpstreamKey, pol.SwitchTargetUpstreamKey)
					continue
				}
				if strings.TrimSpace(targetConsumes) == "" {
					t.Errorf("%s[%s] decision=switch switchTargetUpstreamKey=%q has empty templateConsumesAt; switch must point at an entry the plugin consumes (H6.4 d)", prefix, e.UpstreamKey, pol.SwitchTargetUpstreamKey)
				}
			case "dualSubscribe":
				// (e) dedupRequired must be true. (No catalog entry currently
				// uses dualSubscribe, so this branch is reserved for future
				// catalog growth.)
				if !pol.DedupRequired {
					t.Errorf("%s[%s] decision=dualSubscribe but dedupRequired=false; dual-subscribe must be paired with deduplication (H6.4 e)", prefix, e.UpstreamKey)
				}
			}
		}
	}

	check("busEvents", frozen.BusEvents)
	check("strongHooks", frozen.StrongHooks)
}
