package cc

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/wake/purdex/internal/agent"
)

const ccHooksSupportedVersion = "2.1.114"

func (p *Provider) InstallHooks(pdxPath string) error {
	settingsPath, err := ccSettingsPath()
	if err != nil {
		return fmt.Errorf("cannot determine home directory: %w", err)
	}
	return mergeClaudeHooks(settingsPath, pdxPath, false)
}

func (p *Provider) RemoveHooks(pdxPath string) error {
	settingsPath, err := ccSettingsPath()
	if err != nil {
		return fmt.Errorf("cannot determine home directory: %w", err)
	}
	return mergeClaudeHooks(settingsPath, pdxPath, true)
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
		entries, keyExists := hooks[spec.Name]
		if !keyExists {
			events[spec.Name] = agent.HookEventInfo{Installed: false, FutureOnly: spec.FutureOnly}
			if spec.FutureOnly {
				upgrades = append(upgrades, spec.Name)
				continue
			}
			issues = append(issues, spec.Name+" hook not installed")
			allInstalled = false
			continue
		}
		command := findPdxCommandForEvent(entries, spec.Name)
		events[spec.Name] = agent.HookEventInfo{
			Installed:  command != "",
			Command:    command,
			FutureOnly: spec.FutureOnly,
		}
		if command == "" {
			issues = append(issues, spec.Name+" hook: pdx command not found")
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
	var hooks map[string]any
	if h, ok := settings["hooks"]; ok {
		hooks, _ = h.(map[string]any)
	}
	if hooks == nil {
		hooks = make(map[string]any)
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
	for _, spec := range ccEventSpecs {
		installable := agent.IsInstallableHookSpec(spec)
		if !installable {
			continue
		}
		event := spec.Name
		entries := toEntrySlice(hooks[event])
		entries = filterOutPdx(entries)
		entries = append(entries, makePdxEntry(pdxPath, "cc", event))
		hooks[event] = entries
	}
	settings["hooks"] = hooks
	return writeClaudeSettings(path, settings)
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

func isPdxCommand(cmd string) bool {
	return isPdxCommandCCKnownEvent(cmd, ccKnownEventNames())
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

func ccKnownEventNames() map[string]bool {
	known := make(map[string]bool, len(ccEventSpecs))
	for _, spec := range ccEventSpecs {
		known[spec.Name] = true
	}
	return known
}

func ccOwnedCleanupEventNames() map[string]bool {
	return map[string]bool{
		"SessionStart":      true,
		"UserPromptSubmit":  true,
		"SubagentStart":     true,
		"SubagentStop":      true,
		"Stop":              true,
		"StopFailure":       true,
		"Notification":      true,
		"PermissionRequest": true,
		"SessionEnd":        true,
	}
}
