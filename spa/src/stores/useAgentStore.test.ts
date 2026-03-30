// spa/src/stores/useAgentStore.test.ts
import { describe, it, expect, beforeEach } from 'vitest'
import { useAgentStore } from './useAgentStore'
import { useTabStore } from './useTabStore'
import { createTab } from '../types/tab'
import type { AgentHookEvent } from './useAgentStore'

beforeEach(() => {
  useAgentStore.setState({
    events: {},
    statuses: {},
    unread: {},
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
    useAgentStore.getState().handleHookEvent('dev', event)
    expect(useAgentStore.getState().statuses['dev']).toBe('running')
  })

  it('Notification(permission_prompt) → status = waiting', () => {
    const event: AgentHookEvent = {
      tmux_session: 'dev',
      event_name: 'Notification',
      raw_event: { notification_type: 'permission_prompt' },
      agent_type: 'cc',
      broadcast_ts: Date.now(),
    }
    useAgentStore.getState().handleHookEvent('dev', event)
    expect(useAgentStore.getState().statuses['dev']).toBe('waiting')
  })

  it('Notification(elicitation_dialog) → status = waiting', () => {
    const event: AgentHookEvent = {
      tmux_session: 'dev',
      event_name: 'Notification',
      raw_event: { notification_type: 'elicitation_dialog' },
      agent_type: 'cc',
      broadcast_ts: Date.now(),
    }
    useAgentStore.getState().handleHookEvent('dev', event)
    expect(useAgentStore.getState().statuses['dev']).toBe('waiting')
  })

  it('Notification(idle_prompt) → status = idle (from running)', () => {
    useAgentStore.setState({ statuses: { dev: 'running' } })
    const event: AgentHookEvent = {
      tmux_session: 'dev',
      event_name: 'Notification',
      raw_event: { notification_type: 'idle_prompt' },
      agent_type: 'cc',
      broadcast_ts: Date.now(),
    }
    useAgentStore.getState().handleHookEvent('dev', event)
    expect(useAgentStore.getState().statuses['dev']).toBe('idle')
  })

  it('Notification(auth_success) → status = idle', () => {
    useAgentStore.setState({ statuses: { dev: 'running' } })
    const event: AgentHookEvent = {
      tmux_session: 'dev',
      event_name: 'Notification',
      raw_event: { notification_type: 'auth_success' },
      agent_type: 'cc',
      broadcast_ts: Date.now(),
    }
    useAgentStore.getState().handleHookEvent('dev', event)
    expect(useAgentStore.getState().statuses['dev']).toBe('idle')
  })

  it('Notification without notification_type → does not change status', () => {
    useAgentStore.setState({ statuses: { dev: 'idle' } })
    const event: AgentHookEvent = {
      tmux_session: 'dev',
      event_name: 'Notification',
      raw_event: {},
      agent_type: 'cc',
      broadcast_ts: Date.now(),
    }
    useAgentStore.getState().handleHookEvent('dev', event)
    expect(useAgentStore.getState().statuses['dev']).toBe('idle')
  })

  it('SessionStart(compact) → does not change status', () => {
    useAgentStore.setState({ statuses: { dev: 'idle' } })
    const event: AgentHookEvent = {
      tmux_session: 'dev',
      event_name: 'SessionStart',
      raw_event: { source: 'compact' },
      agent_type: 'cc',
      broadcast_ts: Date.now(),
    }
    useAgentStore.getState().handleHookEvent('dev', event)
    expect(useAgentStore.getState().statuses['dev']).toBe('idle')
  })

  it('SessionStart(startup) → status = running', () => {
    const event: AgentHookEvent = {
      tmux_session: 'dev',
      event_name: 'SessionStart',
      raw_event: { source: 'startup' },
      agent_type: 'cc',
      broadcast_ts: Date.now(),
    }
    useAgentStore.getState().handleHookEvent('dev', event)
    expect(useAgentStore.getState().statuses['dev']).toBe('running')
  })

  it('SessionStart(resume) → status = running', () => {
    const event: AgentHookEvent = {
      tmux_session: 'dev',
      event_name: 'SessionStart',
      raw_event: { source: 'resume' },
      agent_type: 'cc',
      broadcast_ts: Date.now(),
    }
    useAgentStore.getState().handleHookEvent('dev', event)
    expect(useAgentStore.getState().statuses['dev']).toBe('running')
  })

  it('StopFailure → status = idle', () => {
    useAgentStore.setState({ statuses: { dev: 'running' } })
    const event: AgentHookEvent = {
      tmux_session: 'dev',
      event_name: 'StopFailure',
      raw_event: { error: 'rate_limit' },
      agent_type: 'cc',
      broadcast_ts: Date.now(),
    }
    useAgentStore.getState().handleHookEvent('dev', event)
    expect(useAgentStore.getState().statuses['dev']).toBe('idle')
  })

  it('Stop → status = idle', () => {
    const event: AgentHookEvent = {
      tmux_session: 'dev',
      event_name: 'Stop',
      raw_event: {},
      agent_type: 'cc',
      broadcast_ts: Date.now(),
    }
    useAgentStore.getState().handleHookEvent('dev', event)
    expect(useAgentStore.getState().statuses['dev']).toBe('idle')
  })

  it('Stop → marks unread when active tab is not this session', () => {
    const event: AgentHookEvent = {
      tmux_session: 'dev',
      event_name: 'Stop',
      raw_event: {},
      agent_type: 'cc',
      broadcast_ts: Date.now(),
    }
    useAgentStore.getState().handleHookEvent('dev', event)
    expect(useAgentStore.getState().unread['dev']).toBe(true)
  })

  it('Stop → does NOT mark unread when active tab is this session', () => {
    const tab = { ...createTab({ kind: 'session', sessionCode: 'dev', mode: 'terminal' }), id: 't1' }
    useTabStore.setState({ tabs: { t1: tab }, activeTabId: 't1' })
    const event: AgentHookEvent = {
      tmux_session: 'dev',
      event_name: 'Stop',
      raw_event: {},
      agent_type: 'cc',
      broadcast_ts: Date.now(),
    }
    useAgentStore.getState().handleHookEvent('dev', event)
    expect(useAgentStore.getState().unread['dev']).toBeUndefined()
  })

  it('Notification(idle_prompt) → does not mark unread', () => {
    const event: AgentHookEvent = {
      tmux_session: 'dev',
      event_name: 'Notification',
      raw_event: { notification_type: 'idle_prompt' },
      agent_type: 'cc',
      broadcast_ts: Date.now(),
    }
    useAgentStore.getState().handleHookEvent('dev', event)
    expect(useAgentStore.getState().unread['dev']).toBeUndefined()
  })

  it('Notification(auth_success) → does not mark unread', () => {
    const event: AgentHookEvent = {
      tmux_session: 'dev',
      event_name: 'Notification',
      raw_event: { notification_type: 'auth_success' },
      agent_type: 'cc',
      broadcast_ts: Date.now(),
    }
    useAgentStore.getState().handleHookEvent('dev', event)
    expect(useAgentStore.getState().unread['dev']).toBeUndefined()
  })

  it('StopFailure → marks unread when not focused', () => {
    const event: AgentHookEvent = {
      tmux_session: 'dev',
      event_name: 'StopFailure',
      raw_event: { error: 'rate_limit' },
      agent_type: 'cc',
      broadcast_ts: Date.now(),
    }
    useAgentStore.getState().handleHookEvent('dev', event)
    expect(useAgentStore.getState().unread['dev']).toBe(true)
  })

  it('Notification(permission_prompt) → marks unread when not focused', () => {
    const event: AgentHookEvent = {
      tmux_session: 'dev',
      event_name: 'Notification',
      raw_event: { notification_type: 'permission_prompt' },
      agent_type: 'cc',
      broadcast_ts: Date.now(),
    }
    useAgentStore.getState().handleHookEvent('dev', event)
    expect(useAgentStore.getState().unread['dev']).toBe(true)
  })

  it('markRead → clears unread', () => {
    // Set up unread state
    useAgentStore.setState({ unread: { dev: true } })
    useAgentStore.getState().markRead('dev')
    expect(useAgentStore.getState().unread['dev']).toBeUndefined()
  })

  it('SessionEnd → clears status', () => {
    // Set up some state for the session
    useAgentStore.setState({
      events: { dev: { tmux_session: 'dev', event_name: 'Stop', raw_event: {}, agent_type: 'cc', broadcast_ts: Date.now() } },
      statuses: { dev: 'idle' },
      unread: { dev: true },
    })
    const event: AgentHookEvent = {
      tmux_session: 'dev',
      event_name: 'SessionEnd',
      raw_event: {},
      agent_type: 'cc',
      broadcast_ts: Date.now(),
    }
    useAgentStore.getState().handleHookEvent('dev', event)
    expect(useAgentStore.getState().statuses['dev']).toBeUndefined()
    expect(useAgentStore.getState().events['dev']).toBeUndefined()
    expect(useAgentStore.getState().unread['dev']).toBeUndefined()
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
    useAgentStore.getState().handleHookEvent('dev', event)
    expect(useAgentStore.getState().events['dev'].raw_event).toEqual(rawEvent)
  })
})
