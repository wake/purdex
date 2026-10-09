// spa/src/stores/useUnattendedStore.test.ts — per host: does its daemon support 無人值守模式, and what does its switch
// say (plan PU-2a). Pure data, one per renderer.
import { describe, it, expect, beforeEach } from 'vitest'
import { useUnattendedStore } from './useUnattendedStore'
import type { UnattendedState } from '../lib/team/types'

const on: UnattendedState = { on: true, since: 1_000, changed_at: 1_000, changed_by: { kind: 'app', label: 'Purdex.app' } }
const off: UnattendedState = { on: false, since: 1_000, changed_at: 2_000 }

beforeEach(() => useUnattendedStore.getState().reset())

describe('useUnattendedStore', () => {
  it('starts empty: a host never heard of has no entry', () => {
    expect(useUnattendedStore.getState().byHost).toEqual({})
  })

  it('applyState records the state; support stays what it was (unknown for a new host)', () => {
    useUnattendedStore.getState().applyState('h1', on)
    expect(useUnattendedStore.getState().byHost.h1).toEqual({ support: 'unknown', state: on })
  })

  it('applyState replaces the previous state; setSupport keeps it', () => {
    const s = useUnattendedStore.getState()
    s.applyState('h1', on)
    s.setSupport('h1', 'yes')
    s.applyState('h1', off)
    expect(useUnattendedStore.getState().byHost.h1).toEqual({ support: 'yes', state: off })
    s.setSupport('h1', 'no')
    expect(useUnattendedStore.getState().byHost.h1).toEqual({ support: 'no', state: off })
  })

  it('setSupport on a new host has no state', () => {
    useUnattendedStore.getState().setSupport('h2', 'no')
    expect(useUnattendedStore.getState().byHost.h2).toEqual({ support: 'no' })
  })

  it('forgetHost drops only that host', () => {
    const s = useUnattendedStore.getState()
    s.applyState('h1', on)
    s.applyState('h2', off)
    s.forgetHost('h1')
    expect(useUnattendedStore.getState().byHost).toEqual({ h2: { support: 'unknown', state: off } })
    s.forgetHost('nope')
    expect(Object.keys(useUnattendedStore.getState().byHost)).toEqual(['h2'])
  })

  it('a write that changes nothing keeps the same object (no re-render)', () => {
    const s = useUnattendedStore.getState()
    s.setSupport('h1', 'yes')
    const before = useUnattendedStore.getState().byHost
    s.setSupport('h1', 'yes')
    s.forgetHost('nope')
    expect(useUnattendedStore.getState().byHost).toBe(before)
  })
})

describe('quotaSupport (relay quota)', () => {
  beforeEach(() => useUnattendedStore.getState().reset())

  it('is stored per host, is unaffected by a state frame, and goes with the host', () => {
    const st = useUnattendedStore.getState()
    st.setQuotaSupport('h1', 'yes')
    expect(useUnattendedStore.getState().byHost.h1.quotaSupport).toBe('yes')
    st.applyState('h1', { on: true, since: 1, changed_at: 1 })
    expect(useUnattendedStore.getState().byHost.h1.quotaSupport).toBe('yes') // a snapshot must not forget what the probe said
    st.setSupport('h1', 'yes')
    expect(useUnattendedStore.getState().byHost.h1.quotaSupport).toBe('yes')
    st.forgetHost('h1')
    expect(useUnattendedStore.getState().byHost.h1).toBeUndefined()
  })

  it('setting the same value does not change the store object', () => {
    const st = useUnattendedStore.getState()
    st.setQuotaSupport('h1', 'no')
    const before = useUnattendedStore.getState().byHost
    st.setQuotaSupport('h1', 'no')
    expect(useUnattendedStore.getState().byHost).toBe(before)
  })
})
