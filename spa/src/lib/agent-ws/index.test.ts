import { describe, it, expect, vi, beforeEach } from 'vitest'
import { dispatchAgentWsEvent, isAgentWsEvent, AGENT_WS_EVENT_TYPES } from './index'
import * as statusMod from './status-dispatch'
import * as pathHintMod from './path-hint-dispatch'
import type { HostEvent } from '../host-events'

beforeEach(() => {
  vi.restoreAllMocks()
})

describe('dispatchAgentWsEvent — explicit whitelist routing', () => {
  it('routes agent.status to handleStatusEvent', () => {
    const spy = vi.spyOn(statusMod, 'handleStatusEvent').mockImplementation(() => undefined)
    dispatchAgentWsEvent('h1', { type: 'agent.status', session: 's', value: '{}' })
    expect(spy).toHaveBeenCalledOnce()
    expect(spy).toHaveBeenCalledWith('h1', expect.objectContaining({ type: 'agent.status' }))
  })

  it('routes agent.status.cleared to handleStatusEvent', () => {
    const spy = vi.spyOn(statusMod, 'handleStatusEvent').mockImplementation(() => undefined)
    dispatchAgentWsEvent('h1', { type: 'agent.status.cleared', session: '', value: '' })
    expect(spy).toHaveBeenCalledOnce()
  })

  it('routes agent.path_hint to handlePathHintEvent', () => {
    const spy = vi.spyOn(pathHintMod, 'handlePathHintEvent').mockImplementation(() => undefined)
    dispatchAgentWsEvent('h1', { type: 'agent.path_hint', session: 's', value: '{}' })
    expect(spy).toHaveBeenCalledOnce()
    expect(spy).toHaveBeenCalledWith('h1', expect.objectContaining({ type: 'agent.path_hint' }))
  })

  it('drops unknown agent.* types (no broad startsWith filter)', () => {
    const statusSpy = vi.spyOn(statusMod, 'handleStatusEvent').mockImplementation(() => undefined)
    const pathSpy = vi.spyOn(pathHintMod, 'handlePathHintEvent').mockImplementation(() => undefined)
    // Cast — agent.foo is intentionally not in the HostEvent.type union; the
    // router must still tolerate it without dispatching anywhere.
    dispatchAgentWsEvent('h1', { type: 'agent.foo' as HostEvent['type'], session: 's', value: '{}' })
    expect(statusSpy).not.toHaveBeenCalled()
    expect(pathSpy).not.toHaveBeenCalled()
  })

  it('drops non-agent event types', () => {
    const statusSpy = vi.spyOn(statusMod, 'handleStatusEvent').mockImplementation(() => undefined)
    const pathSpy = vi.spyOn(pathHintMod, 'handlePathHintEvent').mockImplementation(() => undefined)
    dispatchAgentWsEvent('h1', { type: 'hook', session: 's', value: '{}' })
    expect(statusSpy).not.toHaveBeenCalled()
    expect(pathSpy).not.toHaveBeenCalled()
  })
})

describe('isAgentWsEvent + AGENT_WS_EVENT_TYPES', () => {
  it('returns true for every type in AGENT_WS_EVENT_TYPES', () => {
    for (const t of AGENT_WS_EVENT_TYPES) {
      expect(isAgentWsEvent(t)).toBe(true)
    }
  })

  it('returns false for non-listed types', () => {
    expect(isAgentWsEvent('hook')).toBe(false)
    expect(isAgentWsEvent('handoff')).toBe(false)
    // Cast — agent.foo is intentionally not in the union
    expect(isAgentWsEvent('agent.foo' as HostEvent['type'])).toBe(false)
  })

  it('whitelist matches dispatcher routing exactly', () => {
    // Every whitelisted type must route somewhere; no whitelist-but-no-route gap.
    const statusSpy = vi.spyOn(statusMod, 'handleStatusEvent').mockImplementation(() => undefined)
    const pathSpy = vi.spyOn(pathHintMod, 'handlePathHintEvent').mockImplementation(() => undefined)
    for (const t of AGENT_WS_EVENT_TYPES) {
      dispatchAgentWsEvent('h1', { type: t, session: 's', value: '{}' })
    }
    expect(statusSpy.mock.calls.length + pathSpy.mock.calls.length).toBe(AGENT_WS_EVENT_TYPES.length)
  })
})
