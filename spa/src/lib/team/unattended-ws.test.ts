// spa/src/lib/team/unattended-ws.test.ts — the daemon's `team.unattended` host event (plan PU-2a):
// {op:"snapshot"|"changed", state} is checked whole; a good frame sets the host's state and proves support.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { useUnattendedStore } from '../../stores/useUnattendedStore'
import { handleUnattendedEvent } from './unattended-ws'

const state = { on: true, since: 1_000, changed_at: 1_000, changed_by: { kind: 'app', label: 'Purdex.app', addr: '100.64.0.4:51234' } }
const v = (o: unknown) => JSON.stringify(o)

let warn: ReturnType<typeof vi.spyOn>

beforeEach(() => {
  useUnattendedStore.getState().reset()
  warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
})
afterEach(() => warn.mockRestore())

describe('handleUnattendedEvent', () => {
  it('a snapshot applies the state and sets support yes', () => {
    handleUnattendedEvent('h1', v({ op: 'snapshot', state }))
    expect(useUnattendedStore.getState().byHost.h1).toEqual({ support: 'yes', state })
    expect(warn).not.toHaveBeenCalled()
  })

  it('a changed replaces it (another window pressed the button)', () => {
    handleUnattendedEvent('h1', v({ op: 'snapshot', state }))
    const off = { on: false, since: 1_000, changed_at: 2_000, changed_by: { kind: 'app', label: 'Purdex.app @ air26' } }
    handleUnattendedEvent('h1', v({ op: 'changed', state: off }))
    expect(useUnattendedStore.getState().byHost.h1).toEqual({ support: 'yes', state: off })
  })

  it('a never-written switch (no changed_by, zero times) is a valid off', () => {
    handleUnattendedEvent('h1', v({ op: 'snapshot', state: { on: false, since: 0, changed_at: 0 } }))
    expect(useUnattendedStore.getState().byHost.h1?.state).toEqual({ on: false, since: 0, changed_at: 0 })
  })

  it('a value already parsed (an object) is accepted too', () => {
    handleUnattendedEvent('h1', { op: 'changed', state })
    expect(useUnattendedStore.getState().byHost.h1?.state?.on).toBe(true)
  })

  it.each([
    ['on is a string', { op: 'changed', state: { ...state, on: 'true' } }],
    ['since is missing', { op: 'snapshot', state: { on: true, changed_at: 1_000 } }],
    ['changed_at is not finite', { op: 'snapshot', state: { ...state, changed_at: null } }],
    ['changed_by is not a record', { op: 'changed', state: { ...state, changed_by: 'app' } }],
    ['changed_by is an empty record', { op: 'changed', state: { ...state, changed_by: {} } }],
    ['changed_by.kind is not a string', { op: 'changed', state: { ...state, changed_by: { kind: 1, label: 'Purdex.app' } } }],
    ['changed_by.kind is empty', { op: 'changed', state: { ...state, changed_by: { kind: '', label: 'Purdex.app' } } }],
    ['changed_by.label is empty', { op: 'changed', state: { ...state, changed_by: { kind: 'app', label: '' } } }],
    ['changed_by.addr is a number', { op: 'changed', state: { ...state, changed_by: { kind: 'app', label: 'Purdex.app', addr: 51234 } } }],
    ['the op is unknown', { op: 'x', state }],
    ['the state is missing', { op: 'snapshot' }],
  ])('a frame whose %s is dropped whole, with one warning, and proves nothing', (_what, frame) => {
    handleUnattendedEvent('h1', v({ op: 'snapshot', state: { on: false, since: 0, changed_at: 0 } }))
    useUnattendedStore.getState().setSupport('h1', 'unknown')
    handleUnattendedEvent('h1', v(frame))
    expect(useUnattendedStore.getState().byHost.h1).toEqual({ support: 'unknown', state: { on: false, since: 0, changed_at: 0 } })
    expect(warn).toHaveBeenCalledTimes(1)
  })

  it('a value that is not JSON or not an object is dropped with one warning', () => {
    handleUnattendedEvent('h1', 'not json')
    handleUnattendedEvent('h1', v([1, 2]))
    expect(useUnattendedStore.getState().byHost).toEqual({})
    expect(warn).toHaveBeenCalledTimes(2)
  })
})
