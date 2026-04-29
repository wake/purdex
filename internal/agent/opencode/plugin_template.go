package opencode

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/wake/purdex/internal/agent"
)

const managedMarker = "pdx-managed:opencode-hooks:v1"

// mappedHookEvent describes a (Name, Payload) pair the JS template would
// emit() for a given Bus event or strong-hook fixture. Used by OC1's
// pluginSimState (the live JS-mirror) to assert renderManagedPlugin's
// output without spawning a Bun runtime.
type mappedHookEvent struct {
	Name    string
	Payload map[string]any
}

// renderPurdexEventConst emits the JS object literal that the rendered
// plugin uses for emit() argument substitution (P3-T3). Keys = PurdexName
// (Pdx-prefixed) so the JS source identifies events by canonical Purdex
// id rather than upstream Bus key. Iteration order matches catalog order
// so byte-exact template comparison stays stable.
func renderPurdexEventConst(specs []agent.HookEventSpec) string {
	var b strings.Builder
	b.WriteString("{\n")
	for _, s := range specs {
		if !agent.IsInstallableHookSpec(s) {
			continue
		}
		fmt.Fprintf(&b, "    %s: %q,\n", s.PurdexName, s.PurdexName)
	}
	b.WriteString("  }")
	return b.String()
}

func renderManagedPlugin(pdxPath string) string {
	return fmt.Sprintf(`// %s
export const PurdexOpenCodeHooks = async () => {
  const activeSubagents = new Map()
  const suppressIdleForSession = new Set()
  const pdxPath = %q
  const PURDEX_EVENT = %s

  async function emit(eventName, payload = {}) {
    const encoded = JSON.stringify(payload)
    const proc = Bun.spawn({
      cmd: [pdxPath, 'hook', '--agent', 'opencode', eventName],
      stdin: 'pipe',
      stdout: 'ignore',
      stderr: 'ignore',
    })
    proc.stdin.write(encoded)
    proc.stdin.end()
    await proc.exited
  }

  function agentTypeFromArgs(args) {
    if (!args || typeof args !== 'object') return 'task'
    if (typeof args.subagent_type === 'string' && args.subagent_type) return args.subagent_type
    if (typeof args.agent === 'string' && args.agent) return args.agent
    return 'task'
  }

  return {
    event: async ({ event }) => {
      switch (event.type) {
        case 'session.created':
          await emit(PURDEX_EVENT.PdxSessionStart, { session_id: event.properties.sessionID })
          return
        case 'permission.asked':
          await emit(PURDEX_EVENT.PdxPermissionRequest, {
            request_type: 'permission',
            permission: event.properties.permission,
            patterns: event.properties.patterns,
          })
          return
        case 'question.asked':
          await emit(PURDEX_EVENT.PdxPermissionRequest, {
            request_type: 'question',
            questions: event.properties.questions,
          })
          return
        case 'session.error':
          if (event.properties.sessionID) suppressIdleForSession.add(event.properties.sessionID)
          await emit(PURDEX_EVENT.PdxStopFailure, {
            error: event.properties.error?.name || '',
            error_details: event.properties.error?.data?.message || '',
          })
          return
        case 'session.status':
          if (event.properties.status?.type !== 'idle') return
          if (suppressIdleForSession.has(event.properties.sessionID)) {
            suppressIdleForSession.delete(event.properties.sessionID)
            return
          }
          await emit(PURDEX_EVENT.PdxStop, { session_id: event.properties.sessionID })
          return
        case 'session.deleted':
          await emit(PURDEX_EVENT.PdxSessionEnd, { session_id: event.properties.sessionID })
          return
      }
    },
    'chat.message': async (input, output) => {
      // New prompt cycle: clear any stale suppressIdleForSession entry
      // the previous cycle's session.error armed. Without this, the next
      // legitimate idle for this session is consumed by the stale entry
      // and Stop is never emitted (session sticks Running/Error).
      if (input.sessionID) suppressIdleForSession.delete(input.sessionID)
      const model = input.model
      const modelName = model ? (model.providerID + '/' + model.modelID) : ''
      await emit(PURDEX_EVENT.PdxUserPromptSubmit, {
        session_id: input.sessionID,
        message_id: input.messageID || output.message?.id || '',
        agent: output.message?.agent || input.agent || '',
        modelName,
        source: 'chat.message',
      })
    },
    'tool.execute.before': async (input, output) => {
      if (input.tool !== 'task' || !input.callID) return
      const subagentKey = input.sessionID + ':' + input.callID
      if (activeSubagents.has(subagentKey)) return
      const agentType = agentTypeFromArgs(output.args)
      activeSubagents.set(subagentKey, agentType)
      await emit(PURDEX_EVENT.PdxSubagentStart, {
        agent_id: input.callID,
        agent_type: agentType,
        description: typeof output.args?.description === 'string' ? output.args.description : '',
        prompt: typeof output.args?.prompt === 'string' ? output.args.prompt : '',
      })
    },
    'tool.execute.after': async (input, output) => {
      if (input.tool !== 'task' || !input.callID) return
      const subagentKey = input.sessionID + ':' + input.callID
      const agentType = activeSubagents.get(subagentKey)
      if (!agentType) return
      activeSubagents.delete(subagentKey)
      await emit(PURDEX_EVENT.PdxSubagentStop, {
        agent_id: input.callID,
        agent_type: agentType,
        title: output.title || '',
        output: output.output || '',
      })
    },
  }
}
`, managedMarker, pdxPath, renderPurdexEventConst(opencodeEventSpecs))
}

// emittedEventPattern matches the two emit() argument shapes the plugin
// template can produce: (a) the canonical post-P3 form
// emit(PURDEX_EVENT.PdxXxx, ...) and (b) the legacy string-literal form
// emit('Xxx', ...) the helper still recognizes so test-layer drift checks
// (PT3 EmitNotInSpec) continue to surface accidental string-literal emit
// regressions. Strictly used by test-layer parity checks (plan §1.5):
// runtime health goes through byte-exact template comparison, so this
// regex's well-known blind spots (comment strings, dead code) never reach
// production judgement.
var emittedEventPattern = regexp.MustCompile(`emit\((?:['"](\w+)['"]|PURDEX_EVENT\.(\w+))`)

// pdxPathLiteralPattern captures the complete quoted Go string literal
// that renderManagedPlugin writes with %q. The [^"\\]|\\. alternation
// covers escaped quotes and backslashes, which a naive `"([^"]+)"` regex
// would otherwise cut short. The capture intentionally includes the
// enclosing double quotes so strconv.Unquote can consume it verbatim
// (plan §1.6 / v4 findings).
var pdxPathLiteralPattern = regexp.MustCompile(`const pdxPath = ("(?:[^"\\]|\\.)*")`)

// extractEmittedEvents returns every event name that the given body
// invokes emit() with. Order preserves first appearance. Test-only helper
// per plan §1.5 — do not wire it into CheckHooks.
func extractEmittedEvents(body string) []string {
	matches := emittedEventPattern.FindAllStringSubmatch(body, -1)
	seen := make(map[string]bool, len(matches))
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		if len(m) < 3 {
			continue
		}
		// Group 1 = string-literal form; Group 2 = PURDEX_EVENT.X form.
		name := m[1]
		if name == "" {
			name = m[2]
		}
		if name == "" {
			continue
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

// validateSpecsCoverEmitted enforces bidirectional parity between the
// event names a managed plugin body emits and the HookEventSpec slice
// driving Go-side installer / checker / Inspector paths. A superset on
// either side yields an error. Scoped to test layer (plan §1.5): runtime
// never calls this — a build-time drift blocks merge via
// TestTemplateSpecsParity (PT7) rather than panicking production code.
//
// P3-T3: declared set keys on PurdexName (canonical Pdx-prefixed id).
// extractEmittedEvents returns whatever the emit() RHS resolved to; with
// PURDEX_EVENT.PdxXxx the captured name is "PdxXxx" so both sides line up.
func validateSpecsCoverEmitted(body string, specs []agent.HookEventSpec) error {
	emitted := make(map[string]bool)
	for _, name := range extractEmittedEvents(body) {
		emitted[name] = true
	}
	declared := make(map[string]bool, len(specs))
	for _, spec := range specs {
		if !agent.IsInstallableHookSpec(spec) {
			continue
		}
		declared[spec.PurdexName] = true
	}
	var missingInSpec []string
	for name := range emitted {
		if !declared[name] {
			missingInSpec = append(missingInSpec, name)
		}
	}
	var missingInEmit []string
	for name := range declared {
		if !emitted[name] {
			missingInEmit = append(missingInEmit, name)
		}
	}
	if len(missingInSpec) == 0 && len(missingInEmit) == 0 {
		return nil
	}
	sort.Strings(missingInSpec)
	sort.Strings(missingInEmit)
	var parts []string
	if len(missingInSpec) > 0 {
		parts = append(parts, fmt.Sprintf("template emits %v but specs do not declare them", missingInSpec))
	}
	if len(missingInEmit) > 0 {
		parts = append(parts, fmt.Sprintf("specs declare %v but template never emits them", missingInEmit))
	}
	return fmt.Errorf("template/specs parity: %s", strings.Join(parts, "; "))
}

// extractPdxPath pulls the pdxPath literal out of a managed plugin body
// and unescapes it to the original path string. Returns ok=false on any
// failure (literal not found, malformed escape) so CheckHooks can fall
// back to the unmanaged path rather than panic (v4 plan §1.6).
func extractPdxPath(body string) (string, bool) {
	m := pdxPathLiteralPattern.FindStringSubmatch(body)
	if len(m) < 2 {
		return "", false
	}
	unquoted, err := strconv.Unquote(m[1])
	if err != nil {
		return "", false
	}
	return unquoted, true
}

func strMapVal(values map[string]any, key string) string {
	if values == nil {
		return ""
	}
	if value, ok := values[key].(string); ok {
		return value
	}
	return ""
}
