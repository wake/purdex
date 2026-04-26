package codex

import "github.com/wake/purdex/internal/agent"

// codexEventSpecs is the declarative hook event catalog for Codex. It
// expands the previous 3-event installer (SessionStart, UserPromptSubmit,
// Stop) to the full 9-event set (issue #613 / plan §1.4), reaching parity
// with cc and with codex DeriveStatus, which has supported all 9 events
// since Phase 1.
//
// The 6 newly-added events (SubagentStart, SubagentStop, StopFailure,
// Notification, PermissionRequest, SessionEnd) may not be emitted by the
// current codex CLI in every path. Declaring them is intentional per plan
// §8 risk table: it rigs the installer so proxy paths and future CLI
// versions land on ready infrastructure, and the drift test pins
// Events() ↔ DeriveStatus parity either way.
var codexEventSpecs = []agent.HookEventSpec{
	{
		Name:        "SessionStart",
		EmitsStatus: []agent.Status{agent.StatusIdle},
		Description: "Codex session started",
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
		FutureOnly:  true,
	},
	{
		Name:        "SubagentStop",
		EmitsStatus: []agent.Status{},
		Description: "Nested sub-agent task completed",
		FutureOnly:  true,
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
		FutureOnly:  true,
	},
	{
		Name:        "Notification",
		EmitsStatus: []agent.Status{agent.StatusWaiting, agent.StatusIdle},
		Description: "Permission/elicitation/idle prompt notifications",
		FutureOnly:  true,
	},
	{
		Name:        "PermissionRequest",
		EmitsStatus: []agent.Status{agent.StatusWaiting},
		Description: "Tool permission request awaiting user approval",
		FutureOnly:  true,
	},
	{
		Name:        "SessionEnd",
		EmitsStatus: []agent.Status{agent.StatusClear},
		Description: "Codex session ended",
		FutureOnly:  true,
	},
}

// Events returns a fresh defensive copy of the codex hook event catalog on
// every call.
func (p *Provider) Events() []agent.HookEventSpec {
	out := make([]agent.HookEventSpec, len(codexEventSpecs))
	for i, spec := range codexEventSpecs {
		out[i] = agent.HookEventSpec{
			Name:        spec.Name,
			EmitsStatus: append([]agent.Status(nil), spec.EmitsStatus...),
			Description: spec.Description,
			FutureOnly:  spec.FutureOnly,
			Handling:    spec.Handling,
		}
		if out[i].EmitsStatus == nil {
			out[i].EmitsStatus = []agent.Status{}
		}
	}
	return out
}

// eventNames returns the ordered installable event Name list for installer /
// check iteration, derived from codexEventSpecs so there is no parallel SSoT.
func (p *Provider) eventNames() []string {
	return codexEventNames()
}

// codexEventNames is the package-level helper used by mergeCodexHooks (a
// free function, not a method).
func codexEventNames() []string {
	out := make([]string, 0, len(codexEventSpecs))
	for _, spec := range codexEventSpecs {
		if !agent.IsInstallableHookSpec(spec) {
			continue
		}
		out = append(out, spec.Name)
	}
	return out
}
