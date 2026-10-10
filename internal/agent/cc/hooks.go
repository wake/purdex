package cc

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/buildinfo"
	"github.com/wake/purdex/internal/config"
)

const ccHooksSupportedVersion = "2.1.114"

// The two settings.json writers InstallHooks and RemoveHooks run; tests
// swap them to fail one step.
var (
	mergeHooksFn = mergeClaudeHooks
	mergeEnvFn   = mergePluginDirs
)

// InstallHooks runs plugin extraction → plugin env → hooks, so a failure
// never leaves hooks installed without their plugin; a failed hooks write
// takes the env entry back out before returning its error.
func (p *Provider) InstallHooks(pdxPath string) error {
	settingsPath, err := ccSettingsPath()
	if err != nil {
		return fmt.Errorf("cannot determine home directory: %w", err)
	}
	// The env value as it was, so a failed hooks write puts back exactly that
	// (critic on PR #1752): a still-working entry of an older install must not
	// be lost because this install failed half way.
	prevDirs, prevPresent, snapErr := readPluginDirs(settingsPath)
	root, err := p.installPlugin(settingsPath, pdxPath)
	if err != nil {
		return err
	}
	if err := mergeHooksFn(settingsPath, pdxPath, false); err != nil {
		if root != "" {
			var rerr error
			if snapErr == nil {
				rerr = restorePluginDirs(settingsPath, prevDirs, prevPresent)
			} else {
				rerr = mergeEnvFn(settingsPath, root, true)
			}
			if rerr != nil {
				return errors.Join(err, fmt.Errorf("undo plugin env: %w", rerr))
			}
		}
		return err
	}
	return nil
}

// RemoveHooks tries all three steps (hooks, plugin env, plugin dir) even
// when one fails, and returns their errors joined.
func (p *Provider) RemoveHooks(pdxPath string) error {
	settingsPath, err := ccSettingsPath()
	if err != nil {
		return fmt.Errorf("cannot determine home directory: %w", err)
	}
	hooksErr := mergeHooksFn(settingsPath, pdxPath, true)
	return errors.Join(hooksErr, p.removePlugin(settingsPath))
}

// installTarget is the data dir and config file the plugin is installed
// for: the daemon's when the provider has a config (the daemon's own
// provider, module.go:242), else the default config's
// ($HOME/.config/pdx[/config.toml]) — the case of `pdx setup` without a
// daemon (cmd/pdx/setup.go:114 builds the provider with nil deps).
func (p *Provider) installTarget() (dataDir, cfgPath string) {
	if p.cfg != nil {
		if p.cfgMu != nil {
			p.cfgMu.RLock()
		}
		dataDir, cfgPath = p.cfg.DataDir, p.cfg.Path
		if p.cfgMu != nil {
			p.cfgMu.RUnlock()
		}
	}
	if dataDir == "" || cfgPath == "" {
		def, _ := config.Load("")
		if dataDir == "" {
			dataDir = def.DataDir
		}
		if cfgPath == "" {
			cfgPath = def.Path
		}
	}
	return dataDir, cfgPath
}

func (p *Provider) dataDir() string {
	dataDir, _ := p.installTarget()
	return dataDir
}

// installPlugin extracts the embedded plugin (spec §5 "Shipping") and names
// it in settings.json env, returning the root it named. Without an embedded
// tree it does nothing and returns "", so the hook installer's own tests are
// unaffected.
func (p *Provider) installPlugin(settingsPath, pdxPath string) (string, error) {
	if PluginSource == nil {
		return "", nil
	}
	dataDir, cfgPath := p.installTarget()
	root, _, err := ExtractPlugin(PluginSource, dataDir, buildinfo.Version, pdxPath, cfgPath)
	if err != nil {
		return "", fmt.Errorf("extract plugin: %w", err)
	}
	if err := mergeEnvFn(settingsPath, root, false); err != nil {
		return "", err
	}
	return root, nil
}

// removePlugin takes the plugin out of settings.json env and deletes the
// extracted folder; both are tried, the errors joined.
func (p *Provider) removePlugin(settingsPath string) error {
	dataDir := p.dataDir()
	envErr := mergeEnvFn(settingsPath, PluginRoot(dataDir), true)
	return errors.Join(envErr, RemovePluginDir(dataDir))
}

func (p *Provider) CheckHooks() (agent.HookStatus, error) {
	settingsPath, err := ccSettingsPath()
	if err != nil {
		return agent.HookStatus{Issues: []string{"cannot find home dir"}}, err
	}
	agentVersion := agent.DetectHookAgentVersion("claude", "--version")
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		return agent.HookStatus{
			Installed:        false,
			Events:           map[string]agent.HookEventInfo{},
			Issues:           []string{"settings.json not found"},
			AgentVersion:     agentVersion,
			SupportedVersion: ccHooksSupportedVersion,
			ExceedsSupport:   agent.CompareHookAgentVersions(agentVersion, ccHooksSupportedVersion) > 0,
		}, nil
	}
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		return agent.HookStatus{}, fmt.Errorf("parse settings.json: %w", err)
	}
	hooks, _ := settings["hooks"].(map[string]any)
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
		key := spec.UpstreamKeys[0]
		entries, keyExists := hooks[key]
		if !keyExists {
			events[spec.PurdexName] = agent.HookEventInfo{Installed: false, FutureOnly: spec.FutureOnly}
			if spec.FutureOnly {
				upgrades = append(upgrades, spec.PurdexName)
				continue
			}
			issues = append(issues, spec.PurdexName+" hook not installed")
			allInstalled = false
			continue
		}
		command := findPdxCommandForEvent(entries, spec.PurdexName)
		events[spec.PurdexName] = agent.HookEventInfo{
			Installed:  command != "",
			Command:    command,
			FutureOnly: spec.FutureOnly,
		}
		if command == "" {
			issues = append(issues, spec.PurdexName+" hook: pdx command not found")
			allInstalled = false
		}
	}
	// With a plugin to ship, the hooks alone are not "installed": a user who
	// installed before the plugin existed (or before this version's) must
	// see Install again, or the mod never reaches their sessions.
	if PluginSource != nil {
		if issue := pluginIssue(settings, p.dataDir()); issue != "" {
			issues = append(issues, issue)
			allInstalled = false
		}
	}
	managed := ccHooksManaged(hooks, allSpecs)
	return agent.HookStatus{
		Installed:         allInstalled,
		Managed:           managed,
		UpgradesAvailable: upgrades,
		Events:            events,
		Issues:            issues,
		AgentVersion:      agentVersion,
		SupportedVersion:  ccHooksSupportedVersion,
		ExceedsSupport:    agent.CompareHookAgentVersions(agentVersion, ccHooksSupportedVersion) > 0,
	}, nil
}

// pluginIssue says why the Purdex plugin is not usable as installed, or ""
// when it is: settings env CLAUDE_CODE_PLUGIN_DIRS must name
// PluginRoot(dataDir), and the tree there must hold hooks/register.js and a
// VERSION, and equal the embedded tree (treeIdentical; without a PluginSource, a VERSION equal to this binary's). A dev
// build, "unknown", accepts any.
func pluginIssue(settings map[string]any, dataDir string) string {
	const notInstalled = "Purdex plugin not installed"
	root := PluginRoot(dataDir)
	env, _ := settings["env"].(map[string]any)
	dirs, _ := env[pluginDirsEnv].(string)
	listed := false
	for _, d := range strings.Split(dirs, string(os.PathListSeparator)) {
		if d != "" && filepath.Clean(d) == root {
			listed = true
			break
		}
	}
	if !listed {
		return notInstalled
	}
	if _, err := os.Stat(filepath.Join(root, "hooks", "register.js")); err != nil {
		return notInstalled
	}
	b, err := os.ReadFile(filepath.Join(root, "VERSION"))
	if err != nil {
		return notInstalled
	}
	got, want := strings.TrimSpace(string(b)), buildinfo.Version
	if want == "" || want == "unknown" {
		return ""
	}
	if got == "" { // a truncated stamp is damage, not a version
		return notInstalled
	}
	// With the embedded tree to compare, the mod is outdated when its CONTENT differs — the same test ExtractPlugin uses
	// to decide a swap (#2403): VERSION is the daemon version the tree last changed at, not this binary's.
	if PluginSource != nil {
		if !treeIdentical(PluginSource, root) {
			return fmt.Sprintf("Purdex plugin outdated (installed %s, the mod's files differ from this version's)", got)
		}
		return ""
	}
	if got != want {
		return fmt.Sprintf("Purdex plugin outdated (installed %s, want %s)", got, want)
	}
	return ""
}

// ccHooksManaged reports whether settings.json has any pdx-owned hook
// entry across any configured hook key. Parallel to codex.codexHooksManaged
// — keeps Managed/Installed distinct so the UI Remove button stays
// enabled on drifted-but-managed state (Finding #2).
func ccHooksManaged(hooks map[string]any, specs []agent.HookEventSpec) bool {
	owned := ccOwnedCleanupEventNames()
	for _, entries := range hooks {
		for _, entry := range toEntrySlice(entries) {
			if entryIsPdxCCKnownEvent(entry, owned) {
				return true
			}
		}
	}
	return false
}

func mergeClaudeHooks(path, pdxPath string, remove bool) error {
	settings := make(map[string]any)
	data, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(data, &settings); err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("read %s: %w", path, err)
	}
	hooks, err := claudeHooksMapForMerge(settings)
	if err != nil {
		return err
	}
	if remove {
		for event, existing := range hooks {
			entries, ok := existing.([]any)
			if !ok {
				continue
			}
			entries = filterOutPdx(entries)
			if len(entries) == 0 {
				delete(hooks, event)
			} else {
				hooks[event] = entries
			}
		}
		settings["hooks"] = hooks
		return writeClaudeSettings(path, settings)
	}
	if err := validateClaudeInstallableHookShapes(hooks); err != nil {
		return err
	}
	for _, spec := range ccEventSpecs {
		installable := agent.IsInstallableHookSpec(spec)
		if !installable {
			continue
		}
		key := spec.UpstreamKeys[0]
		entries := toEntrySlice(hooks[key])
		entries = filterOutPdx(entries)
		entries = append(entries, makePdxEntry(pdxPath, "cc", spec.PurdexName))
		hooks[key] = entries
	}
	settings["hooks"] = hooks
	return writeClaudeSettings(path, settings)
}

func claudeHooksMapForMerge(settings map[string]any) (map[string]any, error) {
	h, ok := settings["hooks"]
	if !ok || h == nil {
		return make(map[string]any), nil
	}
	hooks, ok := h.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("claude hooks root has unsupported value shape")
	}
	return hooks, nil
}

func validateClaudeInstallableHookShapes(hooks map[string]any) error {
	for _, spec := range ccEventSpecs {
		if !agent.IsInstallableHookSpec(spec) {
			continue
		}
		key := spec.UpstreamKeys[0]
		value, ok := hooks[key]
		if !ok || value == nil {
			continue
		}
		if _, ok := value.([]any); !ok {
			return fmt.Errorf("claude hook %s has unsupported value shape", key)
		}
	}
	return nil
}

func writeClaudeSettings(path string, settings map[string]any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create directory: %w", err)
	}
	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal settings: %w", err)
	}
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, out, 0644); err != nil {
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

func makePdxEntry(pdxPath, agentType, event string) map[string]any {
	return map[string]any{
		"hooks": []any{
			map[string]any{
				"type":    "command",
				"command": fmt.Sprintf(`"%s" hook --agent %s %s`, pdxPath, agentType, event),
			},
		},
	}
}

func findPdxCommand(entries any) string {
	return findPdxCommandForEvent(entries, "")
}

func findPdxCommandForEvent(entries any, eventName string) string {
	arr, ok := entries.([]any)
	if !ok {
		return ""
	}
	for _, entry := range arr {
		entryMap, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		hooksList, ok := entryMap["hooks"].([]any)
		if !ok {
			continue
		}
		for _, h := range hooksList {
			hookMap, ok := h.(map[string]any)
			if !ok {
				continue
			}
			cmd, _ := hookMap["command"].(string)
			if eventName != "" && isPdxCommandForCCEvent(cmd, eventName) {
				return cmd
			}
			if eventName == "" && isPdxCommand(cmd) {
				return cmd
			}
		}
	}
	return ""
}

func toEntrySlice(v any) []any {
	if v == nil {
		return []any{}
	}
	if arr, ok := v.([]any); ok {
		return arr
	}
	return []any{}
}

func filterOutPdx(entries []any) []any {
	return filterOutPdxKnownCCEvents(entries)
}

func filterOutPdxKnownCCEvents(entries []any) []any {
	known := ccOwnedCleanupEventNames()
	result := []any{}
	for _, e := range entries {
		if !entryIsPdxCCKnownEvent(e, known) {
			result = append(result, e)
		}
	}
	return result
}

func entryIsPdx(entry any) bool {
	return entryIsPdxCCKnownEvent(entry, ccOwnedCleanupEventNames())
}

func entryIsPdxCCKnownEvent(entry any, known map[string]bool) bool {
	m, ok := entry.(map[string]any)
	if !ok {
		return false
	}
	innerHooks, ok := m["hooks"]
	if !ok {
		return false
	}
	arr, ok := innerHooks.([]any)
	if !ok {
		return false
	}
	for _, h := range arr {
		hookObj, ok := h.(map[string]any)
		if !ok {
			continue
		}
		cmd, ok := hookObj["command"].(string)
		if !ok {
			continue
		}
		if isPdxCommandCCKnownEvent(cmd, known) {
			return true
		}
	}
	return false
}

// isPdxCommand reports whether cmd is a pdx-installed hook command. It
// matches the cleanup set (UpstreamKeys ∪ PurdexName ∪ legacy Name three-way
// union) so both pre-W2 and W2 command tokens are recognised; ccKnownEventNames
// is reserved for upstream-key-only checks.
func isPdxCommand(cmd string) bool {
	return isPdxCommandCCKnownEvent(cmd, ccOwnedCleanupEventNames())
}

func isPdxCommandForCCEvent(cmd string, eventName string) bool {
	return isPdxCommandCC(cmd, func(got string) bool { return got == eventName })
}

func isPdxCommandCCKnownEvent(cmd string, known map[string]bool) bool {
	return isPdxCommandCC(cmd, func(eventName string) bool { return known[eventName] })
}

func isPdxCommandCC(cmd string, eventOK func(string) bool) bool {
	tokens := tokenizeCCCommand(cmd)
	if len(tokens) == 0 || filepath.Base(tokens[0]) != "pdx" {
		return false
	}
	hasAgentCC := false
	if len(tokens) < 2 || tokens[1] != "hook" {
		return false
	}
	for i := 2; i < len(tokens); i++ {
		if tokens[i] == "--agent" && i+1 < len(tokens) && tokens[i+1] == "cc" {
			hasAgentCC = true
		}
	}
	return hasAgentCC && eventOK(tokens[len(tokens)-1])
}

func tokenizeCCCommand(cmd string) []string {
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

// ccKnownEventNames is the set of installable upstream hook keys derived
// from the catalog (Filter(IsInstallable).UpstreamKeys union). Used for
// upstream-key checks; command-token recognition uses ccOwnedCleanupEventNames.
func ccKnownEventNames() map[string]bool {
	known := make(map[string]bool)
	for _, spec := range ccEventSpecs {
		if !agent.IsInstallableHookSpec(spec) {
			continue
		}
		for _, key := range spec.UpstreamKeys {
			known[key] = true
		}
	}
	return known
}

// ccOwnedCleanupEventNames is the two-set union per spec §6.1 invariant 6
// post-cleanup: installable specs' UpstreamKeys ∪ PurdexName. cc has
// one-to-one upstream/Pdx mapping so pre-W2 command-tail tokens (e.g.
// `Stop`) are still recognised via the UpstreamKey leg; the redundant
// legacy Name set retired in PR-W2-cleanup-followup (plan §5.3 CLEANUP-T1)
// once W2 alpha.255 shipped and user reinstall completed.
func ccOwnedCleanupEventNames() map[string]bool {
	owned := make(map[string]bool)
	for _, spec := range ccEventSpecs {
		if !agent.IsInstallableHookSpec(spec) {
			continue
		}
		for _, key := range spec.UpstreamKeys {
			owned[key] = true
		}
		owned[spec.PurdexName] = true
	}
	return owned
}
