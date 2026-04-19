import { describe, it, expect, vi, beforeEach } from 'vitest'
import { dispatchAgentWsEvent } from './agent-ws-dispatch'
import { useAgentStore } from '../stores/useAgentStore'
import { statuslineTestBus } from './statusline-test-bus'

beforeEach(() => {
  useAgentStore.setState({ ccStatus: {}, oscTitles: {} })
  statuslineTestBus.reset()
})

describe('dispatchAgentWsEvent', () => {
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

  it('agent.status.cleared with empty session wipes whole host (existing behavior)', () => {
    useAgentStore.setState({ ccStatus: { 'h1:s1': { receivedAt: 0, raw: {} }, 'h1:s2': { receivedAt: 0, raw: {} } } })
    dispatchAgentWsEvent('h1', { type: 'agent.status.cleared', session: '', value: '' })
    expect(useAgentStore.getState().ccStatus).toEqual({})
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
