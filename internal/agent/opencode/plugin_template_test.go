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
	contractEventCasePattern   = regexp.MustCompile(`case '([^']+)':`)
	contractCallbackKeyPattern = regexp.MustCompile(`(?m)^\s*'([^']+)'\s*:\s*async\s*\(`)
	contractPayloadPathPattern = regexp.MustCompile(`\b(?:event\.properties|input|output)(?:\??\.[A-Za-z_][A-Za-z0-9_]*)+`)
	contractAliasPattern       = regexp.MustCompile(`\b(?:const|let|var)\s+([A-Za-z_$][A-Za-z0-9_$]*)\s*=\s*([^\n]+)`)
	contractDestructurePattern = regexp.MustCompile(`\b(?:const|let|var)\s+\{([^}]+)\}\s*=\s*([^\n]+)`)
	contractFunctionPattern    = regexp.MustCompile(`(?s)\bfunction\s+([A-Za-z_$][A-Za-z0-9_$]*)\s*\(\s*([A-Za-z_$][A-Za-z0-9_$]*)\s*\)\s*\{(.*?)\n\s*\}`)
	contractPathExprPattern    = regexp.MustCompile(`^(?:event\.properties|[A-Za-z_$][A-Za-z0-9_$]*)(?:\.[A-Za-z_][A-Za-z0-9_]*)*$`)
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
	addConsumedPayloadPathsFromScope(body, map[string]string{
		"event.properties": "event.properties",
		"input":            "input",
		"output":           "output",
	}, seen)
	for _, fn := range contractFunctionPattern.FindAllStringSubmatch(body, -1) {
		if len(fn) < 4 {
			continue
		}
		callPattern := regexp.MustCompile(`\b` + regexp.QuoteMeta(fn[1]) + `\s*\(\s*([^)]*?)\s*\)`)
		for _, call := range callPattern.FindAllStringSubmatch(body, -1) {
			if len(call) < 2 {
				continue
			}
			argPath, ok := resolveContractPath(strings.TrimSpace(call[1]), map[string]string{
				"event.properties": "event.properties",
				"input":            "input",
				"output":           "output",
			})
			if !ok {
				continue
			}
			addConsumedPayloadPathsFromScope(fn[3], map[string]string{fn[2]: argPath}, seen)
		}
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

func addConsumedPayloadPathsFromScope(body string, aliases map[string]string, seen map[string]bool) {
	aliases = cloneStringMap(aliases)
	for _, match := range contractPayloadPathPattern.FindAllString(body, -1) {
		path := strings.ReplaceAll(match, "?.", ".")
		seen[path] = true
	}
	for {
		changed := false
		for _, match := range contractAliasPattern.FindAllStringSubmatch(body, -1) {
			if len(match) < 3 {
				continue
			}
			path, ok := resolveContractPath(match[2], aliases)
			if !ok || aliases[match[1]] == path {
				continue
			}
			aliases[match[1]] = path
			seen[path] = true
			changed = true
		}
		for _, match := range contractDestructurePattern.FindAllStringSubmatch(body, -1) {
			if len(match) < 3 {
				continue
			}
			base, ok := resolveContractPath(match[2], aliases)
			if !ok {
				continue
			}
			for _, field := range strings.Split(match[1], ",") {
				name, alias, ok := parseContractDestructuredField(field)
				if !ok {
					continue
				}
				path := base + "." + name
				if aliases[alias] != path {
					aliases[alias] = path
					changed = true
				}
				seen[path] = true
			}
		}
		if !changed {
			break
		}
	}
	for alias, base := range aliases {
		if strings.Contains(alias, ".") {
			continue
		}
		aliasPathPattern := regexp.MustCompile(`\b` + regexp.QuoteMeta(alias) + `(?:\??\.[A-Za-z_][A-Za-z0-9_]*)+`)
		for _, match := range aliasPathPattern.FindAllString(body, -1) {
			path, ok := resolveContractPath(match, aliases)
			if ok {
				seen[path] = true
			}
		}
		if base != alias {
			seen[base] = true
		}
	}
}

func cloneStringMap(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func resolveContractPath(expr string, aliases map[string]string) (string, bool) {
	expr = strings.TrimSpace(strings.ReplaceAll(expr, "?.", "."))
	expr = strings.Trim(expr, "() ")
	expr = strings.TrimRight(expr, ";,")
	if !contractPathExprPattern.MatchString(expr) {
		return "", false
	}
	for alias, base := range aliases {
		if expr == alias {
			return base, true
		}
		if strings.HasPrefix(expr, alias+".") {
			return base + strings.TrimPrefix(expr, alias), true
		}
	}
	return "", false
}

func parseContractDestructuredField(field string) (name, alias string, ok bool) {
	field = strings.TrimSpace(field)
	if field == "" || strings.Contains(field, "...") {
		return "", "", false
	}
	parts := strings.Split(field, ":")
	name = strings.TrimSpace(parts[0])
	alias = name
	if len(parts) > 1 {
		alias = strings.TrimSpace(parts[1])
	}
	if name == "" || alias == "" || strings.ContainsAny(alias, "{}[]= ") {
		return "", "", false
	}
	return name, alias, true
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

func requireContractSampleRef(t *testing.T, fixture opencodeContractFixture, name string, evidence contractEvidence) any {
	t.Helper()
	if evidence.SampleRef == "" {
		t.Fatalf("%s evidence missing sampleRef", name)
	}
	value, ok := resolveContractSampleRef(fixture.Samples, evidence.SampleRef)
	if !ok {
		t.Fatalf("%s sampleRef %q does not resolve into fixture samples", name, evidence.SampleRef)
	}
	return value
}

func resolveContractSampleRef(samples map[string]any, ref string) (any, bool) {
	const prefix = "samples."
	if !strings.HasPrefix(ref, prefix) {
		return nil, false
	}
	path := strings.TrimPrefix(ref, prefix)
	for sampleKey, sample := range samples {
		if path == sampleKey {
			return sample, true
		}
		if strings.HasPrefix(path, sampleKey+".") {
			return resolveContractSamplePath(sample, strings.TrimPrefix(path, sampleKey+"."))
		}
	}
	return nil, false
}

func resolveContractSamplePath(value any, path string) (any, bool) {
	current := value
	for _, part := range strings.Split(path, ".") {
		node, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = node[part]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

func requireContractSampleEvent(t *testing.T, fixture opencodeContractFixture, event contractEvent) {
	t.Helper()
	value := requireContractSampleRef(t, fixture, "event "+event.Key, event.Evidence)
	if got, ok := value.(string); !ok || got != event.Key {
		t.Fatalf("event %s sampleRef resolves to %#v, want event key", event.Key, value)
	}
}

func requireContractSamplePayloadPath(t *testing.T, fixture opencodeContractFixture, path contractPath) {
	t.Helper()
	relativePath, ok := contractSampleRefRelativePath(fixture.Samples, path.Evidence.SampleRef)
	if !ok {
		t.Fatalf("path %s sampleRef %q does not resolve into a fixture sample", path.Path, path.Evidence.SampleRef)
	}
	if relativePath != path.Path {
		t.Fatalf("path %s sampleRef %q points at %s", path.Path, path.Evidence.SampleRef, relativePath)
	}
	requireContractSampleRef(t, fixture, "path "+path.Path, path.Evidence)
}

func contractSampleRefRelativePath(samples map[string]any, ref string) (string, bool) {
	const prefix = "samples."
	if !strings.HasPrefix(ref, prefix) {
		return "", false
	}
	path := strings.TrimPrefix(ref, prefix)
	for sampleKey := range samples {
		if strings.HasPrefix(path, sampleKey+".") {
			return strings.TrimPrefix(path, sampleKey+"."), true
		}
	}
	return "", false
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
	if got := requireContractSampleRef(t, fixture, "version", fixture.Version.Evidence); got != "1.14.23\n" {
		t.Fatalf("version sampleRef resolves to %#v, want version stdout", got)
	}
	if len(fixture.Samples) == 0 {
		t.Fatal("fixture samples are empty")
	}
	for _, event := range fixture.ConsumedEvents {
		if event.Key == "" {
			t.Fatal("fixture contains event with empty key")
		}
		requireContractEvidence(t, "event "+event.Key, event.Evidence)
		requireContractSampleEvent(t, fixture, event)
	}
	for _, path := range fixture.ConsumedPayloadPaths {
		if path.Path == "" {
			t.Fatal("fixture contains empty payload path")
		}
		requireContractEvidence(t, "path "+path.Path, path.Evidence)
		requireContractSamplePayloadPath(t, fixture, path)
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

func TestOpenCodeTemplateContractExtractionRejectsMutatedHelperLocalPayloadPath(t *testing.T) {
	fixture := loadOpenCodeContractFixture(t)
	body := strings.Replace(renderManagedPlugin("/fake/pdx"), "args.subagent_type", "args.worker_type", 1)
	got := extractTemplateConsumedPayloadPaths(body)
	want := fixtureConsumedPayloadPaths(fixture)
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, "\x00") == strings.Join(want, "\x00") {
		t.Fatalf("mutated helper-local payload path still matched fixture contract: %v", got)
	}
}

func TestOpenCodeTemplateContractExtractionRejectsMutatedAliasPayloadPath(t *testing.T) {
	fixture := loadOpenCodeContractFixture(t)
	body := strings.Replace(renderManagedPlugin("/fake/pdx"), "model.providerID", "model.providerName", 1)
	got := extractTemplateConsumedPayloadPaths(body)
	want := fixtureConsumedPayloadPaths(fixture)
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, "\x00") == strings.Join(want, "\x00") {
		t.Fatalf("mutated alias payload path still matched fixture contract: %v", got)
	}
}

func TestOpenCodeTemplateContractExtractionRejectsMutatedDestructuredPayloadPath(t *testing.T) {
	fixture := loadOpenCodeContractFixture(t)
	body := strings.Replace(renderManagedPlugin("/fake/pdx"),
		"const modelName = model ? (model.providerID + '/' + model.modelID) : ''",
		"const { providerID, modelID, providerName } = model || {}\n      const modelName = providerName || (providerID + '/' + modelID)", 1)
	got := extractTemplateConsumedPayloadPaths(body)
	want := fixtureConsumedPayloadPaths(fixture)
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, "\x00") == strings.Join(want, "\x00") {
		t.Fatalf("mutated destructured payload path still matched fixture contract: %v", got)
	}
}

func TestOpenCodeTemplateContractExtractionFindsHelperLocalPayloadPaths(t *testing.T) {
	body := `
function agentTypeFromArgs(args) {
  if (typeof args.subagent_type === 'string') return args.subagent_type
  if (typeof args.agent === 'string') return args.agent
  return 'task'
}
'tool.execute.before': async (input, output) => {
  const agentType = agentTypeFromArgs(output.args)
}
`
	got := extractTemplateConsumedPayloadPaths(body)
	want := []string{"output.args.agent", "output.args.subagent_type"}
	requireExactStringSet(t, "helper-local payload paths", got, want)
}

func TestOpenCodeTemplateContractExtractionFindsAliasAndDestructuredPayloadPaths(t *testing.T) {
	body := `
'chat.message': async (input, output) => {
  const model = input.model
  const { providerID, modelID: modelName } = model
  await emit('UserPromptSubmit', { providerID, modelName })
}
`
	got := extractTemplateConsumedPayloadPaths(body)
	want := []string{"input.model.providerID", "input.model.modelID"}
	requireExactStringSet(t, "alias/destructured payload paths", got, want)
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
