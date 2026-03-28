package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// hookEvents lists all CC hook events that tbox registers.
var hookEvents = []string{
	"SessionStart",
	"UserPromptSubmit",
	"Stop",
	"Notification",
	"PermissionRequest",
	"SessionEnd",
}

// runSetup is the entry point for `tbox setup` and `tbox setup --remove`.
func runSetup(args []string) {
	remove := false
	for _, a := range args {
		if a == "--remove" {
			remove = true
		}
	}

	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "setup: cannot determine home directory: %v\n", err)
		os.Exit(1)
	}
	settingsPath := filepath.Join(home, ".claude", "settings.json")

	// Resolve absolute path to tbox executable
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "setup: cannot determine executable path: %v\n", err)
		os.Exit(1)
	}
	tboxPath, err := filepath.EvalSymlinks(exe)
	if err != nil {
		fmt.Fprintf(os.Stderr, "setup: cannot resolve executable symlink: %v\n", err)
		os.Exit(1)
	}

	if err := mergeHooks(settingsPath, tboxPath, remove); err != nil {
		fmt.Fprintf(os.Stderr, "setup: %v\n", err)
		os.Exit(1)
	}

	if remove {
		fmt.Println("tbox hooks removed from", settingsPath)
	} else {
		fmt.Println("tbox hooks installed to", settingsPath)
	}
	fmt.Println("Please restart Claude Code for changes to take effect.")
}

// mergeHooks reads the settings file at path, adds or removes tbox hook entries,
// and writes the result back. If the file does not exist, it creates a new one.
func mergeHooks(path, tboxPath string, remove bool) error {
	settings := make(map[string]any)

	data, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(data, &settings); err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("read %s: %w", path, err)
	}

	// Get or create "hooks" object
	var hooks map[string]any
	if h, ok := settings["hooks"]; ok {
		hooks, ok = h.(map[string]any)
		if !ok {
			hooks = make(map[string]any)
		}
	} else {
		hooks = make(map[string]any)
	}

	for _, event := range hookEvents {
		entries := toEntrySlice(hooks[event])

		if remove {
			entries = filterOutTbox(entries, tboxPath)
		} else {
			if !hasTboxEntry(entries, tboxPath) {
				entries = append(entries, makeTboxEntry(tboxPath, event))
			}
		}

		hooks[event] = entries
	}

	settings["hooks"] = hooks

	// Ensure parent directory exists
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create directory: %w", err)
	}

	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal settings: %w", err)
	}

	return os.WriteFile(path, out, 0644)
}

// makeTboxEntry creates a hook entry for the given event.
func makeTboxEntry(tboxPath, event string) map[string]any {
	return map[string]any{
		"hooks": []any{
			map[string]any{
				"type":    "command",
				"command": tboxPath + " hook " + event,
			},
		},
	}
}

// toEntrySlice safely converts an interface value to []any.
// Returns an empty slice if v is nil or not a slice.
func toEntrySlice(v any) []any {
	if v == nil {
		return []any{}
	}
	if arr, ok := v.([]any); ok {
		return arr
	}
	return []any{}
}

// hasTboxEntry checks if any entry in the slice contains a tbox command.
func hasTboxEntry(entries []any, tboxPath string) bool {
	for _, e := range entries {
		if entryMatchesTbox(e, tboxPath) {
			return true
		}
	}
	return false
}

// filterOutTbox returns entries with tbox entries removed.
// Always returns a non-nil slice (empty []any{} when all removed).
func filterOutTbox(entries []any, tboxPath string) []any {
	result := []any{}
	for _, e := range entries {
		if !entryMatchesTbox(e, tboxPath) {
			result = append(result, e)
		}
	}
	return result
}

// entryMatchesTbox checks if an entry's hooks[].command contains tboxPath.
func entryMatchesTbox(entry any, tboxPath string) bool {
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
		if strings.Contains(cmd, tboxPath) {
			return true
		}
	}
	return false
}
