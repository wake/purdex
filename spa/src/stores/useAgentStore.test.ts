// spa/src/stores/useAgentStore.test.ts
import { describe, it, expect, beforeEach } from 'vitest'
import { useAgentStore } from './useAgentStore'
import { useTabStore } from './useTabStore'
import { createTab } from '../types/tab'
import type { NormalizedEvent } from './useAgentStore'

const H = 'test-host'

beforeEach(() => {
  useAgentStore.setState({
    statuses: {},
    agentTypes: {},
    models: {},
    subagents: {},
    lastEvents: {},
    unread: {},
  })
  useTabStore.setState({ tabs: {}, activeTabId: null, tabOrder: [] })
})

describe('useAgentStore', () => {
  it('running status from backend → stored correctly', () => {
    const event: NormalizedEvent = {
      agent_type: 'cc',
      status: 'running',
      raw_event_name: 'UserPromptSubmit',
      broadcast_ts: Date.now(),
    }
    useAgentStore.getState().handleNormalizedEvent(H, 'dev', event)
    expect(useAgentStore.getState().statuses[`${H}:dev`]).toBe('running')
  })

  it('waiting status → marks unread when not focused', () => {
    const event: NormalizedEvent = {
      agent_type: 'cc',
      status: 'waiting',
      raw_event_name: 'Notification',
      broadcast_ts: Date.now(),
      detail: { notification_type: 'permission_prompt' },
    }
    useAgentStore.getState().handleNormalizedEvent(H, 'dev', event)
    expect(useAgentStore.getState().statuses[`${H}:dev`]).toBe('waiting')
    expect(useAgentStore.getState().unread[`${H}:dev`]).toBe(true)
  })

  it('idle status → stored correctly', () => {
    const event: NormalizedEvent = {
      agent_type: 'cc',
      status: 'idle',
      raw_event_name: 'Stop',
      broadcast_ts: Date.now(),
    }
    useAgentStore.getState().handleNormalizedEvent(H, 'dev', event)
    expect(useAgentStore.getState().statuses[`${H}:dev`]).toBe('idle')
  })

  it('error status → stored + marks unread', () => {
    const event: NormalizedEvent = {
      agent_type: 'cc',
      status: 'error',
      raw_event_name: 'StopFailure',
      broadcast_ts: Date.now(),
      detail: { error: 'rate_limit' },
    }
    useAgentStore.getState().handleNormalizedEvent(H, 'dev', event)
    expect(useAgentStore.getState().statuses[`${H}:dev`]).toBe('error')
    expect(useAgentStore.getState().unread[`${H}:dev`]).toBe(true)
  })

  it('clear status → removes all state for session', () => {
    // Pre-populate state
    useAgentStore.setState({
      statuses: { [`${H}:dev`]: 'idle' },
      agentTypes: { [`${H}:dev`]: 'cc' },
      models: { [`${H}:dev`]: 'claude-sonnet-4-6' },
      subagents: { [`${H}:dev`]: ['sub-1'] },
      lastEvents: { [`${H}:dev`]: { agent_type: 'cc', status: 'idle', raw_event_name: 'Stop', broadcast_ts: 1 } },
      unread: { [`${H}:dev`]: true },
    })
    const event: NormalizedEvent = {
      agent_type: 'cc',
      status: 'clear',
      raw_event_name: 'SessionEnd',
      broadcast_ts: Date.now(),
    }
    useAgentStore.getState().handleNormalizedEvent(H, 'dev', event)
    const state = useAgentStore.getState()
    expect(state.statuses[`${H}:dev`]).toBeUndefined()
    expect(state.agentTypes[`${H}:dev`]).toBeUndefined()
    expect(state.models[`${H}:dev`]).toBeUndefined()
    expect(state.subagents[`${H}:dev`]).toBeUndefined()
    expect(state.lastEvents[`${H}:dev`]).toBeUndefined()
    expect(state.unread[`${H}:dev`]).toBeUndefined()
  })

  it('model persists across events (via event.model field)', () => {
    useAgentStore.getState().handleNormalizedEvent(H, 'dev', {
      agent_type: 'cc',
      status: 'idle',
      model: 'claude-sonnet-4-6',
      raw_event_name: 'SessionStart',
      broadcast_ts: Date.now(),
    })
    expect(useAgentStore.getState().models[`${H}:dev`]).toBe('claude-sonnet-4-6')

    // Subsequent event without model does not clear it
    useAgentStore.getState().handleNormalizedEvent(H, 'dev', {
      agent_type: 'cc',
      status: 'running',
      raw_event_name: 'UserPromptSubmit',
      broadcast_ts: Date.now(),
    })
    expect(useAgentStore.getState().models[`${H}:dev`]).toBe('claude-sonnet-4-6')
  })

  it('subagent tracking from event.subagents array', () => {
    // Event with subagents
    useAgentStore.getState().handleNormalizedEvent(H, 'dev', {
      agent_type: 'cc',
      status: 'running',
      subagents: ['agent-A', 'agent-B'],
      raw_event_name: 'UserPromptSubmit',
      broadcast_ts: Date.now(),
    })
    expect(useAgentStore.getState().subagents[`${H}:dev`]).toEqual(['agent-A', 'agent-B'])

    // Event with empty subagents removes the entry
    useAgentStore.getState().handleNormalizedEvent(H, 'dev', {
      agent_type: 'cc',
      status: 'running',
      subagents: [],
      raw_event_name: 'UserPromptSubmit',
      broadcast_ts: Date.now(),
    })
    expect(useAgentStore.getState().subagents[`${H}:dev`]).toBeUndefined()
  })

  it('event without subagents field does not clear existing subagents', () => {
    useAgentStore.setState({ subagents: { [`${H}:dev`]: ['agent-A'] } })
    useAgentStore.getState().handleNormalizedEvent(H, 'dev', {
      agent_type: 'cc',
      status: 'running',
      raw_event_name: 'UserPromptSubmit',
      broadcast_ts: Date.now(),
    })
    expect(useAgentStore.getState().subagents[`${H}:dev`]).toEqual(['agent-A'])
  })

  it('markRead → clears unread', () => {
    useAgentStore.setState({ unread: { [`${H}:dev`]: true } })
    useAgentStore.getState().markRead(H, 'dev')
    expect(useAgentStore.getState().unread[`${H}:dev`]).toBeUndefined()
  })

  it('removeHost → clears all host data, preserves others', () => {
    useAgentStore.setState({
      statuses: {
        [`${H}:dev`]: 'idle',
        [`${H}:staging`]: 'running',
        ['other-host:dev']: 'waiting',
      },
      agentTypes: {
        [`${H}:dev`]: 'cc',
        ['other-host:dev']: 'codex',
      },
      models: {
        [`${H}:dev`]: 'claude-sonnet-4-6',
        ['other-host:dev']: 'claude-opus-4-6',
      },
      subagents: {
        [`${H}:dev`]: ['agent-A'],
        ['other-host:dev']: ['agent-B'],
      },
      lastEvents: {
        [`${H}:dev`]: { agent_type: 'cc', status: 'idle', raw_event_name: 'Stop', broadcast_ts: 1 },
        [`${H}:staging`]: { agent_type: 'cc', status: 'running', raw_event_name: 'UserPromptSubmit', broadcast_ts: 2 },
        ['other-host:dev']: { agent_type: 'codex', status: 'waiting', raw_event_name: 'Notification', broadcast_ts: 3 },
      },
      unread: {
        [`${H}:dev`]: true,
        ['other-host:dev']: true,
      },
    })

    useAgentStore.getState().removeHost(H)
    const state = useAgentStore.getState()

    // Host entries cleared
    expect(state.statuses[`${H}:dev`]).toBeUndefined()
    expect(state.statuses[`${H}:staging`]).toBeUndefined()
    expect(state.agentTypes[`${H}:dev`]).toBeUndefined()
    expect(state.models[`${H}:dev`]).toBeUndefined()
    expect(state.subagents[`${H}:dev`]).toBeUndefined()
    expect(state.lastEvents[`${H}:dev`]).toBeUndefined()
    expect(state.lastEvents[`${H}:staging`]).toBeUndefined()
    expect(state.unread[`${H}:dev`]).toBeUndefined()

    // Other host preserved
    expect(state.statuses['other-host:dev']).toBe('waiting')
    expect(state.agentTypes['other-host:dev']).toBe('codex')
    expect(state.models['other-host:dev']).toBe('claude-opus-4-6')
    expect(state.subagents['other-host:dev']).toEqual(['agent-B'])
    expect(state.lastEvents['other-host:dev']).toBeDefined()
    expect(state.unread['other-host:dev']).toBe(true)
  })

  it('Notification(idle_prompt) → idle, does NOT mark unread', () => {
    const event: NormalizedEvent = {
      agent_type: 'cc',
      status: 'idle',
      raw_event_name: 'Notification',
      broadcast_ts: Date.now(),
      detail: { notification_type: 'idle_prompt' },
    }
    useAgentStore.getState().handleNormalizedEvent(H, 'dev', event)
    expect(useAgentStore.getState().statuses[`${H}:dev`]).toBe('idle')
    expect(useAgentStore.getState().unread[`${H}:dev`]).toBeUndefined()
  })

  it('Stop → idle, marks unread when not focused', () => {
    const event: NormalizedEvent = {
      agent_type: 'cc',
      status: 'idle',
      raw_event_name: 'Stop',
      broadcast_ts: Date.now(),
    }
    useAgentStore.getState().handleNormalizedEvent(H, 'dev', event)
    expect(useAgentStore.getState().statuses[`${H}:dev`]).toBe('idle')
    expect(useAgentStore.getState().unread[`${H}:dev`]).toBe(true)
  })

  it('Stop → does NOT mark unread when focused', () => {
    const tab = { ...createTab({ kind: 'tmux-session', hostId: 'test-host', sessionCode: 'dev', mode: 'terminal', cachedName: '', tmuxInstance: '' }), id: 't1' }
    useTabStore.setState({ tabs: { t1: tab }, activeTabId: 't1' })
    const event: NormalizedEvent = {
      agent_type: 'cc',
      status: 'idle',
      raw_event_name: 'Stop',
      broadcast_ts: Date.now(),
    }
    useAgentStore.getState().handleNormalizedEvent(H, 'dev', event)
    expect(useAgentStore.getState().unread[`${H}:dev`]).toBeUndefined()
  })

  it('agent type is stored from event', () => {
    useAgentStore.getState().handleNormalizedEvent(H, 'dev', {
      agent_type: 'codex',
      status: 'running',
      raw_event_name: 'UserPromptSubmit',
      broadcast_ts: Date.now(),
    })
    expect(useAgentStore.getState().agentTypes[`${H}:dev`]).toBe('codex')
  })

  it('lastEvents stores the latest event', () => {
    const event: NormalizedEvent = {
      agent_type: 'cc',
      status: 'running',
      raw_event_name: 'UserPromptSubmit',
      broadcast_ts: Date.now(),
      detail: { session_id: 'abc123' },
    }
    useAgentStore.getState().handleNormalizedEvent(H, 'dev', event)
    expect(useAgentStore.getState().lastEvents[`${H}:dev`]).toEqual(event)
  })

  it('event with empty status string does not update statuses', () => {
    useAgentStore.setState({ statuses: { [`${H}:dev`]: 'running' } })
    const event: NormalizedEvent = {
      agent_type: 'cc',
      status: '',
      subagents: ['agent-A'],
      raw_event_name: 'SubagentStart',
      broadcast_ts: Date.now(),
    }
    useAgentStore.getState().handleNormalizedEvent(H, 'dev', event)
    // Status unchanged
    expect(useAgentStore.getState().statuses[`${H}:dev`]).toBe('running')
    // But subagents updated
    expect(useAgentStore.getState().subagents[`${H}:dev`]).toEqual(['agent-A'])
  })

  it('models map returns undefined for unknown key', () => {
    expect(useAgentStore.getState().models[`${H}:unknown`]).toBeUndefined()
  })

  it('clear status preserves other sessions', () => {
    useAgentStore.setState({
      statuses: { [`${H}:dev`]: 'idle', [`${H}:staging`]: 'running' },
      agentTypes: { [`${H}:dev`]: 'cc', [`${H}:staging`]: 'cc' },
    })
    useAgentStore.getState().handleNormalizedEvent(H, 'dev', {
      agent_type: 'cc',
      status: 'clear',
      raw_event_name: 'SessionEnd',
      broadcast_ts: Date.now(),
    })
    expect(useAgentStore.getState().statuses[`${H}:dev`]).toBeUndefined()
    expect(useAgentStore.getState().statuses[`${H}:staging`]).toBe('running')
    expect(useAgentStore.getState().agentTypes[`${H}:staging`]).toBe('cc')
  })
})
