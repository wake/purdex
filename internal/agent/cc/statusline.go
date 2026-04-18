package cc

import (
	"encoding/json"
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
