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

func renderManagedPlugin(pdxPath string) string {
	return fmt.Sprintf(`// %s
export const PurdexOpenCodeHooks = async () => {
  const activeSubagents = new Map()
  const suppressIdleForSession = new Set()
  const pdxPath = %q

  async function emit(eventName, payload = {}) {
    const proc = Bun.spawn({
      cmd: [pdxPath, 'hook', '--agent', 'opencode', eventName],
      stdin: 'pipe',
      stdout: 'ignore',
      stderr: 'ignore',
    })
    proc.stdin.write(JSON.stringify(payload))
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
          await emit('SessionStart', { session_id: event.properties.sessionID })
          return
        case 'permission.asked':
          await emit('PermissionRequest', {
            request_type: 'permission',
            permission: event.properties.permission,
            patterns: event.properties.patterns,
          })
          return
        case 'question.asked':
          await emit('PermissionRequest', {
            request_type: 'question',
            questions: event.properties.questions,
          })
          return
        case 'session.error':
          if (event.properties.sessionID) suppressIdleForSession.add(event.properties.sessionID)
          await emit('StopFailure', {
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
          await emit('Stop', { session_id: event.properties.sessionID })
          return
        case 'session.deleted':
          await emit('SessionEnd', { session_id: event.properties.sessionID })
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
      await emit('UserPromptSubmit', {
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
      await emit('SubagentStart', {
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
      await emit('SubagentStop', {
        agent_id: input.callID,
        agent_type: agentType,
        title: output.title || '',
        output: output.output || '',
      })
    },
  }
}
`, managedMarker, pdxPath)
}

// emittedEventPattern matches `emit('Name', …)` / `emit("Name", …)` inside
// the plugin body. Strictly used by test-layer parity checks (plan §1.5):
// runtime health goes through byte-exact template comparison, so this
// regex's well-known blind spots (comment strings, dead code) never reach
// production judgement. Keeping the expression scoped to the single
// emit() call shape keeps the helper understandable for that test-only
// role.
var emittedEventPattern = regexp.MustCompile(`emit\(['"](\w+)['"]`)

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
		if len(m) < 2 {
			continue
		}
		name := m[1]
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
		declared[spec.Name] = true
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
