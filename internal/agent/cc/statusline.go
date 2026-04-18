package cc

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	shellwords "github.com/mattn/go-shellwords"
)

// StatuslineState describes the current state of CC's statusLine config.
type StatuslineState struct {
	Mode         string `json:"mode"` // "none" | "pdx" | "wrapped" | "unmanaged"
	Installed    bool   `json:"installed"`
	Inner        string `json:"innerCommand,omitempty"`
	RawCommand   string `json:"rawCommand,omitempty"`
	SettingsPath string `json:"settingsPath"`
}

// detectStatuslineMode reads ~/.claude/settings.json and classifies the
// current statusLine.command value.
func detectStatuslineMode(path string) (StatuslineState, error) {
	s := StatuslineState{Mode: "none", SettingsPath: path}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return s, err
	}
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		return s, err
	}
	slRaw, ok := settings["statusLine"]
	if !ok || slRaw == nil {
		return s, nil
	}
	slObj, isObj := slRaw.(map[string]any)
	if !isObj {
		return s, nil // non-object: treat as none (safe overwrite)
	}
	cmdAny, ok := slObj["command"]
	if !ok {
		return s, nil
	}
	cmd, ok := cmdAny.(string)
	if !ok || strings.TrimSpace(cmd) == "" {
		return s, nil
	}

	s.Installed = true
	s.RawCommand = cmd

	argv, err := shellwords.Parse(cmd)
	if err != nil || len(argv) < 2 {
		s.Mode = "unmanaged"
		s.Inner = cmd
		return s, nil
	}
	base := filepath.Base(argv[0])
	if base != "pdx" && base != "pdx.exe" {
		s.Mode = "unmanaged"
		s.Inner = cmd
		return s, nil
	}
	if argv[1] != "statusline-proxy" {
		s.Mode = "unmanaged"
		s.Inner = cmd
		return s, nil
	}
	switch {
	case len(argv) == 2:
		s.Mode = "pdx"
	case len(argv) >= 4 && argv[2] == "--inner":
		s.Mode = "wrapped"
		s.Inner = argv[3]
	default:
		s.Mode = "unmanaged"
		s.Inner = cmd
	}
	return s, nil
}

// shellSingleQuote returns a POSIX-safe single-quoted form of s that round-trips
// through `sh -c`. Embedded ' characters become '\''.
func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// writeSettingsAtomic marshals settings as JSON and writes to path via temp+rename.
func writeSettingsAtomic(path string, settings map[string]any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, out, 0644); err != nil {
		return fmt.Errorf("write temp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

func loadSettings(path string) (map[string]any, error) {
	settings := make(map[string]any)
	data, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(data, &settings); err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	return settings, nil
}

func installStatuslinePdx(path, pdxPath string) error {
	settings, err := loadSettings(path)
	if err != nil {
		return err
	}
	settings["statusLine"] = map[string]any{
		"type":    "command",
		"command": fmt.Sprintf("%s statusline-proxy", pdxPath),
	}
	return writeSettingsAtomic(path, settings)
}

func installStatuslineWrap(path, pdxPath, inner string) error {
	settings, err := loadSettings(path)
	if err != nil {
		return err
	}
	settings["statusLine"] = map[string]any{
		"type":    "command",
		"command": fmt.Sprintf("%s statusline-proxy --inner %s", pdxPath, shellSingleQuote(inner)),
	}
	return writeSettingsAtomic(path, settings)
}

func removeStatusline(path string) error {
	state, err := detectStatuslineMode(path)
	if err != nil {
		return err
	}
	settings, err := loadSettings(path)
	if err != nil {
		return err
	}
	switch state.Mode {
	case "none":
		return nil
	case "unmanaged":
		return fmt.Errorf("refusing to remove unmanaged statusLine; please remove manually")
	case "pdx":
		delete(settings, "statusLine")
	case "wrapped":
		sl, _ := settings["statusLine"].(map[string]any)
		if sl == nil {
			sl = map[string]any{"type": "command"}
		}
		sl["command"] = state.Inner
		settings["statusLine"] = sl
	}
	return writeSettingsAtomic(path, settings)
}
