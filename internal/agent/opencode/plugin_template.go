package opencode

import "fmt"

const managedMarker = "pdx-managed:opencode-hooks:v1"

type mappedHookEvent struct {
	Name    string
	Payload map[string]any
}

type pluginState struct {
	activeSubagents map[string]string
	suppressIdle    bool
}

func newPluginState() *pluginState {
	return &pluginState{activeSubagents: make(map[string]string)}
}

func (s *pluginState) handleTaskStart(callID string, args map[string]any) (mappedHookEvent, bool) {
	if callID == "" {
		return mappedHookEvent{}, false
	}
	if _, exists := s.activeSubagents[callID]; exists {
		return mappedHookEvent{}, false
	}
	agentType := firstString(args, "subagent_type", "agent")
	if agentType == "" {
		agentType = "task"
	}
	s.activeSubagents[callID] = agentType
	payload := map[string]any{
		"agent_id":   callID,
		"agent_type": agentType,
	}
	if description := strMapVal(args, "description"); description != "" {
		payload["description"] = description
	}
	if prompt := strMapVal(args, "prompt"); prompt != "" {
		payload["prompt"] = prompt
	}
	return mappedHookEvent{Name: "SubagentStart", Payload: payload}, true
}

func (s *pluginState) handleTaskStop(callID, title, output string) (mappedHookEvent, bool) {
	if callID == "" {
		return mappedHookEvent{}, false
	}
	agentType, exists := s.activeSubagents[callID]
	if !exists {
		return mappedHookEvent{}, false
	}
	delete(s.activeSubagents, callID)
	payload := map[string]any{
		"agent_id":   callID,
		"agent_type": agentType,
	}
	if title != "" {
		payload["title"] = title
	}
	if output != "" {
		payload["output"] = output
	}
	return mappedHookEvent{Name: "SubagentStop", Payload: payload}, true
}

func (s *pluginState) handleSessionError(errorName, errorDetails string) (mappedHookEvent, bool) {
	s.suppressIdle = true
	payload := map[string]any{}
	if errorName != "" {
		payload["error"] = errorName
	}
	if errorDetails != "" {
		payload["error_details"] = errorDetails
	}
	return mappedHookEvent{Name: "StopFailure", Payload: payload}, true
}

func (s *pluginState) handleSessionIdle() (mappedHookEvent, bool) {
	if s.suppressIdle {
		s.suppressIdle = false
		return mappedHookEvent{}, false
	}
	return mappedHookEvent{Name: "Stop", Payload: map[string]any{}}, true
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
      stdin: JSON.stringify(payload),
      stdout: 'ignore',
      stderr: 'ignore',
    })
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
        case 'session.idle':
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
      const model = output.message?.model
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
      const agentType = agentTypeFromArgs(output.args)
      activeSubagents.set(input.callID, agentType)
      await emit('SubagentStart', {
        agent_id: input.callID,
        agent_type: agentType,
        description: typeof output.args?.description === 'string' ? output.args.description : '',
        prompt: typeof output.args?.prompt === 'string' ? output.args.prompt : '',
      })
    },
    'tool.execute.after': async (input, output) => {
      if (input.tool !== 'task' || !input.callID) return
      const agentType = activeSubagents.get(input.callID)
      if (!agentType) return
      activeSubagents.delete(input.callID)
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

func firstString(values map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := strMapVal(values, key); value != "" {
			return value
		}
	}
	return ""
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
