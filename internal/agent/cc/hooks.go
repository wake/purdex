package cc

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/wake/purdex/internal/agent"
)

var ccHookEvents = []string{
	"SessionStart", "UserPromptSubmit", "SubagentStart", "SubagentStop",
	"Stop", "StopFailure", "Notification", "PermissionRequest", "SessionEnd",
}

func (p *Provider) InstallHooks(pdxPath string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("cannot determine home directory: %w", err)
	}
	settingsPath := filepath.Join(home, ".claude", "settings.json")
	return mergeClaudeHooks(settingsPath, pdxPath, false)
}

func (p *Provider) RemoveHooks(pdxPath string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("cannot determine home directory: %w", err)
	}
	settingsPath := filepath.Join(home, ".claude", "settings.json")
	return mergeClaudeHooks(settingsPath, pdxPath, true)
}

func (p *Provider) CheckHooks() (agent.HookStatus, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return agent.HookStatus{Issues: []string{"cannot find home dir"}}, err
	}
	settingsPath := filepath.Join(home, ".claude", "settings.json")
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		return agent.HookStatus{
			Installed: false,
			Events:    map[string]agent.HookEventInfo{},
			Issues:    []string{"settings.json not found"},
		}, nil
	}
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		return agent.HookStatus{}, fmt.Errorf("parse settings.json: %w", err)
	}
	hooks, _ := settings["hooks"].(map[string]any)
	events := make(map[string]agent.HookEventInfo, len(ccHookEvents))
	var issues []string
	allInstalled := true
	for _, eventName := range ccHookEvents {
		entries, ok := hooks[eventName]
		if !ok {
			events[eventName] = agent.HookEventInfo{Installed: false}
			issues = append(issues, eventName+" hook not installed")
			allInstalled = false
			continue
		}
		command := findPdxCommand(entries)
		events[eventName] = agent.HookEventInfo{Installed: command != "", Command: command}
		if command == "" {
			issues = append(issues, eventName+" hook: pdx command not found")
			allInstalled = false
		}
	}
	return agent.HookStatus{Installed: allInstalled, Events: events, Issues: issues}, nil
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
	for _, event := range ccHookEvents {
		entries := toEntrySlice(hooks[event])
		entries = filterOutPdx(entries)
		if !remove {
			entries = append(entries, makePdxEntry(pdxPath, "cc", event))
		}
		hooks[event] = entries
	}
	settings["hooks"] = hooks
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
			if strings.Contains(strings.ReplaceAll(cmd, `"`, ""), "pdx hook") {
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
	result := []any{}
	for _, e := range entries {
		if !entryIsPdx(e) {
			result = append(result, e)
		}
	}
	return result
}

func entryIsPdx(entry any) bool {
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
		if isPdxCommand(cmd) {
			return true
		}
	}
	return false
}

func isPdxCommand(cmd string) bool {
	if strings.Contains(cmd, `/pdx" hook`) || strings.HasPrefix(cmd, `"pdx" hook`) {
		return true
	}
	if strings.Contains(cmd, `/pdx hook`) || strings.HasPrefix(cmd, `pdx hook`) {
		return true
	}
	// Legacy: also match old tbox binary for migration cleanup
	if strings.Contains(cmd, `/tbox" hook`) || strings.HasPrefix(cmd, `"tbox" hook`) {
		return true
	}
	if strings.Contains(cmd, `/tbox hook`) || strings.HasPrefix(cmd, `tbox hook`) {
		return true
	}
	return false
}
