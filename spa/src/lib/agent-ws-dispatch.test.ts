// spa/src/lib/agent-ws-dispatch.test.ts
import { describe, it, expect, beforeEach } from 'vitest'
import { dispatchAgentWsEvent } from './agent-ws-dispatch'
import { useAgentStore } from '../stores/useAgentStore'

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
    showOscTitle: false,
  })
})

describe('dispatchAgentWsEvent', () => {
  it('agent.status event calls setCcStatus with parsed value', () => {
    const payload = { session_name: 'abc', model: { display_name: 'Sonnet' } }
    dispatchAgentWsEvent(H, { type: 'agent.status', session: 'dev', value: JSON.stringify(payload) })
    const entry = useAgentStore.getState().ccStatus[`${H}:dev`]
    expect(entry?.raw).toEqual(payload)
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

  it('agent.status.cleared event calls clearHostAgentStatus', () => {
    useAgentStore.getState().setCcStatus(H, 'dev', { session_name: 'stale' })
    dispatchAgentWsEvent(H, { type: 'agent.status.cleared', session: '', value: '{"agent_type":"cc"}' })
    expect(useAgentStore.getState().ccStatus[`${H}:dev`]).toBeUndefined()
  })
})
