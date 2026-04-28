package codex

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/wake/purdex/internal/agent"
)

const codexHooksSupportedVersion = "0.124.0"

func (p *Provider) InstallHooks(pdxPath string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("cannot determine home directory: %w", err)
	}
	hooksPath := filepath.Join(home, ".codex", "hooks.json")
	configPath := filepath.Join(home, ".codex", "config.toml")
	return installCodexHooks(configPath, hooksPath, pdxPath)
}

func (p *Provider) RemoveHooks(pdxPath string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("cannot determine home directory: %w", err)
	}
	hooksPath := filepath.Join(home, ".codex", "hooks.json")
	return mergeCodexHooks(hooksPath, pdxPath, true)
}

func (p *Provider) CheckHooks() (agent.HookStatus, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return agent.HookStatus{Issues: []string{"cannot find home dir"}}, err
	}
	agentVersion := agent.DetectHookAgentVersion("codex", "--version")
	hooksPath := filepath.Join(home, ".codex", "hooks.json")
	data, err := os.ReadFile(hooksPath)
	if err != nil {
		return agent.HookStatus{
			Installed:        false,
			Events:           map[string]agent.HookEventInfo{},
			Issues:           []string{"hooks.json not found"},
			AgentVersion:     agentVersion,
			SupportedVersion: codexHooksSupportedVersion,
			ExceedsSupport:   agent.CompareHookAgentVersions(agentVersion, codexHooksSupportedVersion) > 0,
		}, nil
	}
	var hooksFile map[string]any
	if err := json.Unmarshal(data, &hooksFile); err != nil {
		return agent.HookStatus{}, fmt.Errorf("parse hooks.json: %w", err)
	}
	hooks, _ := hooksFile["hooks"].(map[string]any)
	allSpecs := p.Events()
	specs := make([]agent.HookEventSpec, 0, len(allSpecs))
	for _, spec := range allSpecs {
		if agent.IsInstallableHookSpec(spec) {
			specs = append(specs, spec)
		}
	}
	events := make(map[string]agent.HookEventInfo, len(specs))
	var issues []string
	var upgrades []string
	allInstalled := true
	for _, spec := range specs {
		info, addIssues, blocks := checkCodexEvent(spec, hooks)
		// Propagate the FutureOnly bit to HookEventInfo so the UI can
		// render "tolerated absent" vs "required missing" distinctly.
		info.FutureOnly = spec.FutureOnly
		events[spec.Name] = info
		issues = append(issues, addIssues...)
		if blocks {
			allInstalled = false
		}
		if spec.FutureOnly && !info.Installed {
			// Tolerated-absent FutureOnly → advertise as available upgrade.
			if _, keyExists := hooks[spec.Name]; !keyExists {
				upgrades = append(upgrades, spec.Name)
			}
		}
	}
	managed := codexHooksManaged(hooks, allSpecs)
	featureEnabled, err := codexHooksFeatureEnabled(filepath.Join(home, ".codex", "config.toml"))
	if err != nil {
		return agent.HookStatus{}, err
	}
	if !featureEnabled {
		allInstalled = false
		issues = append(issues, "codex hooks feature flag disabled; run install to enable features.codex_hooks")
	}
	return agent.HookStatus{
		Installed:         allInstalled,
		Managed:           managed,
		UpgradesAvailable: upgrades,
		Events:            events,
		Issues:            issues,
		AgentVersion:      agentVersion,
		SupportedVersion:  codexHooksSupportedVersion,
		ExceedsSupport:    agent.CompareHookAgentVersions(agentVersion, codexHooksSupportedVersion) > 0,
	}, nil
}

func installCodexHooks(configPath, hooksPath, pdxPath string) error {
	config, err := readCodexConfig(configPath)
	if err != nil {
		return fmt.Errorf("parse %s: %w", configPath, err)
	}
	hooksFile, err := readCodexHooksFile(hooksPath)
	if err != nil {
		return err
	}
	if err := validateCodexInstallableHookShapes(hooksFile); err != nil {
		return err
	}
	setCodexHooksFeature(config)
	if err := mergeCodexHooksFile(hooksFile, pdxPath, false); err != nil {
		return err
	}
	if err := writeCodexHooksFile(hooksPath, hooksFile); err != nil {
		return err
	}
	return writeCodexConfig(configPath, config)
}

// codexHooksManaged reports whether hooks.json contains any pdx-owned
// entry (per-event matcher-group shape OR legacy direct-entry shape).
// Distinct from CheckHooks.Installed: a drifted-but-pdx-owned state has
// Managed=true and Installed=false, which the UI needs so the Remove
// button stays enabled.
func codexHooksManaged(hooks map[string]any, _ []agent.HookEventSpec) bool {
	owned := codexOwnedCleanupEventNames()
	for _, entries := range hooks {
		if hasLegacyPdxDirectCodexEntryOwned(entries, owned) {
			return true
		}
		for _, groupEntry := range codexMatcherGroups(entries) {
			group, ok := groupEntry.(map[string]any)
			if !ok {
				continue
			}
			for _, entry := range toCodexEntrySlice(group["hooks"]) {
				m, ok := entry.(map[string]any)
				if !ok {
					continue
				}
				cmd, _ := m["command"].(string)
				if isPdxCommandCodexOwnedEvent(cmd, owned) {
					return true
				}
			}
		}
	}
	return false
}

// checkCodexEvent classifies a single hook event as absent / broken / valid
// per fix-plan §1.4 and applies the FutureOnly-aware decision table. It
// takes the full hooks map so absent vs present-but-empty can be told apart
// via the double-return form `entries, ok := hooks[key]` — a hand-edited
// value of `[]any{}` must be classified as broken, not absent.
//
// W2 P2-T3: hooks.json is keyed by spec.UpstreamKeys[0] (upstream Codex
// hook event name); the per-event command's trailing token is spec.PurdexName
// (daemon-internal canonical id). The lookup mirrors the write boundary.
//
// Returns:
//   - HookEventInfo for events[spec.Name]
//   - zero or more Issue strings to append
//   - whether this event blocks allInstalled
func checkCodexEvent(spec agent.HookEventSpec, hooks map[string]any) (agent.HookEventInfo, []string, bool) {
	key := spec.UpstreamKeys[0]
	entries, keyExists := hooks[key]
	// State A — absent (strict: key not present in map).
	if !keyExists {
		info := agent.HookEventInfo{Installed: false}
		if spec.FutureOnly {
			// Tolerated legacy: no issue, does not block.
			return info, nil, false
		}
		return info, []string{key + " hook not installed"}, true
	}
	// Legacy direct-entry shape is its own signal (present but uses the
	// pre-0.121 shape pdx installer no longer writes).
	if hasLegacyPdxDirectCodexEntry(entries) {
		return agent.HookEventInfo{Installed: false},
			[]string{key + " hook uses legacy format; reinstall required"},
			true
	}
	// Find a pdx command inside the matcher-group list that passes strict
	// per-event validation (tokenized binary / --agent codex / matching
	// PurdexName tail). Empty entries, wrong shape, wrong agent, wrong
	// command tail all collapse to command == "" here → State B.
	command := findPdxCommandInCodexForEvent(entries, spec.PurdexName)
	if command == "" {
		if spec.FutureOnly {
			return agent.HookEventInfo{Installed: false},
				[]string{key + " hook: pdx command malformed (FutureOnly event has existing hook entry but pdx path incorrect — run install to repair)"},
				true
		}
		return agent.HookEventInfo{Installed: false},
			[]string{key + " hook: pdx command not found"},
			true
	}
	// State C — valid.
	return agent.HookEventInfo{Installed: true, Command: command}, nil, false
}

func mergeCodexHooks(path, pdxPath string, remove bool) error {
	hooksFile, err := readCodexHooksFile(path)
	if err != nil {
		return err
	}
	if !remove {
		if err := validateCodexInstallableHookShapes(hooksFile); err != nil {
			return err
		}
	}
	if err := mergeCodexHooksFile(hooksFile, pdxPath, remove); err != nil {
		return err
	}
	return writeCodexHooksFile(path, hooksFile)
}

func validateCodexInstallableHookShapes(hooksFile map[string]any) error {
	hooks, err := codexHooksMapForMerge(hooksFile)
	if err != nil {
		return err
	}
	for _, spec := range codexEventSpecs {
		if !agent.IsInstallableHookSpec(spec) {
			continue
		}
		key := spec.UpstreamKeys[0]
		value, ok := hooks[key]
		if !ok || value == nil {
			continue
		}
		if _, ok := value.([]any); !ok {
			return fmt.Errorf("codex hook %s has unsupported value shape", key)
		}
	}
	return nil
}

func codexHooksMapForMerge(hooksFile map[string]any) (map[string]any, error) {
	h, ok := hooksFile["hooks"]
	if !ok || h == nil {
		return make(map[string]any), nil
	}
	hooks, ok := h.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("codex hooks root has unsupported value shape")
	}
	return hooks, nil
}

func mergeCodexHooksFile(hooksFile map[string]any, pdxPath string, remove bool) error {
	hooks, err := codexHooksMapForMerge(hooksFile)
	if err != nil {
		return err
	}
	if remove {
		for event, existing := range hooks {
			if _, ok := existing.([]any); !ok {
				continue
			}
			entries := filterOutPdxCodexKnownEvents(existing)
			if len(entries) == 0 {
				delete(hooks, event)
			} else {
				hooks[event] = entries
			}
		}
		hooksFile["hooks"] = hooks
		return nil
	}
	for _, spec := range codexEventSpecs {
		installable := agent.IsInstallableHookSpec(spec)
		if !installable {
			continue
		}
		// W2 P2-T3 boundary: hooks.json key is the upstream Codex event name
		// (UpstreamKeys[0]); the per-event command's trailing token is the
		// daemon-internal PurdexName (PdxXxx). Phase 3 ship drops the legacy
		// Name field; codex maps one UpstreamKey per spec so the file shape
		// is unchanged here.
		key := spec.UpstreamKeys[0]
		entries := filterOutPdxCodexKnownEvents(hooks[key])
		entries = append(entries, map[string]any{
			"hooks": []any{
				map[string]any{
					"type":    "command",
					"command": fmt.Sprintf(`"%s" hook --agent codex %s`, pdxPath, spec.PurdexName),
					"timeout": 5,
				},
			},
		})
		hooks[key] = entries
	}
	hooksFile["hooks"] = hooks
	return nil

}

func readCodexHooksFile(path string) (map[string]any, error) {
	hooksFile := make(map[string]any)
	data, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(data, &hooksFile); err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		return hooksFile, nil
	}
	if os.IsNotExist(err) {
		return hooksFile, nil
	}
	return nil, fmt.Errorf("read %s: %w", path, err)
}

func writeCodexHooksFile(path string, hooksFile map[string]any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create directory: %w", err)
	}
	out, err := json.MarshalIndent(hooksFile, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, out, 0644); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

func readCodexConfig(path string) (map[string]any, error) {
	config := make(map[string]any)
	data, err := os.ReadFile(path)
	if err == nil {
		if _, err := toml.Decode(string(data), &config); err != nil {
			return nil, err
		}
		return config, nil
	}
	if os.IsNotExist(err) {
		return config, nil
	}
	return nil, err
}

func writeCodexConfig(path string, config map[string]any) error {
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(config); err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	mode, err := existingFileMode(path, 0600)
	if err != nil {
		return fmt.Errorf("stat config: %w", err)
	}
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, buf.Bytes(), mode); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("rename config: %w", err)
	}
	if err := os.Chmod(path, mode); err != nil {
		return fmt.Errorf("chmod config: %w", err)
	}
	return nil
}

func existingFileMode(path string, defaultMode os.FileMode) (os.FileMode, error) {
	info, err := os.Stat(path)
	if err == nil {
		return info.Mode().Perm(), nil
	}
	if os.IsNotExist(err) {
		return defaultMode, nil
	}
	return 0, err
}

func setCodexHooksFeature(config map[string]any) {
	features, _ := config["features"].(map[string]any)
	if features == nil {
		features = make(map[string]any)
	}
	features["codex_hooks"] = true
	config["features"] = features
}

func codexHooksFeatureEnabled(path string) (bool, error) {
	config, err := readCodexConfig(path)
	if err != nil {
		return false, fmt.Errorf("parse %s: %w", path, err)
	}
	features, _ := config["features"].(map[string]any)
	if features == nil {
		return false, nil
	}
	enabled, _ := features["codex_hooks"].(bool)
	return enabled, nil
}

// isPdxCommandCodex is the relaxed shape check used by filter / legacy
// detection paths where we only need to know "does this look like any pdx
// hook command?" — e.g. when mergeCodexHooks strips all pdx entries before
// rewriting, or when hasLegacyPdxDirectCodexEntry tags the pre-0.121
// direct-entry shape. Per-event health assertions must not use this; see
// isPdxCommandCodexForEvent (PR #616 review Finding #1).
func isPdxCommandCodex(cmd string) bool {
	return isPdxCommandCodexOwned(cmd)
}

// isPdxCommandCodexForEvent reports whether cmd is a well-formed pdx hook
// command for the given event name. Rules (all must hold):
//  1. The first non-empty token, stripped of surrounding quotes, has
//     basename "pdx" (covers "/abs/path/pdx", "pdx", the quoted forms
//     mergeCodexHooks writes, and paths containing spaces such as
//     "/Applications/Purdex Beta/pdx").
//  2. The first subcommand token equals "hook".
//  3. Two consecutive tokens "--agent" "codex" appear after "hook".
//  4. The final non-empty token equals eventName exactly.
//
// A non-pdx third-party command, a pdx command targeting a different
// agent, or a copy of a pdx command whose event-name tail does not
// match the key it is filed under, all return false. Token splitting
// is quote-aware (see tokenizeCodexCommand) so double-quoted paths
// with literal spaces round-trip correctly — the installer emits
// "<pdxPath>" hook --agent codex <event>, where pdxPath may contain
// spaces on macOS app-bundle layouts.
func isPdxCommandCodexForEvent(cmd string, eventName string) bool {
	tokens := tokenizeCodexCommand(cmd)
	if len(tokens) == 0 {
		return false
	}
	first := tokens[0]
	if first == "" {
		return false
	}
	if filepath.Base(first) != "pdx" {
		return false
	}
	hasAgentCodex := false
	if len(tokens) < 2 || tokens[1] != "hook" {
		return false
	}
	for i := 2; i < len(tokens); i++ {
		if tokens[i] == "--agent" && i+1 < len(tokens) && tokens[i+1] == "codex" {
			hasAgentCodex = true
		}
	}
	if !hasAgentCodex {
		return false
	}
	return tokens[len(tokens)-1] == eventName
}

// tokenizeCodexCommand splits a codex hook command string into tokens,
// respecting single and double quotes. Whitespace inside a quoted run
// is preserved; quote characters themselves are stripped. This lets
// the installer's output "<pdxPath>" hook --agent codex <event>
// round-trip correctly when pdxPath contains spaces (e.g. macOS
// "/Applications/Purdex Beta/pdx"). Escape sequences are not
// interpreted — the installer never emits them.
func tokenizeCodexCommand(cmd string) []string {
	var tokens []string
	var cur strings.Builder
	inQuote := false
	var quoteChar byte
	flush := func() {
		if cur.Len() > 0 {
			tokens = append(tokens, cur.String())
			cur.Reset()
		}
	}
	for i := 0; i < len(cmd); i++ {
		c := cmd[i]
		if inQuote {
			if c == quoteChar {
				inQuote = false
				quoteChar = 0
				continue
			}
			cur.WriteByte(c)
			continue
		}
		if c == '"' || c == '\'' {
			inQuote = true
			quoteChar = c
			continue
		}
		if c == ' ' || c == '\t' || c == '\n' {
			flush()
			continue
		}
		cur.WriteByte(c)
	}
	flush()
	return tokens
}

// findPdxCommandInCodexForEvent walks the matcher-group list and returns the
// first command that passes isPdxCommandCodexForEvent for the given event
// name. Empty return means no qualifying command was found → checkCodexEvent
// classifies the event as broken.
func findPdxCommandInCodexForEvent(entries any, eventName string) string {
	for _, groupEntry := range codexMatcherGroups(entries) {
		group, ok := groupEntry.(map[string]any)
		if !ok {
			continue
		}
		for _, entry := range toCodexEntrySlice(group["hooks"]) {
			m, ok := entry.(map[string]any)
			if !ok {
				continue
			}
			cmd, _ := m["command"].(string)
			if isPdxCommandCodexForEvent(cmd, eventName) {
				return cmd
			}
		}
	}
	return ""
}

func toCodexEntrySlice(v any) []any {
	if v == nil {
		return []any{}
	}
	if arr, ok := v.([]any); ok {
		return arr
	}
	return []any{}
}

func codexMatcherGroups(v any) []any {
	var groups []any
	for _, entry := range toCodexEntrySlice(v) {
		m, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if _, ok := m["hooks"]; ok {
			groups = append(groups, m)
		}
	}
	return groups
}

// hasLegacyPdxDirectCodexEntry reports true only when a pre-0.121 direct-entry
// shape ({"type": "command", "command": "...pdx hook..."}) is found. Presence
// of a third-party legacy entry alongside a correctly-installed pdx matcher
// group is not a pdx reinstall trigger and must not report false.
func hasLegacyPdxDirectCodexEntry(v any) bool {
	return hasLegacyPdxDirectCodexEntryOwned(v, codexOwnedCleanupEventNames())
}

func hasLegacyPdxDirectCodexEntryOwned(v any, owned map[string]bool) bool {
	for _, entry := range toCodexEntrySlice(v) {
		m, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if _, hasHooks := m["hooks"]; hasHooks {
			continue
		}
		if _, hasType := m["type"]; !hasType {
			continue
		}
		cmd, _ := m["command"].(string)
		if isPdxCommandCodexOwnedEvent(cmd, owned) {
			return true
		}
	}
	return false
}

func cloneCodexMap(src map[string]any) map[string]any {
	dst := make(map[string]any, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func filterOutPdxCodex(entries any) []any {
	return filterOutPdxCodexWithPredicate(entries, isPdxCommandCodex)
}

func filterOutPdxCodexKnownEvents(entries any) []any {
	owned := codexOwnedCleanupEventNames()
	return filterOutPdxCodexWithPredicate(entries, func(cmd string) bool {
		return isPdxCommandCodexOwnedEvent(cmd, owned)
	})
}

func filterOutPdxCodexWithPredicate(entries any, isOwned func(string) bool) []any {
	var result []any
	for _, entry := range toCodexEntrySlice(entries) {
		group, ok := entry.(map[string]any)
		if !ok {
			result = append(result, entry)
			continue
		}
		if _, ok := group["hooks"]; !ok {
			if _, ok := group["type"]; ok {
				cmd, _ := group["command"].(string)
				if isOwned(cmd) {
					continue
				}
				result = append(result, entry)
				continue
			}
			result = append(result, entry)
			continue
		}
		var kept []any
		hooks, ok := group["hooks"].([]any)
		if !ok {
			result = append(result, entry)
			continue
		}
		for _, hookEntry := range hooks {
			m, ok := hookEntry.(map[string]any)
			if !ok {
				kept = append(kept, hookEntry)
				continue
			}
			cmd, _ := m["command"].(string)
			if !isOwned(cmd) {
				kept = append(kept, hookEntry)
			}
		}
		if len(kept) == 0 {
			continue
		}
		cloned := cloneCodexMap(group)
		cloned["hooks"] = kept
		result = append(result, cloned)
	}
	return result
}

func isPdxCommandCodexKnownEvent(cmd string, known map[string]bool) bool {
	return isPdxCommandCodexOwned(cmd) && known[lastCodexCommandToken(cmd)]
}

func isPdxCommandCodexOwnedEvent(cmd string, owned map[string]bool) bool {
	return isPdxCommandCodexOwned(cmd) && owned[lastCodexCommandToken(cmd)]
}

func isPdxCommandCodexOwned(cmd string) bool {
	tokens := tokenizeCodexCommand(cmd)
	if len(tokens) == 0 || filepath.Base(tokens[0]) != "pdx" {
		return false
	}
	hasAgentCodex := false
	if len(tokens) < 2 || tokens[1] != "hook" {
		return false
	}
	for i := 2; i < len(tokens); i++ {
		if tokens[i] == "--agent" && i+1 < len(tokens) && tokens[i+1] == "codex" {
			hasAgentCodex = true
		}
	}
	return hasAgentCodex
}

func lastCodexCommandToken(cmd string) string {
	tokens := tokenizeCodexCommand(cmd)
	if len(tokens) == 0 {
		return ""
	}
	return tokens[len(tokens)-1]
}

// codexKnownEventNames is the set of installable upstream hook keys derived
// from the catalog (Filter(IsInstallable).UpstreamKeys union). Used for
// upstream-key checks; command-token recognition uses
// codexOwnedCleanupEventNames.
func codexKnownEventNames() map[string]bool {
	known := make(map[string]bool)
	for _, spec := range codexEventSpecs {
		if !agent.IsInstallableHookSpec(spec) {
			continue
		}
		for _, key := range spec.UpstreamKeys {
			known[key] = true
		}
	}
	return known
}

// codexOwnedCleanupEventNames is the three-set union per spec §6.1
// invariant 6: installable specs' UpstreamKeys ∪ PurdexName ∪ legacy Name.
// codex has one-to-one upstream/Pdx mapping so the union collapses to legacy
// Name ∪ PurdexName at runtime. Legacy Name is preserved per plan G1 until
// PR-W2-cleanup-followup so reinstalls following the alpha bump still
// recognise pre-W2 command tokens.
func codexOwnedCleanupEventNames() map[string]bool {
	owned := make(map[string]bool)
	for _, spec := range codexEventSpecs {
		if !agent.IsInstallableHookSpec(spec) {
			continue
		}
		for _, key := range spec.UpstreamKeys {
			owned[key] = true
		}
		owned[spec.PurdexName] = true
		owned[spec.Name] = true
	}
	return owned
}
