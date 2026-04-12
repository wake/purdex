package codex

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/wake/purdex/internal/agent"
)

var codexHookEvents = []string{
	"SessionStart",
	"UserPromptSubmit",
	"Stop",
}

func (p *Provider) InstallHooks(tboxPath string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("cannot determine home directory: %w", err)
	}
	hooksPath := filepath.Join(home, ".codex", "hooks.json")
	return mergeCodexHooks(hooksPath, tboxPath, false)
}

func (p *Provider) RemoveHooks(tboxPath string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("cannot determine home directory: %w", err)
	}
	hooksPath := filepath.Join(home, ".codex", "hooks.json")
	return mergeCodexHooks(hooksPath, tboxPath, true)
}

func (p *Provider) CheckHooks() (agent.HookStatus, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return agent.HookStatus{Issues: []string{"cannot find home dir"}}, err
	}
	hooksPath := filepath.Join(home, ".codex", "hooks.json")
	data, err := os.ReadFile(hooksPath)
	if err != nil {
		return agent.HookStatus{
			Installed: false,
			Events:    map[string]agent.HookEventInfo{},
			Issues:    []string{"hooks.json not found"},
		}, nil
	}
	var hooksFile map[string]any
	if err := json.Unmarshal(data, &hooksFile); err != nil {
		return agent.HookStatus{}, fmt.Errorf("parse hooks.json: %w", err)
	}
	hooks, _ := hooksFile["hooks"].(map[string]any)
	events := make(map[string]agent.HookEventInfo, len(codexHookEvents))
	var issues []string
	allInstalled := true
	for _, eventName := range codexHookEvents {
		entries, ok := hooks[eventName]
		if !ok {
			events[eventName] = agent.HookEventInfo{Installed: false}
			issues = append(issues, eventName+" hook not installed")
			allInstalled = false
			continue
		}
		command := findTboxCommandInCodex(entries)
		events[eventName] = agent.HookEventInfo{Installed: command != "", Command: command}
		if command == "" {
			issues = append(issues, eventName+" hook: tbox command not found")
			allInstalled = false
		}
	}
	return agent.HookStatus{Installed: allInstalled, Events: events, Issues: issues}, nil
}

func mergeCodexHooks(path, tboxPath string, remove bool) error {
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
	for _, event := range codexHookEvents {
		entries := toCodexEntrySlice(hooks[event])
		entries = filterOutTboxCodex(entries)
		if !remove {
			entries = append(entries, map[string]any{
				"type":    "command",
				"command": fmt.Sprintf(`"%s" hook --agent codex %s`, tboxPath, event),
				"timeout": 5,
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

func isTboxCommandCodex(cmd string) bool {
	// Match both quoted ("/path/tbox" hook) and unquoted (/path/tbox hook) forms.
	normalized := strings.ReplaceAll(cmd, `"`, "")
	return strings.Contains(normalized, "tbox hook")
}

func findTboxCommandInCodex(entries any) string {
	arr, ok := entries.([]any)
	if !ok {
		return ""
	}
	for _, entry := range arr {
		m, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		cmd, _ := m["command"].(string)
		if isTboxCommandCodex(cmd) {
			return cmd
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

func filterOutTboxCodex(entries []any) []any {
	var result []any
	for _, e := range entries {
		m, ok := e.(map[string]any)
		if !ok {
			result = append(result, e)
			continue
		}
		cmd, _ := m["command"].(string)
		if !isTboxCommandCodex(cmd) {
			result = append(result, e)
		}
	}
	return result
}
