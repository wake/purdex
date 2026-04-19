// spa/src/lib/agent-ws-dispatch.test.ts
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { dispatchAgentWsEvent } from './agent-ws-dispatch'
import { useAgentStore } from '../stores/useAgentStore'
import { statuslineTestBus } from './statusline-test-bus'

const H = 'test-host'

beforeEach(() => {
  useAgentStore.setState({
    statuses: {},
    agentTypes: {},
    models: {},
    subagents: {},
    lastEvents: {},
    oscTitles: {},
    ccStatus: {},
    unread: {},
  })
  statuslineTestBus.reset()
})

describe('dispatchAgentWsEvent — wire-format guards', () => {
  it('agent.status event unwraps {agent_type,status} wire shape and stores inner status', () => {
    const status = { session_name: 'abc', model: { display_name: 'Sonnet' } }
    const wire = { agent_type: 'cc', status }
    dispatchAgentWsEvent(H, { type: 'agent.status', session: 'dev', value: JSON.stringify(wire) })
    const entry = useAgentStore.getState().ccStatus[`${H}:dev`]
    expect(entry?.raw).toEqual(status)
  })

  it('agent.status with malformed value is silently ignored', () => {
    dispatchAgentWsEvent(H, { type: 'agent.status', session: 'dev', value: 'not-json' })
    expect(useAgentStore.getState().ccStatus[`${H}:dev`]).toBeUndefined()
  })

  it('agent.status with non-object JSON (null/array/primitive) is ignored', () => {
    dispatchAgentWsEvent(H, { type: 'agent.status', session: 'dev', value: 'null' })
    dispatchAgentWsEvent(H, { type: 'agent.status', session: 'dev', value: '123' })
    dispatchAgentWsEvent(H, { type: 'agent.status', session: 'dev', value: '[1,2,3]' })
    dispatchAgentWsEvent(H, { type: 'agent.status', session: 'dev', value: '"bare"' })
    expect(useAgentStore.getState().ccStatus[`${H}:dev`]).toBeUndefined()
  })

  it('ignores agent.status with non-"cc" agent_type', () => {
    const wire = { agent_type: 'codex', status: { session_name: 'abc' } }
    dispatchAgentWsEvent(H, { type: 'agent.status', session: 'dev', value: JSON.stringify(wire) })
    expect(useAgentStore.getState().ccStatus[`${H}:dev`]).toBeUndefined()
  })

  it('ignores agent.status with missing status field', () => {
    const wire = { agent_type: 'cc' }
    dispatchAgentWsEvent(H, { type: 'agent.status', session: 'dev', value: JSON.stringify(wire) })
    expect(useAgentStore.getState().ccStatus[`${H}:dev`]).toBeUndefined()
  })

  it('ignores agent.status with non-object status field', () => {
    const wire = { agent_type: 'cc', status: 'not-an-object' }
    dispatchAgentWsEvent(H, { type: 'agent.status', session: 'dev', value: JSON.stringify(wire) })
    expect(useAgentStore.getState().ccStatus[`${H}:dev`]).toBeUndefined()
  })

  it('agent.status.cleared with empty session calls clearHostAgentStatus', () => {
    useAgentStore.getState().setCcStatus(H, 'dev', { session_name: 'stale' })
    dispatchAgentWsEvent(H, { type: 'agent.status.cleared', session: '', value: '{"agent_type":"cc"}' })
    expect(useAgentStore.getState().ccStatus[`${H}:dev`]).toBeUndefined()
  })
})

describe('dispatchAgentWsEvent — nonce routing (#481)', () => {
  it('routes agent.status for real sessions into setCcStatus only (no bus emit)', () => {
    const busSpy = vi.fn()
    statuslineTestBus.subscribe('__pdx_test_aaaa1111', busSpy)

    dispatchAgentWsEvent('h1', {
      type: 'agent.status',
      session: 'real-session-abc',
      value: JSON.stringify({ agent_type: 'cc', status: { model: { id: 'foo' } } }),
    })

    expect(useAgentStore.getState().ccStatus['h1:real-session-abc']).toBeDefined()
    expect(busSpy).not.toHaveBeenCalled()
  })

  it('routes agent.status for test nonce into setCcStatus AND emits bus event', () => {
    const busSpy = vi.fn()
    const nonce = '__pdx_test_aaaa1111'
    statuslineTestBus.subscribe(nonce, busSpy)

    dispatchAgentWsEvent('h1', {
      type: 'agent.status',
      session: nonce,
      value: JSON.stringify({ agent_type: 'cc', status: { model: { id: 'pipeline-test' } } }),
    })

    expect(useAgentStore.getState().ccStatus[`h1:${nonce}`]).toBeDefined()
    expect(busSpy).toHaveBeenCalledOnce()
    expect(busSpy).toHaveBeenCalledWith(expect.objectContaining({ nonce, hostId: 'h1' }))
  })

  it('agent.status.cleared with specific session clears only that entry', () => {
    const nonce = '__pdx_test_bbbb2222'
    useAgentStore.setState({
      ccStatus: {
        'h1:real-s1': { receivedAt: 0, raw: {} },
        [`h1:${nonce}`]: { receivedAt: 0, raw: {} },
      },
    })
    dispatchAgentWsEvent('h1', { type: 'agent.status.cleared', session: nonce, value: '' })
    expect(useAgentStore.getState().ccStatus[`h1:${nonce}`]).toBeUndefined()
    expect(useAgentStore.getState().ccStatus['h1:real-s1']).toBeDefined()
  })
})
