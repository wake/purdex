// spa/src/stores/useShownHostsStore.test.ts — the hosts a workbench enables (host ownership spec §4.1 / §4.5, plan
// H2d-1 T1, §0.6).
import { beforeEach, describe, expect, it, vi } from 'vitest'

const registerSpy = vi.hoisted(() => vi.fn())

vi.mock('../lib/storage/sync', () => ({
  syncManager: { register: registerSpy, notify: vi.fn(), destroy: vi.fn() },
  createSyncManager: vi.fn(),
}))

import { useShownHostsStore } from './useShownHostsStore'
import { STORAGE_KEYS } from '../lib/storage/keys'

/** Writes `state` as the persisted value and runs the store's own `merge` over it. */
async function rehydrateFrom(state: unknown): Promise<void> {
  localStorage.setItem(STORAGE_KEYS.SHOWN_HOSTS, JSON.stringify({ state, version: 1 }))
  await useShownHostsStore.persist.rehydrate()
}

const shown = () => {
  const { all, ids } = useShownHostsStore.getState()
  return { all, ids }
}

beforeEach(() => {
  localStorage.clear()
  useShownHostsStore.setState({ all: true, ids: [] })
})

describe('useShownHostsStore — shape (§0.6: { all, ids }, both always present)', () => {
  it('defaults to { all: true, ids: [] }', () => {
    const initial = useShownHostsStore.getInitialState()
    expect({ all: initial.all, ids: initial.ids }).toEqual({ all: true, ids: [] })
  })

  it('persists under purdex-shown-hosts, all and ids only', () => {
    expect(STORAGE_KEYS.SHOWN_HOSTS).toBe('purdex-shown-hosts')
    useShownHostsStore.getState().setShown(['d1_a'])
    const persisted = JSON.parse(localStorage.getItem(STORAGE_KEYS.SHOWN_HOSTS) ?? '{}') as { state: Record<string, unknown> }
    expect(persisted.state).toEqual({ all: false, ids: ['d1_a'] })
  })

  it('is registered with syncManager under its storage key', () => {
    expect(registerSpy).toHaveBeenCalledWith(STORAGE_KEYS.SHOWN_HOSTS, useShownHostsStore)
  })
})

describe('useShownHostsStore — merge', () => {
  it('keeps strings only, dedupes keeping the first, keeps unknown ids and their order', async () => {
    await rehydrateFrom({ all: false, ids: ['d1_zzz', 'local-1', 3, null, 'd1_a', 'd1_zzz', { x: 1 }, 'local-1', 'd1_b'] })
    expect(shown()).toEqual({ all: false, ids: ['d1_zzz', 'local-1', 'd1_a', 'd1_b'] })
  })

  it('keeps ids in both modes: all: true with a list keeps the list', async () => {
    await rehydrateFrom({ all: true, ids: ['d1_a', 'd1_unknown'] })
    expect(shown()).toEqual({ all: true, ids: ['d1_a', 'd1_unknown'] })
  })

  it('a non-array ids reads as []; a non-boolean all keeps the value in memory', async () => {
    useShownHostsStore.setState({ all: false, ids: ['d1_a'] })
    await rehydrateFrom({ all: 'yes', ids: 'd1_a' })
    expect(shown()).toEqual({ all: false, ids: [] })
    useShownHostsStore.setState({ all: true, ids: [] })
    await rehydrateFrom({ ids: ['d1_b'] })
    expect(shown()).toEqual({ all: true, ids: ['d1_b'] })
  })

  it('nothing stored keeps memory as it is', async () => {
    useShownHostsStore.setState({ all: false, ids: ['d1_a'] })
    await useShownHostsStore.persist.rehydrate()
    expect(shown()).toEqual({ all: false, ids: ['d1_a'] })
  })
})

describe('useShownHostsStore — actions', () => {
  it('showAll → all: true, the list (unknown ids included) kept', () => {
    useShownHostsStore.setState({ all: false, ids: ['d1_a', 'd1_unknown'] })
    useShownHostsStore.getState().showAll()
    expect(shown()).toEqual({ all: true, ids: ['d1_a', 'd1_unknown'] })
  })

  it('setShown → all: false and exactly that list (strings, deduped keeping the first)', () => {
    useShownHostsStore.getState().setShown(['d1_b', 'd1_a', 'd1_b', 'd1_unknown'])
    expect(shown()).toEqual({ all: false, ids: ['d1_b', 'd1_a', 'd1_unknown'] })
  })

  it('toggle from all: every known wire id but that one; an unknown id already listed is kept', () => {
    useShownHostsStore.setState({ all: true, ids: ['d1_unknown'] })
    useShownHostsStore.getState().toggle('d1_b', ['d1_a', 'd1_b', 'local-c'])
    expect(shown()).toEqual({ all: false, ids: ['d1_unknown', 'd1_a', 'local-c'] })
  })

  it('toggle from all drops a stale listed copy of that id too', () => {
    useShownHostsStore.setState({ all: true, ids: ['d1_b', 'd1_unknown'] })
    useShownHostsStore.getState().toggle('d1_b', ['d1_a', 'd1_b'])
    expect(shown()).toEqual({ all: false, ids: ['d1_unknown', 'd1_a'] })
  })

  it('toggle from a list: a listed id leaves, an unlisted one is appended; unknown ids and order stay', () => {
    useShownHostsStore.setState({ all: false, ids: ['d1_unknown', 'd1_a', 'd1_b'] })
    useShownHostsStore.getState().toggle('d1_a', ['d1_a', 'd1_b', 'd1_c'])
    expect(shown()).toEqual({ all: false, ids: ['d1_unknown', 'd1_b'] })
    useShownHostsStore.getState().toggle('d1_c', ['d1_a', 'd1_b', 'd1_c'])
    expect(shown()).toEqual({ all: false, ids: ['d1_unknown', 'd1_b', 'd1_c'] })
  })

  it('addShown appends once (idempotent) and leaves all as it is', () => {
    useShownHostsStore.setState({ all: false, ids: ['d1_unknown'] })
    useShownHostsStore.getState().addShown('d1_a')
    const after = useShownHostsStore.getState()
    useShownHostsStore.getState().addShown('d1_a')
    expect(useShownHostsStore.getState()).toBe(after)
    expect(shown()).toEqual({ all: false, ids: ['d1_unknown', 'd1_a'] })
    useShownHostsStore.setState({ all: true, ids: [] })
    useShownHostsStore.getState().addShown('d1_b')
    expect(shown()).toEqual({ all: true, ids: ['d1_b'] })
  })

  it('rekey replaces a local id in place; an already-present d1_ drops the local id; a missing source is a no-op', () => {
    useShownHostsStore.setState({ all: false, ids: ['d1_unknown', 'localA', 'd1_b', 'localB', 'tail'] })
    useShownHostsStore.getState().rekey([['localA', 'd1_a'], ['localB', 'd1_b']])
    expect(shown()).toEqual({ all: false, ids: ['d1_unknown', 'd1_a', 'd1_b', 'tail'] })
    const before = useShownHostsStore.getState()
    useShownHostsStore.getState().rekey([['localZ', 'd1_z']])
    expect(useShownHostsStore.getState()).toBe(before)
  })

  it('rekey keeps all as it is (the list is re-keyed in both modes)', () => {
    useShownHostsStore.setState({ all: true, ids: ['localA'] })
    useShownHostsStore.getState().rekey([['localA', 'd1_a']])
    expect(shown()).toEqual({ all: true, ids: ['d1_a'] })
  })
})

describe('useShownHostsStore — module boundary', () => {
  it('importing the module does not load useHostStore (ids are opaque wire ids; the store imports no store)', async () => {
    vi.resetModules()
    const loaded = vi.fn()
    vi.doMock('./useHostStore', () => {
      loaded()
      return {}
    })
    await import('./useShownHostsStore')
    expect(loaded).not.toHaveBeenCalled()
    vi.doUnmock('./useHostStore')
  })
})
