// spa/src/stores/useAgentStore.test.ts
import { describe, it, expect, beforeEach } from 'vitest'
import { useAgentStore } from './useAgentStore'
import { useTabStore } from './useTabStore'
import { createTab } from '../types/tab'
import type { AgentHookEvent } from './useAgentStore'

const H = 'test-host'

beforeEach(() => {
  useAgentStore.setState({
    events: {},
    statuses: {},
    unread: {},
    activeSubagents: {},
    hooksInstalled: false,
  })
  useTabStore.setState({ tabs: {}, activeTabId: null, tabOrder: [] })
})

describe('useAgentStore', () => {
  it('UserPromptSubmit → status = running', () => {
    const event: AgentHookEvent = {
      tmux_session: 'dev',
      event_name: 'UserPromptSubmit',
      raw_event: { foo: 'bar' },
      agent_type: 'cc',
      broadcast_ts: Date.now(),
    }
    useAgentStore.getState().handleHookEvent(H, 'dev', event)
    expect(useAgentStore.getState().statuses[`${H}:dev`]).toBe('running')
  })

  it('Notification(permission_prompt) → status = waiting', () => {
    const event: AgentHookEvent = {
      tmux_session: 'dev',
      event_name: 'Notification',
      raw_event: { notification_type: 'permission_prompt' },
      agent_type: 'cc',
      broadcast_ts: Date.now(),
    }
    useAgentStore.getState().handleHookEvent(H, 'dev', event)
    expect(useAgentStore.getState().statuses[`${H}:dev`]).toBe('waiting')
  })

  it('Notification(elicitation_dialog) → status = waiting', () => {
    const event: AgentHookEvent = {
      tmux_session: 'dev',
      event_name: 'Notification',
      raw_event: { notification_type: 'elicitation_dialog' },
      agent_type: 'cc',
      broadcast_ts: Date.now(),
    }
    useAgentStore.getState().handleHookEvent(H, 'dev', event)
    expect(useAgentStore.getState().statuses[`${H}:dev`]).toBe('waiting')
  })

  it('Notification(idle_prompt) → status = idle (from running)', () => {
    useAgentStore.setState({ statuses: { [`${H}:dev`]: 'running' } })
    const event: AgentHookEvent = {
      tmux_session: 'dev',
      event_name: 'Notification',
      raw_event: { notification_type: 'idle_prompt' },
      agent_type: 'cc',
      broadcast_ts: Date.now(),
    }
    useAgentStore.getState().handleHookEvent(H, 'dev', event)
    expect(useAgentStore.getState().statuses[`${H}:dev`]).toBe('idle')
  })

  it('Notification(auth_success) → status = idle', () => {
    useAgentStore.setState({ statuses: { [`${H}:dev`]: 'running' } })
    const event: AgentHookEvent = {
      tmux_session: 'dev',
      event_name: 'Notification',
      raw_event: { notification_type: 'auth_success' },
      agent_type: 'cc',
      broadcast_ts: Date.now(),
    }
    useAgentStore.getState().handleHookEvent(H, 'dev', event)
    expect(useAgentStore.getState().statuses[`${H}:dev`]).toBe('idle')
  })

  it('Notification without notification_type → does not change status', () => {
    useAgentStore.setState({ statuses: { [`${H}:dev`]: 'idle' } })
    const event: AgentHookEvent = {
      tmux_session: 'dev',
      event_name: 'Notification',
      raw_event: {},
      agent_type: 'cc',
      broadcast_ts: Date.now(),
    }
    useAgentStore.getState().handleHookEvent(H, 'dev', event)
    expect(useAgentStore.getState().statuses[`${H}:dev`]).toBe('idle')
  })

  it('SessionStart(compact) → does not change status', () => {
    useAgentStore.setState({ statuses: { [`${H}:dev`]: 'idle' } })
    const event: AgentHookEvent = {
      tmux_session: 'dev',
      event_name: 'SessionStart',
      raw_event: { source: 'compact' },
      agent_type: 'cc',
      broadcast_ts: Date.now(),
    }
    useAgentStore.getState().handleHookEvent(H, 'dev', event)
    expect(useAgentStore.getState().statuses[`${H}:dev`]).toBe('idle')
  })

  it('SessionStart(startup) → status = idle', () => {
    const event: AgentHookEvent = {
      tmux_session: 'dev',
      event_name: 'SessionStart',
      raw_event: { source: 'startup' },
      agent_type: 'cc',
      broadcast_ts: Date.now(),
    }
    useAgentStore.getState().handleHookEvent(H, 'dev', event)
    expect(useAgentStore.getState().statuses[`${H}:dev`]).toBe('idle')
  })

  it('SessionStart(resume) → status = idle', () => {
    const event: AgentHookEvent = {
      tmux_session: 'dev',
      event_name: 'SessionStart',
      raw_event: { source: 'resume' },
      agent_type: 'cc',
      broadcast_ts: Date.now(),
    }
    useAgentStore.getState().handleHookEvent(H, 'dev', event)
    expect(useAgentStore.getState().statuses[`${H}:dev`]).toBe('idle')
  })

  it('SessionStart → marks unread when not focused', () => {
    const event: AgentHookEvent = {
      tmux_session: 'dev',
      event_name: 'SessionStart',
      raw_event: { source: 'startup' },
      agent_type: 'cc',
      broadcast_ts: Date.now(),
    }
    useAgentStore.getState().handleHookEvent(H, 'dev', event)
    expect(useAgentStore.getState().unread[`${H}:dev`]).toBe(true)
  })

  it('StopFailure → status = error', () => {
    useAgentStore.setState({ statuses: { [`${H}:dev`]: 'running' } })
    const event: AgentHookEvent = {
      tmux_session: 'dev',
      event_name: 'StopFailure',
      raw_event: { error: 'rate_limit' },
      agent_type: 'cc',
      broadcast_ts: Date.now(),
    }
    useAgentStore.getState().handleHookEvent(H, 'dev', event)
    expect(useAgentStore.getState().statuses[`${H}:dev`]).toBe('error')
  })

  it('Stop → status = idle', () => {
    const event: AgentHookEvent = {
      tmux_session: 'dev',
      event_name: 'Stop',
      raw_event: {},
      agent_type: 'cc',
      broadcast_ts: Date.now(),
    }
    useAgentStore.getState().handleHookEvent(H, 'dev', event)
    expect(useAgentStore.getState().statuses[`${H}:dev`]).toBe('idle')
  })

  it('Stop → marks unread when active tab is not this session', () => {
    const event: AgentHookEvent = {
      tmux_session: 'dev',
      event_name: 'Stop',
      raw_event: {},
      agent_type: 'cc',
      broadcast_ts: Date.now(),
    }
    useAgentStore.getState().handleHookEvent(H, 'dev', event)
    expect(useAgentStore.getState().unread[`${H}:dev`]).toBe(true)
  })

  it('Stop → does NOT mark unread when active tab is this session', () => {
    const tab = { ...createTab({ kind: 'tmux-session', hostId: 'test-host', sessionCode: 'dev', mode: 'terminal', cachedName: '', tmuxInstance: '' }), id: 't1' }
    useTabStore.setState({ tabs: { t1: tab }, activeTabId: 't1' })
    const event: AgentHookEvent = {
      tmux_session: 'dev',
      event_name: 'Stop',
      raw_event: {},
      agent_type: 'cc',
      broadcast_ts: Date.now(),
    }
    useAgentStore.getState().handleHookEvent(H, 'dev', event)
    expect(useAgentStore.getState().unread[`${H}:dev`]).toBeUndefined()
  })

  it('Notification(idle_prompt) → does not mark unread', () => {
    const event: AgentHookEvent = {
      tmux_session: 'dev',
      event_name: 'Notification',
      raw_event: { notification_type: 'idle_prompt' },
      agent_type: 'cc',
      broadcast_ts: Date.now(),
    }
    useAgentStore.getState().handleHookEvent(H, 'dev', event)
    expect(useAgentStore.getState().unread[`${H}:dev`]).toBeUndefined()
  })

  it('Notification(auth_success) → does not mark unread', () => {
    const event: AgentHookEvent = {
      tmux_session: 'dev',
      event_name: 'Notification',
      raw_event: { notification_type: 'auth_success' },
      agent_type: 'cc',
      broadcast_ts: Date.now(),
    }
    useAgentStore.getState().handleHookEvent(H, 'dev', event)
    expect(useAgentStore.getState().unread[`${H}:dev`]).toBeUndefined()
  })

  it('StopFailure → marks unread when not focused', () => {
    const event: AgentHookEvent = {
      tmux_session: 'dev',
      event_name: 'StopFailure',
      raw_event: { error: 'rate_limit' },
      agent_type: 'cc',
      broadcast_ts: Date.now(),
    }
    useAgentStore.getState().handleHookEvent(H, 'dev', event)
    expect(useAgentStore.getState().unread[`${H}:dev`]).toBe(true)
  })

  it('Notification(permission_prompt) → marks unread when not focused', () => {
    const event: AgentHookEvent = {
      tmux_session: 'dev',
      event_name: 'Notification',
      raw_event: { notification_type: 'permission_prompt' },
      agent_type: 'cc',
      broadcast_ts: Date.now(),
    }
    useAgentStore.getState().handleHookEvent(H, 'dev', event)
    expect(useAgentStore.getState().unread[`${H}:dev`]).toBe(true)
  })

  it('markRead → clears unread', () => {
    // Set up unread state
    useAgentStore.setState({ unread: { [`${H}:dev`]: true } })
    useAgentStore.getState().markRead(H, 'dev')
    expect(useAgentStore.getState().unread[`${H}:dev`]).toBeUndefined()
  })

  it('SessionEnd → clears status', () => {
    // Set up some state for the session
    useAgentStore.setState({
      events: { [`${H}:dev`]: { tmux_session: 'dev', event_name: 'Stop', raw_event: {}, agent_type: 'cc', broadcast_ts: Date.now() } },
      statuses: { [`${H}:dev`]: 'idle' },
      unread: { [`${H}:dev`]: true },
    })
    const event: AgentHookEvent = {
      tmux_session: 'dev',
      event_name: 'SessionEnd',
      raw_event: {},
      agent_type: 'cc',
      broadcast_ts: Date.now(),
    }
    useAgentStore.getState().handleHookEvent(H, 'dev', event)
    expect(useAgentStore.getState().statuses[`${H}:dev`]).toBeUndefined()
    expect(useAgentStore.getState().events[`${H}:dev`]).toBeUndefined()
    expect(useAgentStore.getState().unread[`${H}:dev`]).toBeUndefined()
  })

  it('SessionEnd → clears activeSubagents', () => {
    useAgentStore.setState({ activeSubagents: { [`${H}:dev`]: ['agent-A'] } })
    const event: AgentHookEvent = {
      tmux_session: 'dev',
      event_name: 'SessionEnd',
      raw_event: {},
      agent_type: 'cc',
      broadcast_ts: Date.now(),
    }
    useAgentStore.getState().handleHookEvent(H, 'dev', event)
    expect(useAgentStore.getState().activeSubagents[`${H}:dev`]).toBeUndefined()
  })

  it('SubagentStart → adds agent_id to activeSubagents', () => {
    const event: AgentHookEvent = {
      tmux_session: 'dev',
      event_name: 'SubagentStart',
      raw_event: { agent_id: 'agent-A', agent_type: 'Explore' },
      agent_type: 'cc',
      broadcast_ts: Date.now(),
    }
    useAgentStore.getState().handleHookEvent(H, 'dev', event)
    expect(useAgentStore.getState().activeSubagents[`${H}:dev`]).toEqual(['agent-A'])
  })

  it('SubagentStart → does not duplicate agent_id', () => {
    useAgentStore.setState({ activeSubagents: { [`${H}:dev`]: ['agent-A'] } })
    const event: AgentHookEvent = {
      tmux_session: 'dev',
      event_name: 'SubagentStart',
      raw_event: { agent_id: 'agent-A', agent_type: 'Explore' },
      agent_type: 'cc',
      broadcast_ts: Date.now(),
    }
    useAgentStore.getState().handleHookEvent(H, 'dev', event)
    expect(useAgentStore.getState().activeSubagents[`${H}:dev`]).toEqual(['agent-A'])
  })

  it('SubagentStop → removes agent_id from activeSubagents', () => {
    useAgentStore.setState({ activeSubagents: { [`${H}:dev`]: ['agent-A', 'agent-B'] } })
    const event: AgentHookEvent = {
      tmux_session: 'dev',
      event_name: 'SubagentStop',
      raw_event: { agent_id: 'agent-A', agent_type: 'Explore' },
      agent_type: 'cc',
      broadcast_ts: Date.now(),
    }
    useAgentStore.getState().handleHookEvent(H, 'dev', event)
    expect(useAgentStore.getState().activeSubagents[`${H}:dev`]).toEqual(['agent-B'])
  })

  it('SubagentStop → removes session key when last agent removed', () => {
    useAgentStore.setState({ activeSubagents: { [`${H}:dev`]: ['agent-A'] } })
    const event: AgentHookEvent = {
      tmux_session: 'dev',
      event_name: 'SubagentStop',
      raw_event: { agent_id: 'agent-A', agent_type: 'Explore' },
      agent_type: 'cc',
      broadcast_ts: Date.now(),
    }
    useAgentStore.getState().handleHookEvent(H, 'dev', event)
    expect(useAgentStore.getState().activeSubagents[`${H}:dev`]).toBeUndefined()
  })

  it('SubagentStart/Stop → does not change main status', () => {
    useAgentStore.setState({ statuses: { [`${H}:dev`]: 'idle' } })
    const start: AgentHookEvent = {
      tmux_session: 'dev',
      event_name: 'SubagentStart',
      raw_event: { agent_id: 'agent-A', agent_type: 'Explore' },
      agent_type: 'cc',
      broadcast_ts: Date.now(),
    }
    useAgentStore.getState().handleHookEvent(H, 'dev', start)
    expect(useAgentStore.getState().statuses[`${H}:dev`]).toBe('idle')

    const stop: AgentHookEvent = {
      tmux_session: 'dev',
      event_name: 'SubagentStop',
      raw_event: { agent_id: 'agent-A', agent_type: 'Explore' },
      agent_type: 'cc',
      broadcast_ts: Date.now(),
    }
    useAgentStore.getState().handleHookEvent(H, 'dev', stop)
    expect(useAgentStore.getState().statuses[`${H}:dev`]).toBe('idle')
  })

  it('SessionStart → clears stale activeSubagents', () => {
    useAgentStore.setState({ activeSubagents: { [`${H}:dev`]: ['agent-A'] } })
    const event: AgentHookEvent = {
      tmux_session: 'dev',
      event_name: 'SessionStart',
      raw_event: { source: 'startup' },
      agent_type: 'cc',
      broadcast_ts: Date.now(),
    }
    useAgentStore.getState().handleHookEvent(H, 'dev', event)
    expect(useAgentStore.getState().activeSubagents[`${H}:dev`]).toBeUndefined()
  })

  it('SessionStart(compact) → does NOT clear activeSubagents', () => {
    useAgentStore.setState({ activeSubagents: { [`${H}:dev`]: ['agent-A'] } })
    const event: AgentHookEvent = {
      tmux_session: 'dev',
      event_name: 'SessionStart',
      raw_event: { source: 'compact' },
      agent_type: 'cc',
      broadcast_ts: Date.now(),
    }
    useAgentStore.getState().handleHookEvent(H, 'dev', event)
    expect(useAgentStore.getState().activeSubagents[`${H}:dev`]).toEqual(['agent-A'])
  })

  it('SubagentStart without agent_id → ignored', () => {
    const event: AgentHookEvent = {
      tmux_session: 'dev',
      event_name: 'SubagentStart',
      raw_event: { agent_type: 'Explore' },
      agent_type: 'cc',
      broadcast_ts: Date.now(),
    }
    useAgentStore.getState().handleHookEvent(H, 'dev', event)
    expect(useAgentStore.getState().activeSubagents[`${H}:dev`]).toBeUndefined()
    expect(useAgentStore.getState().events[`${H}:dev`]).toBeUndefined()
  })

  it('SubagentStop without agent_id → ignored', () => {
    useAgentStore.setState({ activeSubagents: { [`${H}:dev`]: ['agent-A'] } })
    const event: AgentHookEvent = {
      tmux_session: 'dev',
      event_name: 'SubagentStop',
      raw_event: { agent_type: 'Explore' },
      agent_type: 'cc',
      broadcast_ts: Date.now(),
    }
    useAgentStore.getState().handleHookEvent(H, 'dev', event)
    // activeSubagents unchanged — agent-A still there
    expect(useAgentStore.getState().activeSubagents[`${H}:dev`]).toEqual(['agent-A'])
  })

  it('error status is not downgraded by Notification(idle_prompt)', () => {
    useAgentStore.setState({ statuses: { [`${H}:dev`]: 'error' } })
    const event: AgentHookEvent = {
      tmux_session: 'dev',
      event_name: 'Notification',
      raw_event: { notification_type: 'idle_prompt' },
      agent_type: 'cc',
      broadcast_ts: Date.now(),
    }
    useAgentStore.getState().handleHookEvent(H, 'dev', event)
    expect(useAgentStore.getState().statuses[`${H}:dev`]).toBe('error')
  })

  it('error status is not downgraded by Notification(auth_success)', () => {
    useAgentStore.setState({ statuses: { [`${H}:dev`]: 'error' } })
    const event: AgentHookEvent = {
      tmux_session: 'dev',
      event_name: 'Notification',
      raw_event: { notification_type: 'auth_success' },
      agent_type: 'cc',
      broadcast_ts: Date.now(),
    }
    useAgentStore.getState().handleHookEvent(H, 'dev', event)
    expect(useAgentStore.getState().statuses[`${H}:dev`]).toBe('error')
  })

  it('error status IS cleared by UserPromptSubmit', () => {
    useAgentStore.setState({ statuses: { [`${H}:dev`]: 'error' } })
    const event: AgentHookEvent = {
      tmux_session: 'dev',
      event_name: 'UserPromptSubmit',
      raw_event: {},
      agent_type: 'cc',
      broadcast_ts: Date.now(),
    }
    useAgentStore.getState().handleHookEvent(H, 'dev', event)
    expect(useAgentStore.getState().statuses[`${H}:dev`]).toBe('running')
  })

  it('stores raw_event data', () => {
    const rawEvent = { session_id: 'abc123', model: 'opus-4', extra: [1, 2, 3] }
    const event: AgentHookEvent = {
      tmux_session: 'dev',
      event_name: 'UserPromptSubmit',
      raw_event: rawEvent,
      agent_type: 'cc',
      broadcast_ts: Date.now(),
    }
    useAgentStore.getState().handleHookEvent(H, 'dev', event)
    expect(useAgentStore.getState().events[`${H}:dev`].raw_event).toEqual(rawEvent)
  })

  it('clearSubagentsForHost → clears only matching host entries', () => {
    useAgentStore.setState({
      activeSubagents: {
        [`${H}:dev`]: ['agent-A'],
        [`${H}:staging`]: ['agent-B'],
        ['other-host:dev']: ['agent-C'],
      },
    })
    useAgentStore.getState().clearSubagentsForHost(H)
    expect(useAgentStore.getState().activeSubagents[`${H}:dev`]).toBeUndefined()
    expect(useAgentStore.getState().activeSubagents[`${H}:staging`]).toBeUndefined()
    expect(useAgentStore.getState().activeSubagents['other-host:dev']).toEqual(['agent-C'])
  })
})
