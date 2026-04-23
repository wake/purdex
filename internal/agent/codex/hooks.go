package codex

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/wake/purdex/internal/agent"
)

const codexHooksSupportedVersion = "0.121.0"

func (p *Provider) InstallHooks(pdxPath string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("cannot determine home directory: %w", err)
	}
	hooksPath := filepath.Join(home, ".codex", "hooks.json")
	return mergeCodexHooks(hooksPath, pdxPath, false)
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
	events := make(map[string]agent.HookEventInfo, len(codexEventSpecs))
	var issues []string
	allInstalled := true
	for _, spec := range p.Events() {
		info, addIssues, blocks := checkCodexEvent(spec, hooks)
		events[spec.Name] = info
		issues = append(issues, addIssues...)
		if blocks {
			allInstalled = false
		}
	}
	return agent.HookStatus{
		Installed:        allInstalled,
		Events:           events,
		Issues:           issues,
		AgentVersion:     agentVersion,
		SupportedVersion: codexHooksSupportedVersion,
		ExceedsSupport:   agent.CompareHookAgentVersions(agentVersion, codexHooksSupportedVersion) > 0,
	}, nil
}

// checkCodexEvent classifies a single hook event as absent / broken / valid
// per fix-plan §1.4 and applies the FutureOnly-aware decision table. It
// takes the full hooks map so absent vs present-but-empty can be told apart
// via the double-return form `entries, ok := hooks[spec.Name]` — a value of
// `[]any{}` (what mergeCodexHooks leaves behind when the last pdx entry is
// removed) must be classified as broken, not absent.
//
// Returns:
//   - HookEventInfo for events[spec.Name]
//   - zero or more Issue strings to append
//   - whether this event blocks allInstalled
func checkCodexEvent(spec agent.HookEventSpec, hooks map[string]any) (agent.HookEventInfo, []string, bool) {
	entries, keyExists := hooks[spec.Name]
	// State A — absent (strict: key not present in map).
	if !keyExists {
		info := agent.HookEventInfo{Installed: false}
		if spec.FutureOnly {
			// Tolerated legacy: no issue, does not block.
			return info, nil, false
		}
		return info, []string{spec.Name + " hook not installed"}, true
	}
	// Legacy direct-entry shape is its own signal (present but uses the
	// pre-0.121 shape pdx installer no longer writes).
	if hasLegacyPdxDirectCodexEntry(entries) {
		return agent.HookEventInfo{Installed: false},
			[]string{spec.Name + " hook uses legacy format; reinstall required"},
			true
	}
	// Find a pdx command inside the matcher-group list. Empty entries, wrong
	// shape, or no pdx command all collapse to command == "" here → State B.
	command := findPdxCommandInCodex(entries)
	if command == "" {
		if spec.FutureOnly {
			return agent.HookEventInfo{Installed: false},
				[]string{spec.Name + " hook: pdx command malformed (FutureOnly event has existing hook entry but pdx path incorrect — run install to repair)"},
				true
		}
		return agent.HookEventInfo{Installed: false},
			[]string{spec.Name + " hook: pdx command not found"},
			true
	}
	// State C — valid.
	return agent.HookEventInfo{Installed: true, Command: command}, nil, false
}

func mergeCodexHooks(path, pdxPath string, remove bool) error {
	hooksFile := make(map[string]any)
	data, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(data, &hooksFile); err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("read %s: %w", path, err)
	}
	var hooks map[string]any
	if h, ok := hooksFile["hooks"]; ok {
		hooks, _ = h.(map[string]any)
	}
	if hooks == nil {
		hooks = make(map[string]any)
	}
	for _, event := range codexEventNames() {
		entries := filterOutPdxCodex(hooks[event])
		if !remove {
			entries = append(entries, map[string]any{
				"hooks": []any{
					map[string]any{
						"type":    "command",
						"command": fmt.Sprintf(`"%s" hook --agent codex %s`, pdxPath, event),
						"timeout": 5,
					},
				},
			})
		}
		hooks[event] = entries
	}
	hooksFile["hooks"] = hooks
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

func isPdxCommandCodex(cmd string) bool {
	// Match both quoted ("/path/pdx" hook) and unquoted (/path/pdx hook) forms.
	normalized := strings.ReplaceAll(cmd, `"`, "")
	return strings.Contains(normalized, "pdx hook")
}

func findPdxCommandInCodex(entries any) string {
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
			if isPdxCommandCodex(cmd) {
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
		if isPdxCommandCodex(cmd) {
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
				if isPdxCommandCodex(cmd) {
					continue
				}
				result = append(result, map[string]any{
					"hooks": []any{cloneCodexMap(group)},
				})
				continue
			}
			result = append(result, entry)
			continue
		}
		var kept []any
		for _, hookEntry := range toCodexEntrySlice(group["hooks"]) {
			m, ok := hookEntry.(map[string]any)
			if !ok {
				kept = append(kept, hookEntry)
				continue
			}
			cmd, _ := m["command"].(string)
			if !isPdxCommandCodex(cmd) {
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
