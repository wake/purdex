package opencode

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/wake/purdex/internal/agent"
)

// resolveCanonicalPdxPath returns the daemon's own executable path with
// symlinks resolved — the single trusted pdxPath CheckHooks renders the
// managed template against. The second return reports whether resolution
// succeeded; callers must treat false as "body differs" rather than
// falling back to the on-disk literal (PR #616 review Finding #3). It is
// a package-level var so tests can stub without touching os.Executable.
var resolveCanonicalPdxPath = func() (string, bool) {
	exe, err := os.Executable()
	if err != nil || exe == "" {
		return "", false
	}
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil || resolved == "" {
		return "", false
	}
	return resolved, true
}

func (p *Provider) InstallHooks(pdxPath string) error {
	pluginPath, err := opencodePluginPath()
	if err != nil {
		return err
	}
	return writeManagedPlugin(pluginPath, pdxPath)
}

func (p *Provider) RemoveHooks(_ string) error {
	pluginPath, err := opencodePluginPath()
	if err != nil {
		return err
	}
	data, err := os.ReadFile(pluginPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read plugin: %w", err)
	}
	if !isManagedPlugin(data) {
		return fmt.Errorf("plugin file exists but is unmanaged")
	}
	if err := os.Remove(pluginPath); err != nil {
		return fmt.Errorf("remove plugin: %w", err)
	}
	return nil
}

func (p *Provider) CheckHooks() (agent.HookStatus, error) {
	pluginPath, err := opencodePluginPath()
	if err != nil {
		return agent.HookStatus{Issues: []string{"cannot find home dir"}}, err
	}
	agentVersion := agent.DetectHookAgentVersion("opencode", "--version")
	data, err := os.ReadFile(pluginPath)
	if err != nil {
		if os.IsNotExist(err) {
			return agent.HookStatus{
				Installed:    false,
				Events:       map[string]agent.HookEventInfo{},
				Issues:       []string{"pdx-agent-hooks.js not found"},
				AgentVersion: agentVersion,
			}, nil
		}
		return agent.HookStatus{}, fmt.Errorf("read plugin: %w", err)
	}
	if !isManagedPlugin(data) {
		return agent.HookStatus{
			Installed:    false,
			Events:       map[string]agent.HookEventInfo{},
			Issues:       []string{"plugin file exists but is unmanaged"},
			AgentVersion: agentVersion,
		}, nil
	}

	// Plan §1.6 + PR #616 review Finding #3: the opencode plugin is a
	// single pdx-managed artifact. CheckHooks renders the expected template
	// against the trusted runtime canonical pdxPath (daemon's own
	// os.Executable, symlinks resolved) — never against the literal the
	// file itself carries, which would make path-only edits silently pass
	// their own round-trip. Any deviation (comment, whitespace, dead code,
	// broken switch/case, tampered pdxPath literal) collapses every
	// declared event to Installed=false — "managed body drifted,
	// reinstall" matches the plugin's all-or-nothing semantics.
	specs := p.Events()
	trustedPath, resolved := resolveCanonicalPdxPath()
	events := make(map[string]agent.HookEventInfo, len(specs))
	if !resolved {
		for _, spec := range specs {
			events[spec.Name] = agent.HookEventInfo{Installed: false, Command: pluginPath}
		}
		return agent.HookStatus{
			Installed:    false,
			Events:       events,
			Issues:       []string{"plugin body differs from managed template (cannot resolve canonical pdx path — run reinstall)"},
			AgentVersion: agentVersion,
		}, nil
	}
	expected := renderManagedPlugin(trustedPath)
	if !bytes.Equal(data, []byte(expected)) {
		for _, spec := range specs {
			events[spec.Name] = agent.HookEventInfo{Installed: false, Command: pluginPath}
		}
		return agent.HookStatus{
			Installed:    false,
			Events:       events,
			Issues:       []string{"plugin body differs from managed template (pdx binary may have moved or file was edited — run reinstall)"},
			AgentVersion: agentVersion,
		}, nil
	}
	for _, spec := range specs {
		events[spec.Name] = agent.HookEventInfo{Installed: true, Command: pluginPath}
	}
	return agent.HookStatus{Installed: true, Events: events, Issues: []string{}, AgentVersion: agentVersion}, nil
}

func opencodePluginPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}
	return filepath.Join(home, ".config", "opencode", "plugins", "pdx-agent-hooks.js"), nil
}

func writeManagedPlugin(path, pdxPath string) error {
	if data, err := os.ReadFile(path); err == nil && !isManagedPlugin(data) {
		return fmt.Errorf("plugin file exists but is unmanaged")
	} else if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read plugin: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create directory: %w", err)
	}
	out := []byte(renderManagedPlugin(pdxPath))
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, out, 0644); err != nil {
		return fmt.Errorf("write plugin: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("rename plugin: %w", err)
	}
	return nil
}

func isManagedPlugin(data []byte) bool {
	return strings.Contains(string(data), managedMarker)
}
