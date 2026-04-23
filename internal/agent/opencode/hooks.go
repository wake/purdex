package opencode

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/wake/purdex/internal/agent"
)

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

	// Plan §1.6: the opencode plugin is a single pdx-managed artifact, not
	// a per-event configuration. CheckHooks succeeds only when the on-disk
	// body byte-matches the rendered template for the pdxPath literal the
	// file itself carries. Any deviation (comment, whitespace, dead code,
	// broken switch/case) collapses every declared event to Installed=false
	// — "managed body drifted, reinstall" is a uniform signal that matches
	// the plugin's all-or-nothing semantics.
	specs := p.Events()
	pdxPath, ok := extractPdxPath(string(data))
	if !ok {
		events := make(map[string]agent.HookEventInfo, len(specs))
		for _, spec := range specs {
			events[spec.Name] = agent.HookEventInfo{Installed: false, Command: pluginPath}
		}
		return agent.HookStatus{
			Installed:    false,
			Events:       events,
			Issues:       []string{"plugin body differs from managed template (run reinstall)"},
			AgentVersion: agentVersion,
		}, nil
	}
	expected := renderManagedPlugin(pdxPath)
	events := make(map[string]agent.HookEventInfo, len(specs))
	if !bytes.Equal(data, []byte(expected)) {
		for _, spec := range specs {
			events[spec.Name] = agent.HookEventInfo{Installed: false, Command: pluginPath}
		}
		return agent.HookStatus{
			Installed:    false,
			Events:       events,
			Issues:       []string{"plugin body differs from managed template (run reinstall)"},
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
