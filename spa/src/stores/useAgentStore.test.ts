// spa/src/stores/useAgentStore.test.ts
import { describe, it, expect, beforeEach } from 'vitest'
import { useAgentStore } from './useAgentStore'
import type { AgentHookEvent } from './useAgentStore'

beforeEach(() => {
  useAgentStore.setState({
    events: {},
    statuses: {},
    unread: {},
    focusedSession: null,
  })
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

  it('Notification → status = waiting', () => {
    const event: AgentHookEvent = {
      tmux_session: 'dev',
      event_name: 'Notification',
      raw_event: {},
      agent_type: 'cc',
      broadcast_ts: Date.now(),
    }
    useAgentStore.getState().handleHookEvent('dev', event)
    expect(useAgentStore.getState().statuses['dev']).toBe('waiting')
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

  it('Stop → marks unread when focusedSession is null', () => {
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
