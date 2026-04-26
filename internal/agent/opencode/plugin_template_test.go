package opencode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/agent"
)

type opencodeContractFixture struct {
	Version struct {
		ExactOutput string           `json:"exactOutput"`
		Evidence    contractEvidence `json:"evidence"`
	} `json:"version"`
	ConsumedEvents       []contractEvent `json:"consumedEvents"`
	ConsumedPayloadPaths []contractPath  `json:"consumedPayloadPaths"`
	Samples              map[string]any  `json:"samples"`
}

type contractEvent struct {
	Key      string           `json:"key"`
	Evidence contractEvidence `json:"evidence"`
}

type contractPath struct {
	Path     string           `json:"path"`
	Evidence contractEvidence `json:"evidence"`
}

type contractEvidence struct {
	Kind      string `json:"kind"`
	Source    string `json:"source"`
	Tag       string `json:"tag"`
	Commit    string `json:"commit"`
	LineRange string `json:"lineRange"`
	SampleRef string `json:"sampleRef"`
}

var (
	contractEventCasePattern      = regexp.MustCompile(`case '([^']+)':`)
	contractCallbackKeyPattern    = regexp.MustCompile(`(?m)^\s*'([^']+)'\s*:\s*async\s*\(`)
	contractPayloadPathPattern    = regexp.MustCompile(`\b(?:event\.properties|input|output)(?:\??\.[A-Za-z_][A-Za-z0-9_]*)+`)
	contractModelAliasPathPattern = regexp.MustCompile(`\bmodel\.(providerID|modelID)\b`)
)

func loadOpenCodeContractFixture(t *testing.T) opencodeContractFixture {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "opencode-1.14.23-contract.json"))
	if err != nil {
		t.Fatalf("read OpenCode contract fixture: %v", err)
	}
	var fixture opencodeContractFixture
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatalf("parse OpenCode contract fixture: %v", err)
	}
	return fixture
}

func extractTemplateConsumedEventKeys(body string) []string {
	seen := map[string]bool{}
	var out []string
	for _, re := range []*regexp.Regexp{contractEventCasePattern, contractCallbackKeyPattern} {
		for _, match := range re.FindAllStringSubmatch(body, -1) {
			if len(match) < 2 || seen[match[1]] {
				continue
			}
			seen[match[1]] = true
			out = append(out, match[1])
		}
	}
	sort.Strings(out)
	return out
}

func extractTemplateConsumedPayloadPaths(body string) []string {
	seen := map[string]bool{}
	for _, match := range contractPayloadPathPattern.FindAllString(body, -1) {
		path := strings.ReplaceAll(match, "?.", ".")
		seen[path] = true
	}
	if strings.Contains(body, "const model = input.model") {
		for _, match := range contractModelAliasPathPattern.FindAllStringSubmatch(body, -1) {
			if len(match) >= 2 {
				seen["input.model."+match[1]] = true
			}
		}
	}
	if strings.Contains(body, "agentTypeFromArgs(output.args)") {
		seen["output.args.subagent_type"] = true
		seen["output.args.agent"] = true
	}
	delete(seen, "input.model")
	delete(seen, "output.args")
	paths := make([]string, 0, len(seen))
	for path := range seen {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

func fixtureConsumedEventKeys(fixture opencodeContractFixture) []string {
	out := make([]string, 0, len(fixture.ConsumedEvents))
	for _, event := range fixture.ConsumedEvents {
		out = append(out, event.Key)
	}
	sort.Strings(out)
	return out
}

func fixtureConsumedPayloadPaths(fixture opencodeContractFixture) []string {
	out := make([]string, 0, len(fixture.ConsumedPayloadPaths))
	for _, path := range fixture.ConsumedPayloadPaths {
		out = append(out, path.Path)
	}
	sort.Strings(out)
	return out
}

func requireExactStringSet(t *testing.T, name string, got, want []string) {
	t.Helper()
	got = append([]string(nil), got...)
	want = append([]string(nil), want...)
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("%s mismatch\n got: %v\nwant: %v", name, got, want)
	}
}

func requireContractEvidence(t *testing.T, name string, evidence contractEvidence) {
	t.Helper()
	if evidence.Kind != "docs" && evidence.Kind != "source" && evidence.Kind != "runtime" {
		t.Fatalf("%s evidence kind = %q, want docs/source/runtime", name, evidence.Kind)
	}
	if evidence.Source == "" {
		t.Fatalf("%s evidence source is empty", name)
	}
	if evidence.Kind == "runtime" {
		if evidence.SampleRef == "" {
			t.Fatalf("%s runtime evidence missing sampleRef", name)
		}
		return
	}
	if evidence.Tag == "" && evidence.Commit == "" {
		t.Fatalf("%s evidence missing tag or commit", name)
	}
	if evidence.LineRange == "" {
		t.Fatalf("%s evidence missing source range", name)
	}
}

func TestPluginState_TaskStartMapsSubagentStart(t *testing.T) {
	state := newPluginState()
	event, ok := state.handleTaskStart("sess-1", "call-1", map[string]any{
		"subagent_type": "Explore",
		"description":   "trace tree",
		"prompt":        "inspect process tree",
	})
	if !ok {
		t.Fatal("expected task start event")
	}
	if event.Name != "SubagentStart" {
		t.Fatalf("event name = %q, want SubagentStart", event.Name)
	}
	if event.Payload["agent_id"] != "call-1" {
		t.Fatalf("agent_id = %#v, want call-1", event.Payload["agent_id"])
	}
	if event.Payload["agent_type"] != "Explore" {
		t.Fatalf("agent_type = %#v, want Explore", event.Payload["agent_type"])
	}
	if len(state.activeSubagents) != 1 || state.activeSubagents["sess-1:call-1"] != "Explore" {
		t.Fatalf("activeSubagents = %#v, want sess-1:call-1 => Explore", state.activeSubagents)
	}
}

func TestPluginState_TaskStartDuplicateCallIDIgnored(t *testing.T) {
	state := newPluginState()
	if _, ok := state.handleTaskStart("sess-1", "call-1", map[string]any{"agent": "Explore"}); !ok {
		t.Fatal("first start should be accepted")
	}
	if _, ok := state.handleTaskStart("sess-1", "call-1", map[string]any{"agent": "Plan"}); ok {
		t.Fatal("duplicate start should be ignored")
	}
	if got := state.activeSubagents["sess-1:call-1"]; got != "Explore" {
		t.Fatalf("activeSubagents[sess-1:call-1] = %q, want Explore", got)
	}
}

func TestPluginState_TaskStartSameCallIDDifferentSessionAllowed(t *testing.T) {
	state := newPluginState()
	if _, ok := state.handleTaskStart("sess-1", "call-1", map[string]any{"agent": "Explore"}); !ok {
		t.Fatal("first start should be accepted")
	}
	if _, ok := state.handleTaskStart("sess-2", "call-1", map[string]any{"agent": "Plan"}); !ok {
		t.Fatal("same callID in different session should be accepted")
	}
	if got := state.activeSubagents["sess-2:call-1"]; got != "Plan" {
		t.Fatalf("activeSubagents[sess-2:call-1] = %q, want Plan", got)
	}
}

func TestPluginState_TaskStartEmptyCallIDIgnored(t *testing.T) {
	state := newPluginState()
	if _, ok := state.handleTaskStart("sess-1", "", map[string]any{"agent": "Explore"}); ok {
		t.Fatal("empty callID should be ignored")
	}
	if len(state.activeSubagents) != 0 {
		t.Fatalf("activeSubagents = %#v, want empty", state.activeSubagents)
	}
}

func TestPluginState_TaskStopMapsSubagentStop(t *testing.T) {
	state := newPluginState()
	if _, ok := state.handleTaskStart("sess-1", "call-1", map[string]any{"agent": "Explore"}); !ok {
		t.Fatal("task start should be accepted")
	}
	event, ok := state.handleTaskStop("sess-1", "call-1", "done", "all good")
	if !ok {
		t.Fatal("expected task stop event")
	}
	if event.Name != "SubagentStop" {
		t.Fatalf("event name = %q, want SubagentStop", event.Name)
	}
	if event.Payload["agent_id"] != "call-1" {
		t.Fatalf("agent_id = %#v, want call-1", event.Payload["agent_id"])
	}
	if event.Payload["agent_type"] != "Explore" {
		t.Fatalf("agent_type = %#v, want Explore", event.Payload["agent_type"])
	}
	if len(state.activeSubagents) != 0 {
		t.Fatalf("activeSubagents = %#v, want empty", state.activeSubagents)
	}
}

func TestPluginState_TaskStopUnknownCallIDIgnored(t *testing.T) {
	state := newPluginState()
	if _, ok := state.handleTaskStop("sess-1", "missing", "done", "all good"); ok {
		t.Fatal("unknown callID stop should be ignored")
	}
}

func TestPluginState_TaskStopBeforeStartIgnored(t *testing.T) {
	state := newPluginState()
	if _, ok := state.handleTaskStop("sess-1", "call-1", "done", "all good"); ok {
		t.Fatal("stop-before-start should be ignored")
	}
	if len(state.activeSubagents) != 0 {
		t.Fatalf("activeSubagents = %#v, want empty", state.activeSubagents)
	}
}

func TestPluginState_SuppressIdleAfterError(t *testing.T) {
	state := newPluginState()
	event, ok := state.handleSessionError("provider_error", "boom")
	if !ok {
		t.Fatal("expected stop failure event")
	}
	if event.Name != "StopFailure" {
		t.Fatalf("event name = %q, want StopFailure", event.Name)
	}
	if _, ok := state.handleSessionIdle(); ok {
		t.Fatal("first idle after error should be suppressed")
	}
	event, ok = state.handleSessionIdle()
	if !ok {
		t.Fatal("second idle should emit stop")
	}
	if event.Name != "Stop" {
		t.Fatalf("event name = %q, want Stop", event.Name)
	}
}

// TestValidateSpecsCoverEmitted_Equal is the fix-plan §2.3 PT2 assertion:
// the real rendered body and opencodeEventSpecs agree on the event set.
// Together with PT7 this is the build-time contract guard that keeps the
// JS template and Go-side specs from drifting apart (plan §1.5 — runtime
// is deliberately not involved).
func TestValidateSpecsCoverEmitted_Equal(t *testing.T) {
	body := renderManagedPlugin("/fake/pdx")
	if err := validateSpecsCoverEmitted(body, opencodeEventSpecs); err != nil {
		t.Fatalf("validateSpecsCoverEmitted for the real template must be nil; got %v", err)
	}
}

// TestValidateSpecsCoverEmitted_EmitNotInSpec is the fix-plan §2.3 PT3
// assertion: a template that emits an event not listed in specs must
// error. Failure mode this catches: someone adding a new emit() without
// updating opencodeEventSpecs.
func TestValidateSpecsCoverEmitted_EmitNotInSpec(t *testing.T) {
	body := renderManagedPlugin("/fake/pdx") + "\nawait emit('Ghost', {})\n"
	if err := validateSpecsCoverEmitted(body, opencodeEventSpecs); err == nil {
		t.Fatal("expected error for emit-not-in-spec; got nil")
	}
}

// TestValidateSpecsCoverEmitted_SpecNotInEmit is the fix-plan §2.3 PT4
// assertion: a specs list that declares an event the template never
// emits must error. Failure mode this catches: removing an emit without
// updating opencodeEventSpecs.
func TestValidateSpecsCoverEmitted_SpecNotInEmit(t *testing.T) {
	body := renderManagedPlugin("/fake/pdx")
	extended := append([]agent.HookEventSpec(nil), opencodeEventSpecs...)
	extended = append(extended, agent.HookEventSpec{Name: "Phantom"})
	if err := validateSpecsCoverEmitted(body, extended); err == nil {
		t.Fatal("expected error for spec-not-in-emit; got nil")
	}
}

func TestValidateSpecsCoverEmitted_IgnoresNonInstallableSpecs(t *testing.T) {
	body := renderManagedPlugin("/fake/pdx")
	extended := append([]agent.HookEventSpec(nil), opencodeEventSpecs...)
	extended = append(extended,
		agent.HookEventSpec{Name: "IgnoredSynthetic", Handling: agent.HookHandlingIgnored, Description: "Ignored synthetic hook"},
		agent.HookEventSpec{Name: "UnsupportedSynthetic", Handling: agent.HookHandlingUnsupported, Description: "Unsupported synthetic hook"},
	)
	if err := validateSpecsCoverEmitted(body, extended); err != nil {
		t.Fatalf("validateSpecsCoverEmitted should ignore non-installable specs; got %v", err)
	}
}

func TestOpenCodeCheckHooks_ExcludesNonInstallableSpecs(t *testing.T) {
	original := opencodeEventSpecs
	opencodeEventSpecs = append(append([]agent.HookEventSpec(nil), opencodeEventSpecs...),
		agent.HookEventSpec{Name: "IgnoredSynthetic", Handling: agent.HookHandlingIgnored, Description: "Ignored synthetic hook"},
		agent.HookEventSpec{Name: "UnsupportedSynthetic", Handling: agent.HookHandlingUnsupported, Description: "Unsupported synthetic hook"},
	)
	t.Cleanup(func() { opencodeEventSpecs = original })

	for _, name := range opencodeEventNames() {
		if name == "IgnoredSynthetic" || name == "UnsupportedSynthetic" {
			t.Fatalf("opencodeEventNames included non-installable spec %q", name)
		}
	}

	home := t.TempDir()
	t.Setenv("HOME", home)
	SetResolveCanonicalPdxPathForTesting(t, func() (string, bool) { return "/usr/local/bin/pdx", true })
	p := NewProvider()
	if err := p.InstallHooks("/usr/local/bin/pdx"); err != nil {
		t.Fatalf("InstallHooks: %v", err)
	}
	status, err := p.CheckHooks()
	if err != nil {
		t.Fatalf("CheckHooks: %v", err)
	}
	pluginPath := filepath.Join(home, ".config", "opencode", "plugins", "pdx-agent-hooks.js")
	if _, err := os.Stat(pluginPath); err != nil {
		t.Fatalf("expected managed plugin at %s: %v", pluginPath, err)
	}
	for _, name := range []string{"IgnoredSynthetic", "UnsupportedSynthetic"} {
		if _, ok := status.Events[name]; ok {
			t.Errorf("CheckHooks.Events included non-installable event %q", name)
		}
	}
}

func TestOpenCodeTemplateEventContractsDocumented(t *testing.T) {
	fixture := loadOpenCodeContractFixture(t)
	requireExactStringSet(t, "consumed event keys", fixtureConsumedEventKeys(fixture), extractTemplateConsumedEventKeys(renderManagedPlugin("/fake/pdx")))
}

func TestOpenCodeTemplatePayloadPathsDocumented(t *testing.T) {
	fixture := loadOpenCodeContractFixture(t)
	requireExactStringSet(t, "consumed payload paths", fixtureConsumedPayloadPaths(fixture), extractTemplateConsumedPayloadPaths(renderManagedPlugin("/fake/pdx")))
}

func TestOpenCodeTemplateUsesOnlyDocumentedContractEvents(t *testing.T) {
	fixture := loadOpenCodeContractFixture(t)
	want := []string{
		"chat.message",
		"permission.asked",
		"question.asked",
		"session.created",
		"session.deleted",
		"session.error",
		"session.idle",
		"tool.execute.after",
		"tool.execute.before",
	}
	requireExactStringSet(t, "documented OpenCode callback events", extractTemplateConsumedEventKeys(renderManagedPlugin("/fake/pdx")), want)
	requireExactStringSet(t, "fixture OpenCode callback events", fixtureConsumedEventKeys(fixture), want)
}

func TestOpenCodeTemplateContractFixtureHasProvenance(t *testing.T) {
	fixture := loadOpenCodeContractFixture(t)
	if fixture.Version.ExactOutput != "1.14.23" {
		t.Fatalf("version exactOutput = %q, want 1.14.23", fixture.Version.ExactOutput)
	}
	requireContractEvidence(t, "version", fixture.Version.Evidence)
	if len(fixture.Samples) == 0 {
		t.Fatal("fixture samples are empty")
	}
	for _, event := range fixture.ConsumedEvents {
		if event.Key == "" {
			t.Fatal("fixture contains event with empty key")
		}
		requireContractEvidence(t, "event "+event.Key, event.Evidence)
	}
	for _, path := range fixture.ConsumedPayloadPaths {
		if path.Path == "" {
			t.Fatal("fixture contains empty payload path")
		}
		requireContractEvidence(t, "path "+path.Path, path.Evidence)
	}
}

func TestOpenCodeTemplateContractExtractionRejectsMutatedCallbackKey(t *testing.T) {
	fixture := loadOpenCodeContractFixture(t)
	body := strings.Replace(renderManagedPlugin("/fake/pdx"), "'chat.message': async", "'message.updated': async", 1)
	got := extractTemplateConsumedEventKeys(body)
	want := fixtureConsumedEventKeys(fixture)
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, "\x00") == strings.Join(want, "\x00") {
		t.Fatalf("mutated callback key still matched fixture contract: %v", got)
	}
}

func TestOpenCodeTemplateContractExtractionRejectsMutatedPayloadPath(t *testing.T) {
	fixture := loadOpenCodeContractFixture(t)
	body := strings.Replace(renderManagedPlugin("/fake/pdx"), "output.output || ''", "output.text || ''", 1)
	got := extractTemplateConsumedPayloadPaths(body)
	want := fixtureConsumedPayloadPaths(fixture)
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, "\x00") == strings.Join(want, "\x00") {
		t.Fatalf("mutated payload path still matched fixture contract: %v", got)
	}
}

// TestRenderManagedPlugin_ProducesValidBody is the fix-plan §2.3 PT5
// assertion: rendering does not panic and the body contains the managed
// marker that CheckHooks keys off of.
func TestRenderManagedPlugin_ProducesValidBody(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("renderManagedPlugin panicked: %v", r)
		}
	}()
	body := renderManagedPlugin("/fake/pdx")
	if !strings.Contains(body, managedMarker) {
		t.Fatalf("rendered body missing managed marker %q", managedMarker)
	}
}

// TestTemplateSpecsParity is the fix-plan §2.3 PT7 build-time contract
// guard. If the template emits X and specs do not declare X (or vice
// versa) this test fails and blocks the merge — so commits never ship an
// inconsistent pair. Per v4 plan §1.5 this replaces a runtime panic that
// would have punished every CheckHooks / Install call for a build-time
// drift; contract enforcement belongs at test layer.
func TestTemplateSpecsParity(t *testing.T) {
	body := renderManagedPlugin("/fake/pdx")
	if err := validateSpecsCoverEmitted(body, opencodeEventSpecs); err != nil {
		t.Fatalf("template/specs parity drifted: %v", err)
	}
}

// TestExtractEmittedEvents is the fix-plan §2.3 PT1 assertion: the helper
// reliably pulls event names from emit('...') and emit("...") calls in a
// synthetic body, including across multiple lines and with incidental
// whitespace. This helper is test-only (plan §1.5); it underwrites the
// template/specs parity check, not runtime health.
func TestExtractEmittedEvents(t *testing.T) {
	body := `
  await emit('SessionStart', {foo: 1})
  await emit("UserPromptSubmit",
    {bar: 2})
  await emit('Stop', {})
  // not captured: commented out. // await emit('Ignored', {})
`
	got := extractEmittedEvents(body)
	want := map[string]bool{"SessionStart": true, "UserPromptSubmit": true, "Stop": true, "Ignored": true}
	// We deliberately include 'Ignored' from a comment — regex-based extraction
	// is known to see through comments. That is fine because this helper is
	// only used against the real template body where no such comments exist,
	// and PT2-PT4 tests assert the spec-side parity that would surface a
	// mismatch if such a ghost emit ever shipped.
	gotSet := make(map[string]bool, len(got))
	for _, n := range got {
		gotSet[n] = true
	}
	for n := range want {
		if !gotSet[n] {
			t.Errorf("extractEmittedEvents missing %q; got %v", n, got)
		}
	}
}

// TestExtractPdxPath_RoundtripEscapedLiterals is the fix-plan §2.3 PT6
// assertion: renderManagedPlugin writes pdxPath with %q (complete with
// escapes for backslash/quote/unicode/…) and extractPdxPath must be able
// to round-trip it. Using a naive regex (e.g. `"([^"]+)"`) would stop at
// the first escaped quote and then double-escape when re-rendered,
// producing a byte-mismatch on perfectly managed plugins — the v4 plan
// specifically calls this out.
func TestExtractPdxPath_RoundtripEscapedLiterals(t *testing.T) {
	cases := []string{
		"/path with spaces/pdx",
		`C:\Users\foo\pdx.exe`,
		`/weird"path/pdx`,
		"/使用者/pdx",
	}
	for _, input := range cases {
		body := renderManagedPlugin(input)
		got, ok := extractPdxPath(body)
		if !ok {
			t.Errorf("extractPdxPath failed for %q", input)
			continue
		}
		if got != input {
			t.Errorf("extractPdxPath(%q) round-trip = %q", input, got)
		}
	}
}

func TestRenderManagedPlugin_UsesInputModelAndSessionScopedSubagentKeys(t *testing.T) {
	rendered := renderManagedPlugin("/usr/local/bin/pdx")
	if !strings.Contains(rendered, "const model = input.model") {
		t.Fatalf("rendered plugin should read model from input.model: %s", rendered)
	}
	if !strings.Contains(rendered, "const subagentKey = input.sessionID + ':' + input.callID") {
		t.Fatalf("rendered plugin should scope subagent keys by sessionID+callID: %s", rendered)
	}
	if !strings.Contains(rendered, "if (activeSubagents.has(subagentKey)) return") {
		t.Fatalf("rendered plugin should ignore duplicate subagent starts: %s", rendered)
	}
}
