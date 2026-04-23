package opencode

import "github.com/wake/purdex/internal/agent"

// opencodeEventSpecs is the declarative hook event catalog for OpenCode. It
// is the single source of truth for CheckHooks iteration, SupportedStatuses
// derivation, and future Inspector UI. The managed plugin template
// (plugin_template.go) wires the same events on the JS side; ordering here
// matches the plan §1.4 opencode table.
//
// Note: writeManagedPlugin/renderManagedPlugin write a fixed-string plugin
// body; this plan's zero-change boundary (§1.7 / §1.8) keeps that template
// untouched. Only the Go-side iteration (CheckHooks) migrates to Events().
var opencodeEventSpecs = []agent.HookEventSpec{
	{
		Name:        "SessionStart",
		EmitsStatus: []agent.Status{agent.StatusIdle},
		Description: "OpenCode session started",
	},
	{
		Name:        "UserPromptSubmit",
		EmitsStatus: []agent.Status{agent.StatusRunning},
		Description: "User submitted a prompt",
	},
	{
		Name:        "SubagentStart",
		EmitsStatus: []agent.Status{},
		Description: "Nested sub-agent task dispatched",
	},
	{
		Name:        "SubagentStop",
		EmitsStatus: []agent.Status{},
		Description: "Nested sub-agent task completed",
	},
	{
		Name:        "PermissionRequest",
		EmitsStatus: []agent.Status{agent.StatusWaiting},
		Description: "Tool permission request awaiting user approval",
	},
	{
		Name:        "Stop",
		EmitsStatus: []agent.Status{agent.StatusIdle},
		Description: "Agent finished responding and is idle",
	},
	{
		Name:        "StopFailure",
		EmitsStatus: []agent.Status{agent.StatusError},
		Description: "Agent stopped due to an error",
	},
	{
		Name:        "SessionEnd",
		EmitsStatus: []agent.Status{agent.StatusClear},
		Description: "OpenCode session ended",
	},
}

// Events returns a fresh defensive copy of the opencode hook event catalog
// on every call.
func (p *Provider) Events() []agent.HookEventSpec {
	out := make([]agent.HookEventSpec, len(opencodeEventSpecs))
	for i, spec := range opencodeEventSpecs {
		out[i] = agent.HookEventSpec{
			Name:        spec.Name,
			EmitsStatus: append([]agent.Status(nil), spec.EmitsStatus...),
			Description: spec.Description,
		}
		if out[i].EmitsStatus == nil {
			out[i].EmitsStatus = []agent.Status{}
		}
	}
	return out
}

// eventNames returns the ordered event Name list for CheckHooks iteration.
func (p *Provider) eventNames() []string {
	return opencodeEventNames()
}

// opencodeEventNames is the package-level helper. Kept in parallel with the
// provider-method form for parity with cc/codex; any future free function in
// this package routes through the same spec slice.
func opencodeEventNames() []string {
	out := make([]string, 0, len(opencodeEventSpecs))
	for _, spec := range opencodeEventSpecs {
		out = append(out, spec.Name)
	}
	return out
}
