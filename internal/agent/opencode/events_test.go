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
// are the catalog entries the plugin template's emit('PdxXxx', ...) calls bind
// to. Non-installable entries (ignored/unsupported) use upstream-key naming
// (still keyed off UpstreamKeys[0]) per plan v1.3 §2.2 bipartite policy and
// are validated separately by HC5.
//
// Post-W2 (P3-T4): names are PurdexName values (Pdx-prefixed).
var expectedOpenCodeInstallableNames = []string{
	"PdxSessionStart",
	"PdxUserPromptSubmit",
	"PdxSubagentStart",
	"PdxSubagentStop",
	"PdxPermissionRequest",
	"PdxStop",
	"PdxStopFailure",
	"PdxSessionEnd",
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
		if got[e.PurdexName] {
			t.Errorf("opencode installable contains duplicate PurdexName %q", e.PurdexName)
		}
		got[e.PurdexName] = true
	}
	want := installableNameSet()
	for n := range want {
		if !got[n] {
			t.Errorf("opencode installable missing required PurdexName %q", n)
		}
	}
	for n := range got {
		if !want[n] {
			t.Errorf("opencode installable contains unexpected PurdexName %q", n)
		}
	}
}

// TestOpenCodeEvents_DetailOnlyHaveEmptyEmitsStatus guards the empty-slice
// convention for detail-only SubagentStart/Stop installable entries.
func TestOpenCodeEvents_DetailOnlyHaveEmptyEmitsStatus(t *testing.T) {
	p := opencode.NewProvider()
	for _, e := range p.Events() {
		if e.PurdexName != "PdxSubagentStart" && e.PurdexName != "PdxSubagentStop" {
			continue
		}
		if e.EmitsStatus == nil {
			t.Errorf("opencode %s EmitsStatus is nil; want non-nil empty slice", e.PurdexName)
		}
		if len(e.EmitsStatus) != 0 {
			t.Errorf("opencode %s EmitsStatus = %v, want empty", e.PurdexName, e.EmitsStatus)
		}
	}
}

// TestOpenCodeEvents_DescriptionsNonEmpty asserts every Description is
// non-empty, ≤70 chars (plan §2.2), no trailing period, and emoji-free.
func TestOpenCodeEvents_DescriptionsNonEmpty(t *testing.T) {
	p := opencode.NewProvider()
	for _, e := range p.Events() {
		if strings.TrimSpace(e.Description) == "" {
			t.Errorf("opencode %s Description is empty", e.PurdexName)
			continue
		}
		if len(e.Description) > 70 {
			t.Errorf("opencode %s Description %d chars > 70 (plan §2.2): %q", e.PurdexName, len(e.Description), e.Description)
		}
		if strings.HasSuffix(strings.TrimSpace(e.Description), ".") {
			t.Errorf("opencode %s Description ends with period (plan §2.2): %q", e.PurdexName, e.Description)
		}
		for _, r := range e.Description {
			if r >= 0x1F300 && r <= 0x1FAFF {
				t.Errorf("opencode %s Description contains emoji rune %U: %q", e.PurdexName, r, e.Description)
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
	first[0].PurdexName = "__mutated__"
	if len(first[0].EmitsStatus) > 0 {
		first[0].EmitsStatus[0] = agent.Status("__mutated__")
	}
	second := p.Events()
	if second[0].PurdexName == "__mutated__" {
		t.Errorf("opencode Events shares backing array; second call first PurdexName mutated to %q", second[0].PurdexName)
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

// catalogByPurdexName returns a PurdexName -> spec map. Used to resolve
// installable manifest entries via "Pdx" + purdexEventName.
func catalogByPurdexName(specs []agent.HookEventSpec) map[string]agent.HookEventSpec {
	out := make(map[string]agent.HookEventSpec, len(specs))
	for _, spec := range specs {
		out[spec.PurdexName] = spec
	}
	return out
}

// catalogByUpstreamKey returns an upstream-key -> spec map. Used to resolve
// non-installable (ignored/unsupported) manifest entries; a single upstream
// key maps to at most one non-installable catalog entry per spec §2.2.
func catalogByUpstreamKey(specs []agent.HookEventSpec) map[string]agent.HookEventSpec {
	out := make(map[string]agent.HookEventSpec, len(specs))
	for _, spec := range specs {
		for _, k := range spec.UpstreamKeys {
			out[k] = spec
		}
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
	specsByPurdexName := catalogByPurdexName(specs)
	specsByUpstreamKey := catalogByUpstreamKey(specs)
	frozen := loadFrozenEvents(t)

	// Track which events.go entries (keyed by PurdexName) get matched by
	// events.json (for rule b/c).
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
				// Manifest's purdexEventName is the legacy short form
				// ("Stop", "SessionStart"); post-W2 catalog stores the
				// "Pdx"-prefixed canonical PurdexName. Resolve via prefix.
				wantPurdex := "Pdx" + e.Purdex.PurdexEventName
				spec, ok := specsByPurdexName[wantPurdex]
				if !ok {
					t.Errorf("%s[%s] purdexEventName=%q has no opencodeEventSpecs entry with PurdexName=%q", prefix, e.UpstreamKey, e.Purdex.PurdexEventName, wantPurdex)
					continue
				}
				h := agent.EffectiveHookHandling(spec)
				if h != agent.HookHandlingStatus && h != agent.HookHandlingDetail {
					t.Errorf("%s[%s] purdexEventName=%q resolves to handling=%q; want status/detail", prefix, e.UpstreamKey, e.Purdex.PurdexEventName, h)
				}
				matchedSpecs[wantPurdex]++
			case "ignored":
				spec, ok := specsByUpstreamKey[e.UpstreamKey]
				if !ok {
					t.Errorf("%s[%s] kind=ignored but no opencodeEventSpecs entry with UpstreamKeys containing %q", prefix, e.UpstreamKey, e.UpstreamKey)
					continue
				}
				if agent.EffectiveHookHandling(spec) != agent.HookHandlingIgnored {
					t.Errorf("%s[%s] kind=ignored but opencodeEventSpecs[%q].Handling=%q", prefix, e.UpstreamKey, spec.PurdexName, agent.EffectiveHookHandling(spec))
				}
				matchedSpecs[spec.PurdexName]++
			case "unsupported":
				spec, ok := specsByUpstreamKey[e.UpstreamKey]
				if !ok {
					t.Errorf("%s[%s] kind=unsupported but no opencodeEventSpecs entry with UpstreamKeys containing %q", prefix, e.UpstreamKey, e.UpstreamKey)
					continue
				}
				if agent.EffectiveHookHandling(spec) != agent.HookHandlingUnsupported {
					t.Errorf("%s[%s] kind=unsupported but opencodeEventSpecs[%q].Handling=%q", prefix, e.UpstreamKey, spec.PurdexName, agent.EffectiveHookHandling(spec))
				}
				matchedSpecs[spec.PurdexName]++
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
		if matchedSpecs[spec.PurdexName] == 0 {
			t.Errorf("opencodeEventSpecs[%q] has no matching events.json entry (events.go-only forbidden by HC5 rule c)", spec.PurdexName)
		}
	}
}

// HC5b: collision check + non-installable explicit Handling.
// Plan v1.3 §3 + §2.2:
//   (a) ignored/unsupported entries set Handling explicitly (not blank);
//   (b) ignored/unsupported entry PurdexName values must NOT collide with the
//       8 installable Purdex Names (bipartite naming policy).
func TestOpenCodeEvents_NonInstallableHaveExplicitHandlingAndNoNameCollision(t *testing.T) {
	p := opencode.NewProvider()
	specs := p.Events()
	installable := installableNameSet()
	for _, spec := range specs {
		if agent.IsInstallableHookSpec(spec) {
			continue
		}
		if spec.Handling == "" {
			t.Errorf("opencode non-installable %q has blank Handling; expected explicit ignored/unsupported", spec.PurdexName)
		}
		if installable[spec.PurdexName] {
			t.Errorf("opencode non-installable PurdexName %q collides with installable Purdex Name set", spec.PurdexName)
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
			t.Errorf("opencode non-installable %q EmitsStatus is nil; want non-nil empty slice", spec.PurdexName)
		}
		if len(spec.EmitsStatus) != 0 {
			t.Errorf("opencode non-installable %q EmitsStatus=%v; want empty", spec.PurdexName, spec.EmitsStatus)
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

// === W2 Phase 3 (P3-T1) catalog naming separation invariants ===
//
// Mirrors cc/codex events_test.go invariant 1/3/4/5 assertions but skips
// invariant 2 (Name backfill mechanical rename) — opencode's non-installable
// entries use dotted-lowercase upstream names that don't satisfy the
// PurdexName == "Pdx" + TrimPrefix constraint (e.g. auth.session vs
// PdxAuthSession). Phase 3 P3-T4 removes the Name field entirely so this
// invariant is vacuous after merge.

// expectedOpenCodeLifecycle pins the LifecycleEventKind for every installable
// per spec §2.3.1, keyed on PurdexName post P3-T4. Non-installable entries
// default to LifecycleNone (verified directly in
// TestOpenCodeEventSpecs_LifecycleAlignment).
var expectedOpenCodeLifecycle = map[string]agent.LifecycleEventKind{
	"PdxSessionStart":      agent.LifecycleSessionStart,
	"PdxUserPromptSubmit":  agent.LifecycleUserPromptSubmit,
	"PdxSubagentStart":     agent.LifecycleSubagentStart,
	"PdxSubagentStop":      agent.LifecycleSubagentStop,
	"PdxPermissionRequest": agent.LifecycleNone,
	"PdxStop":              agent.LifecycleStop,
	"PdxStopFailure":       agent.LifecycleStopFailure,
	"PdxSessionEnd":        agent.LifecycleSessionEnd,
}

// expectedOpenCodeInstallableUpstreamKeys pins the multi-source mapping for
// the 8 installable entries per spec §2.3 — UpstreamKeys lists the plugin
// Bus event sources that fire this catalog entry. Keys are PurdexName.
var expectedOpenCodeInstallableUpstreamKeys = map[string][]string{
	"PdxSessionStart":      {"session.created"},
	"PdxUserPromptSubmit":  {"chat.message"},
	"PdxSubagentStart":     {"tool.execute.before"},
	"PdxSubagentStop":      {"tool.execute.after"},
	"PdxPermissionRequest": {"permission.asked", "question.asked"},
	"PdxStop":              {"session.status"},
	"PdxStopFailure":       {"session.error"},
	"PdxSessionEnd":        {"session.deleted"},
}

// opencodeLegacyMetadata mirrors cc/codex pattern for P3-T1 invariant 4 —
// preserved metadata after the plain-struct-literal rewrite. Keys are
// PurdexName post P3-T4.
type opencodeLegacyMetadata struct {
	EmitsStatus []agent.Status
	Description string
	FutureOnly  bool
	Handling    agent.HookHandling
}

var expectedOpenCodeInstallablePreservedMetadata = map[string]opencodeLegacyMetadata{
	"PdxSessionStart":      {EmitsStatus: []agent.Status{agent.StatusIdle}, Description: "OpenCode session started"},
	"PdxUserPromptSubmit":  {EmitsStatus: []agent.Status{agent.StatusRunning}, Description: "User submitted a prompt"},
	"PdxSubagentStart":     {EmitsStatus: []agent.Status{}, Description: "Nested sub-agent task dispatched"},
	"PdxSubagentStop":      {EmitsStatus: []agent.Status{}, Description: "Nested sub-agent task completed"},
	"PdxPermissionRequest": {EmitsStatus: []agent.Status{agent.StatusWaiting}, Description: "Tool permission request awaiting user approval"},
	"PdxStop":              {EmitsStatus: []agent.Status{agent.StatusIdle}, Description: "Agent finished responding and is idle"},
	"PdxStopFailure":       {EmitsStatus: []agent.Status{agent.StatusError}, Description: "Agent stopped due to an error"},
	"PdxSessionEnd":        {EmitsStatus: []agent.Status{agent.StatusClear}, Description: "OpenCode session ended"},
}

// TestOpenCodeEventSpecs_PurdexNamePdxPrefix verifies invariant 1 across all
// 65 entries: every PurdexName starts with "Pdx".
func TestOpenCodeEventSpecs_PurdexNamePdxPrefix(t *testing.T) {
	for _, e := range opencode.NewProvider().Events() {
		if e.PurdexName == "" {
			t.Errorf("opencode %v: PurdexName empty", e.UpstreamKeys)
			continue
		}
		if !strings.HasPrefix(e.PurdexName, "Pdx") {
			t.Errorf("opencode %q: PurdexName lacks Pdx prefix", e.PurdexName)
		}
	}
}

// TestOpenCodeEventSpecs_UpstreamKeysNotEmpty verifies invariant 1 sub-clause:
// every entry has a non-empty UpstreamKeys slice (installable / unsupported /
// ignored alike per spec §2.3 — non-installable UpstreamKeys = [legacy Name]).
func TestOpenCodeEventSpecs_UpstreamKeysNotEmpty(t *testing.T) {
	for _, e := range opencode.NewProvider().Events() {
		if len(e.UpstreamKeys) == 0 {
			t.Errorf("opencode %q: UpstreamKeys empty", e.PurdexName)
		}
	}
}

// TestOpenCodeEventSpecs_PurdexNameNotInUpstreamKeys verifies invariant 3.
func TestOpenCodeEventSpecs_PurdexNameNotInUpstreamKeys(t *testing.T) {
	for _, e := range opencode.NewProvider().Events() {
		for _, k := range e.UpstreamKeys {
			if k == e.PurdexName {
				t.Errorf("opencode %q: PurdexName %q present in UpstreamKeys", e.PurdexName, k)
			}
		}
	}
}

// TestOpenCodeEventSpecs_InstallableMultiSource pins the 8 installable
// entries' UpstreamKeys to the plugin Bus event sources per spec §2.3 —
// PdxPermissionRequest is the multi-source case (permission.asked +
// question.asked).
func TestOpenCodeEventSpecs_InstallableMultiSource(t *testing.T) {
	got := make(map[string][]string)
	for _, e := range opencode.NewProvider().Events() {
		if !agent.IsInstallableHookSpec(e) {
			continue
		}
		got[e.PurdexName] = e.UpstreamKeys
	}
	for name, want := range expectedOpenCodeInstallableUpstreamKeys {
		gotKeys, ok := got[name]
		if !ok {
			t.Errorf("opencode installable %q: not in catalog", name)
			continue
		}
		if len(gotKeys) != len(want) {
			t.Errorf("opencode installable %q: UpstreamKeys=%v want %v", name, gotKeys, want)
			continue
		}
		for i, k := range want {
			if gotKeys[i] != k {
				t.Errorf("opencode installable %q: UpstreamKeys[%d]=%q want %q", name, i, gotKeys[i], k)
			}
		}
	}
}

// TestOpenCodeEventSpecs_LifecycleAlignment verifies invariant 5 across all
// 65 entries: installable Lifecycle matches the §2.3.1 table; non-installable
// entries default to LifecycleNone.
func TestOpenCodeEventSpecs_LifecycleAlignment(t *testing.T) {
	for _, e := range opencode.NewProvider().Events() {
		want := agent.LifecycleNone
		if agent.IsInstallableHookSpec(e) {
			if w, ok := expectedOpenCodeLifecycle[e.PurdexName]; ok {
				want = w
			}
		}
		if e.Lifecycle != want {
			t.Errorf("opencode %q (installable=%v): Lifecycle=%v, want %v",
				e.PurdexName, agent.IsInstallableHookSpec(e), e.Lifecycle, want)
		}
	}
}

// TestOpenCodeEventSpecs_InstallablePreservedMetadata verifies invariant 4
// for the 8 installable entries — the plain-struct-literal rewrite in P3-T1
// did not drop any pre-W2 metadata field (EmitsStatus / Description /
// FutureOnly / Handling).
func TestOpenCodeEventSpecs_InstallablePreservedMetadata(t *testing.T) {
	for _, e := range opencode.NewProvider().Events() {
		if !agent.IsInstallableHookSpec(e) {
			continue
		}
		want, ok := expectedOpenCodeInstallablePreservedMetadata[e.PurdexName]
		if !ok {
			t.Errorf("opencode %q: not present in expectedOpenCodeInstallablePreservedMetadata; update fixture", e.PurdexName)
			continue
		}
		if e.Description != want.Description {
			t.Errorf("opencode %q: Description=%q want %q", e.PurdexName, e.Description, want.Description)
		}
		if e.FutureOnly != want.FutureOnly {
			t.Errorf("opencode %q: FutureOnly=%v want %v", e.PurdexName, e.FutureOnly, want.FutureOnly)
		}
		if e.Handling != want.Handling {
			t.Errorf("opencode %q: Handling=%q want %q", e.PurdexName, e.Handling, want.Handling)
		}
		if len(e.EmitsStatus) != len(want.EmitsStatus) {
			t.Errorf("opencode %q: EmitsStatus len=%d want %d (got=%v want=%v)", e.PurdexName, len(e.EmitsStatus), len(want.EmitsStatus), e.EmitsStatus, want.EmitsStatus)
			continue
		}
		gotSet := make(map[agent.Status]bool, len(e.EmitsStatus))
		for _, s := range e.EmitsStatus {
			gotSet[s] = true
		}
		for _, s := range want.EmitsStatus {
			if !gotSet[s] {
				t.Errorf("opencode %q: EmitsStatus missing %q (got %v)", e.PurdexName, s, e.EmitsStatus)
			}
		}
	}
}
