// spa/src/stores/useQuickReplyPrefsStore.test.ts — the Mac App's quick-reply switches (quick-replies v2 spec §3, QR-2b): off-lists keyed by WIRE host id,
// a sanitising merge for every arrival, pruning and the re-pass's re-key.
import { beforeEach, describe, expect, it, vi } from 'vitest'

const registerSpy = vi.hoisted(() => vi.fn())

vi.mock('../lib/storage/sync', () => ({
  syncManager: { register: registerSpy, notify: vi.fn(), destroy: vi.fn() },
  createSyncManager: vi.fn(),
}))

import { useQuickReplyPrefsStore, sanitizeDisabled } from './useQuickReplyPrefsStore'
import { STORAGE_KEYS } from '../lib/storage/keys'

/** Writes `state` as the persisted value and runs the store's own `merge` over it (what another window's write, or a Profile apply, does). */
async function rehydrateFrom(state: unknown): Promise<void> {
  localStorage.setItem(STORAGE_KEYS.QUICK_REPLY_PREFS, JSON.stringify({ state, version: 1 }))
  await useQuickReplyPrefsStore.persist.rehydrate()
}

beforeEach(() => {
  localStorage.clear()
  useQuickReplyPrefsStore.setState({ rowEnabled: true, disabled: {} })
})

describe('useQuickReplyPrefsStore — shape', () => {
  it('defaults to the row on and nothing switched off', () => {
    const s = useQuickReplyPrefsStore.getInitialState()
    expect([s.rowEnabled, s.disabled]).toEqual([true, {}])
  })

  it('persists under purdex-quick-reply-prefs, the two fields only', () => {
    expect(STORAGE_KEYS.QUICK_REPLY_PREFS).toBe('purdex-quick-reply-prefs')
    useQuickReplyPrefsStore.getState().setRowEnabled(false)
    useQuickReplyPrefsStore.getState().setItemDisabled('d1_a', 'x', true)
    const persisted = JSON.parse(localStorage.getItem(STORAGE_KEYS.QUICK_REPLY_PREFS) ?? '{}') as { state: Record<string, unknown> }
    expect(persisted.state).toEqual({ rowEnabled: false, disabled: { d1_a: ['x'] } })
  })

  it('is registered with syncManager under its storage key (cross-window)', () => {
    expect(registerSpy).toHaveBeenCalledWith(STORAGE_KEYS.QUICK_REPLY_PREFS, useQuickReplyPrefsStore)
  })
})

describe('useQuickReplyPrefsStore — merge sanitises every arrival', () => {
  it('keeps a valid record as it is', async () => {
    await rehydrateFrom({ rowEnabled: false, disabled: { d1_a: ['x', 'y'], 'k3Jq-local_9': ['z'] } })
    expect(useQuickReplyPrefsStore.getState()).toMatchObject({ rowEnabled: false, disabled: { d1_a: ['x', 'y'], 'k3Jq-local_9': ['z'] } })
  })

  it('drops unknown fields; a non-boolean rowEnabled reads as the default (on)', async () => {
    await rehydrateFrom({ rowEnabled: 'no', extra: 1, disabled: { d1_a: ['x'] } })
    const s = useQuickReplyPrefsStore.getState() as unknown as Record<string, unknown>
    expect(s.rowEnabled).toBe(true)
    expect(s.disabled).toEqual({ d1_a: ['x'] })
    expect(s).not.toHaveProperty('extra')
  })

  it('drops non-string ids and collapses duplicates (first one kept); a list that is not a list, or empties out, is no entry', () => {
    expect(sanitizeDisabled({ d1_a: ['x', 3, null, 'x', 'y', { id: 'z' }], d1_b: 'x', d1_c: [], d1_d: [1] })).toEqual({ d1_a: ['x', 'y'] })
    expect(sanitizeDisabled([])).toEqual({})
    expect(sanitizeDisabled(null)).toEqual({})
  })

  it('never copies a __proto__ key', async () => {
    localStorage.setItem(STORAGE_KEYS.QUICK_REPLY_PREFS, '{"state":{"rowEnabled":true,"disabled":{"__proto__":["evil"],"d1_a":["ok"]}},"version":1}')
    await useQuickReplyPrefsStore.persist.rehydrate()
    const { disabled } = useQuickReplyPrefsStore.getState()
    expect(Object.keys(disabled)).toEqual(['d1_a'])
    expect(Object.getPrototypeOf(disabled)).toBe(Object.prototype)
  })

  it('a key naming a host this Mac lacks round-trips untouched (keys are opaque wire ids)', async () => {
    await rehydrateFrom({ rowEnabled: true, disabled: { d1_far_away: ['a'] } })
    useQuickReplyPrefsStore.getState().setItemDisabled('d1_here', 'b', true)
    expect(useQuickReplyPrefsStore.getState().disabled).toEqual({ d1_far_away: ['a'], d1_here: ['b'] })
  })

  it('another window\'s write (a rehydrate) replaces what memory held', async () => {
    useQuickReplyPrefsStore.getState().setItemDisabled('d1_a', 'old', true)
    await rehydrateFrom({ rowEnabled: false, disabled: { d1_a: ['new'] } })
    expect(useQuickReplyPrefsStore.getState()).toMatchObject({ rowEnabled: false, disabled: { d1_a: ['new'] } })
  })

  it('nothing stored keeps memory', async () => {
    useQuickReplyPrefsStore.getState().setRowEnabled(false)
    localStorage.clear()
    await useQuickReplyPrefsStore.persist.rehydrate()
    expect(useQuickReplyPrefsStore.getState().rowEnabled).toBe(false)
  })
})

describe('useQuickReplyPrefsStore — actions', () => {
  it('setItemDisabled adds to / removes from the off-list; the same state back writes nothing; an emptied list is removed', () => {
    const { setItemDisabled } = useQuickReplyPrefsStore.getState()
    setItemDisabled('d1_a', 'x', true)
    setItemDisabled('d1_a', 'y', true)
    expect(useQuickReplyPrefsStore.getState().disabled).toEqual({ d1_a: ['x', 'y'] })
    const before = useQuickReplyPrefsStore.getState()
    setItemDisabled('d1_a', 'x', true)
    setItemDisabled('d1_a', 'nope', false)
    expect(useQuickReplyPrefsStore.getState()).toBe(before)
    setItemDisabled('d1_a', 'x', false)
    setItemDisabled('d1_a', 'y', false)
    expect(useQuickReplyPrefsStore.getState().disabled).toEqual({})
  })

  it('setRowEnabled flips the app-wide switch', () => {
    useQuickReplyPrefsStore.getState().setRowEnabled(false)
    expect(useQuickReplyPrefsStore.getState().rowEnabled).toBe(false)
    const before = useQuickReplyPrefsStore.getState()
    useQuickReplyPrefsStore.getState().setRowEnabled(false)
    expect(useQuickReplyPrefsStore.getState()).toBe(before)
  })

  it('prune keeps the intersection with the ids given; nothing to drop writes nothing; an emptied list is removed; other hosts are untouched', () => {
    useQuickReplyPrefsStore.setState({ disabled: { d1_a: ['x', 'y', 'z'], d1_b: ['q'] } })
    useQuickReplyPrefsStore.getState().prune('d1_a', ['x', 'z', 'other'])
    expect(useQuickReplyPrefsStore.getState().disabled).toEqual({ d1_a: ['x', 'z'], d1_b: ['q'] })
    const before = useQuickReplyPrefsStore.getState()
    useQuickReplyPrefsStore.getState().prune('d1_a', ['x', 'z'])
    useQuickReplyPrefsStore.getState().prune('d1_none', [])
    expect(useQuickReplyPrefsStore.getState()).toBe(before)
    useQuickReplyPrefsStore.getState().prune('d1_a', [])
    expect(useQuickReplyPrefsStore.getState().disabled).toEqual({ d1_b: ['q'] })
  })

  it('rekey moves from → to; an existing `to` wins and `from` is dropped; a missing `from` is a no-op', () => {
    useQuickReplyPrefsStore.setState({ disabled: { local_1: ['a'], local_2: ['b'], d1_two: ['keep'] } })
    useQuickReplyPrefsStore.getState().rekey([['local_1', 'd1_one'], ['local_2', 'd1_two'], ['local_9', 'd1_nine']])
    expect(useQuickReplyPrefsStore.getState().disabled).toEqual({ d1_one: ['a'], d1_two: ['keep'] })
    const before = useQuickReplyPrefsStore.getState()
    useQuickReplyPrefsStore.getState().rekey([['local_9', 'd1_nine']])
    expect(useQuickReplyPrefsStore.getState()).toBe(before)
  })
})
