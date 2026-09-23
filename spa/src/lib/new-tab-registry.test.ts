import { describe, it, expect, beforeEach } from 'vitest'
import {
  registerNewTabProvider,
  getNewTabProviders,
  clearNewTabRegistry,
  unregisterNewTabProvidersByModule,
  registerNewTabProviderSource,
  unregisterNewTabProviderSource,
  subscribeNewTabProviders,
  getStaleNewTabProviderIds,
  getReadyNewTabProviders,
  getNewTabProviderMigrations,
  type NewTabProviderProps,
} from './new-tab-registry'

const Stub: React.FC<NewTabProviderProps> = () => null

beforeEach(() => {
  clearNewTabRegistry()
})

describe('new-tab-registry', () => {
  it('registerNewTabProvider adds a provider', () => {
    registerNewTabProvider({
      id: 'sessions',
      label: 'Sessions',
      icon: 'List',
      order: 0,
      component: Stub,
    })
    expect(getNewTabProviders()).toHaveLength(1)
    expect(getNewTabProviders()[0].id).toBe('sessions')
  })

  it('getNewTabProviders returns providers sorted by order', () => {
    registerNewTabProvider({
      id: 'b',
      label: 'B',
      icon: 'B',
      order: 10,
      component: Stub,
    })
    registerNewTabProvider({
      id: 'a',
      label: 'A',
      icon: 'A',
      order: 0,
      component: Stub,
    })
    registerNewTabProvider({
      id: 'c',
      label: 'C',
      icon: 'C',
      order: 5,
      component: Stub,
    })
    const ids = getNewTabProviders().map((p) => p.id)
    expect(ids).toEqual(['a', 'c', 'b'])
  })

  it('getNewTabProviders returns a copy (not the internal array)', () => {
    registerNewTabProvider({
      id: 'x',
      label: 'X',
      icon: 'X',
      order: 0,
      component: Stub,
    })
    const first = getNewTabProviders()
    const second = getNewTabProviders()
    expect(first).not.toBe(second)
    expect(first).toEqual(second)
  })

  it('clearNewTabRegistry removes all providers', () => {
    registerNewTabProvider({
      id: 'sessions',
      label: 'Sessions',
      icon: 'List',
      order: 0,
      component: Stub,
    })
    registerNewTabProvider({
      id: 'tools',
      label: 'Tools',
      icon: 'Wrench',
      order: 1,
      component: Stub,
    })
    expect(getNewTabProviders()).toHaveLength(2)
    clearNewTabRegistry()
    expect(getNewTabProviders()).toHaveLength(0)
  })

  it('registerNewTabProvider replaces a previous entry with the same id', () => {
    registerNewTabProvider({ id: 'x', label: 'A', icon: 'A', order: 1, component: Stub })
    registerNewTabProvider({ id: 'x', label: 'B', icon: 'B', order: 2, component: Stub })
    const all = getNewTabProviders()
    expect(all).toHaveLength(1)
    expect(all[0].label).toBe('B')
    expect(all[0].order).toBe(2)
  })

  it('unregisterNewTabProvidersByModule removes only entries owned by that module', () => {
    registerNewTabProvider({ id: 'a', label: 'a', icon: 'A', order: 0, component: Stub, moduleId: 'editor' })
    registerNewTabProvider({ id: 'b', label: 'b', icon: 'B', order: 1, component: Stub, moduleId: 'editor' })
    registerNewTabProvider({ id: 'c', label: 'c', icon: 'C', order: 2, component: Stub, moduleId: 'browser' })
    registerNewTabProvider({ id: 'd', label: 'd', icon: 'D', order: 3, component: Stub })  // legacy, no moduleId

    unregisterNewTabProvidersByModule('editor')

    const remaining = getNewTabProviders().map((p) => p.id).sort()
    expect(remaining).toEqual(['c', 'd'])
  })

  it('unregisterNewTabProvidersByModule is a no-op for unknown modules', () => {
    registerNewTabProvider({ id: 'a', label: 'a', icon: 'A', order: 0, component: Stub, moduleId: 'editor' })
    unregisterNewTabProvidersByModule('does-not-exist')
    expect(getNewTabProviders()).toHaveLength(1)
  })
})

describe('new-tab-registry — dynamic provider sources', () => {
  function makeSource(initial: string[]) {
    let ids = initial
    const listeners = new Set<() => void>()
    return {
      source: {
        id: 'dyn',
        getProviders: () => ids.map((id) => ({ id: `dyn:${id}`, label: 'Dyn', icon: 'List', order: 1, component: Stub })),
        subscribe: (l: () => void) => { listeners.add(l); return () => { listeners.delete(l) } },
        ownsId: (id: string) => id === 'dyn' || id.startsWith('dyn:'),
      },
      set(next: string[]) { ids = next; listeners.forEach((l) => l()) },
      listenerCount: () => listeners.size,
    }
  }

  it('merges source providers with static ones, sorted by order', () => {
    registerNewTabProvider({ id: 'first', label: 'F', icon: 'F', order: 0, component: Stub })
    registerNewTabProvider({ id: 'last', label: 'L', icon: 'L', order: 5, component: Stub })
    registerNewTabProviderSource(makeSource(['a', 'b']).source)
    expect(getNewTabProviders().map((p) => p.id)).toEqual(['first', 'dyn:a', 'dyn:b', 'last'])
  })

  it('reflects the source’s current providers on every call', () => {
    const s = makeSource(['a'])
    registerNewTabProviderSource(s.source)
    s.set(['a', 'c'])
    expect(getNewTabProviders().map((p) => p.id)).toEqual(['dyn:a', 'dyn:c'])
  })

  it('subscribeNewTabProviders fires when a source changes and unsubscribes cleanly', () => {
    const s = makeSource(['a'])
    registerNewTabProviderSource(s.source)
    let calls = 0
    const unsub = subscribeNewTabProviders(() => { calls++ })
    s.set(['b'])
    expect(calls).toBe(1)
    unsub()
    s.set(['c'])
    expect(calls).toBe(1)
    expect(s.listenerCount()).toBe(0)
  })

  it('getStaleNewTabProviderIds returns source-owned ids that no longer resolve', () => {
    registerNewTabProvider({ id: 'static', label: 'S', icon: 'S', order: 0, component: Stub })
    registerNewTabProviderSource(makeSource(['a']).source)
    expect(getStaleNewTabProviderIds(['static', 'dyn', 'dyn:a', 'dyn:gone', 'unowned'])).toEqual(['dyn', 'dyn:gone'])
  })

  // Host ownership §3.2: a host-bearing column whose host is not on this device is kept, never pruned.
  it('an id a ready source RETAINS is never stale; an owned id it does not retain still is', () => {
    registerNewTabProviderSource({ ...makeSource(['a']).source, retainsId: (id: string) => id.startsWith('dyn:') })
    expect(getStaleNewTabProviderIds(['dyn', 'dyn:a', 'dyn:gone'])).toEqual(['dyn'])
  })

  it('an unready source neither reports stale ids nor contributes ready providers', () => {
    registerNewTabProvider({ id: 'static', label: 'S', icon: 'S', order: 0, component: Stub })
    let ready = false
    registerNewTabProviderSource({ ...makeSource(['a']).source, isReady: () => ready })
    expect(getStaleNewTabProviderIds(['dyn:gone'])).toEqual([])
    expect(getReadyNewTabProviders().map((p) => p.id)).toEqual(['static'])
    ready = true
    expect(getStaleNewTabProviderIds(['dyn:gone'])).toEqual(['dyn:gone'])
    expect(getReadyNewTabProviders().map((p) => p.id)).toEqual(['static', 'dyn:a'])
  })

  it('getNewTabProviderMigrations collects migrations from ready sources only', () => {
    let ready = false
    registerNewTabProviderSource({
      ...makeSource(['a']).source,
      isReady: () => ready,
      migrations: () => [{ from: 'dyn', to: ['dyn:a'] }],
    })
    registerNewTabProviderSource({ ...makeSource([]).source, id: 'plain' })
    expect(getNewTabProviderMigrations()).toEqual([])
    ready = true
    expect(getNewTabProviderMigrations()).toEqual([{ from: 'dyn', to: ['dyn:a'] }])
  })

  it('subscribeNewTabProviders notifies on register / replace / clear, and follows sources added later', () => {
    let calls = 0
    const unsub = subscribeNewTabProviders(() => { calls++ })
    const late = makeSource(['a'])
    registerNewTabProviderSource(late.source)
    expect(calls).toBe(1)
    late.set(['b']) // emitter of a source added after subscription
    expect(calls).toBe(2)
    const replacement = makeSource(['z'])
    registerNewTabProviderSource(replacement.source) // same id → replace
    expect(calls).toBe(3)
    expect(late.listenerCount()).toBe(0) // old emitter released
    late.set(['stale'])
    expect(calls).toBe(3)
    registerNewTabProvider({ id: 'static', label: 'S', icon: 'S', order: 0, component: Stub })
    expect(calls).toBe(4)
    clearNewTabRegistry()
    expect(calls).toBe(5)
    expect(replacement.listenerCount()).toBe(0)
    unsub()
    registerNewTabProviderSource(makeSource(['q']).source)
    expect(calls).toBe(5)
  })

  it('re-registering a source with the same id replaces it; clear removes sources', () => {
    registerNewTabProviderSource(makeSource(['a']).source)
    registerNewTabProviderSource(makeSource(['z']).source)
    expect(getNewTabProviders().map((p) => p.id)).toEqual(['dyn:z'])
    clearNewTabRegistry()
    expect(getNewTabProviders()).toHaveLength(0)
  })
})

describe('new-tab-registry — source teardown', () => {
  function makeSource(id: string, ids: string[], moduleId?: string) {
    const listeners = new Set<() => void>()
    return {
      source: {
        id,
        moduleId,
        getProviders: () => ids.map((x) => ({ id: `${id}:${x}`, label: id, icon: 'List', order: 1, component: Stub })),
        subscribe: (l: () => void) => { listeners.add(l); return () => { listeners.delete(l) } },
        ownsId: (pid: string) => pid.startsWith(`${id}:`),
      },
      emit() { listeners.forEach((l) => l()) },
      listenerCount: () => listeners.size,
    }
  }

  it('unregisterNewTabProvidersByModule removes sources with that moduleId and releases their subscriptions', () => {
    const owned = makeSource('headless', ['h1', 'h2'], 'execution')
    const sessions = makeSource('sessions', ['h1']) // no moduleId — never module-owned
    registerNewTabProvider({ id: 'static', label: 's', icon: 'S', order: 0, component: Stub, moduleId: 'execution' })
    registerNewTabProviderSource(owned.source)
    registerNewTabProviderSource(sessions.source)
    let calls = 0
    const unsub = subscribeNewTabProviders(() => { calls++ })
    expect(owned.listenerCount()).toBe(1)

    unregisterNewTabProvidersByModule('execution')

    expect(getNewTabProviders().map((p) => p.id)).toEqual(['sessions:h1'])
    expect(owned.listenerCount()).toBe(0)
    const before = calls
    owned.emit()
    expect(calls).toBe(before) // the removed source no longer reaches the registry listener
    expect(sessions.listenerCount()).toBe(1)
    sessions.emit()
    expect(calls).toBe(before + 1)
    expect(getStaleNewTabProviderIds(['headless:h1'])).toEqual([]) // nobody owns it any more
    unsub()
  })

  it('unregisterNewTabProvidersByModule leaves sources without a moduleId untouched', () => {
    const sessions = makeSource('sessions', ['h1'])
    registerNewTabProviderSource(sessions.source)
    let calls = 0
    const unsub = subscribeNewTabProviders(() => { calls++ })
    unregisterNewTabProvidersByModule('execution')
    expect(calls).toBe(0) // nothing changed → no registry notification
    expect(getNewTabProviders().map((p) => p.id)).toEqual(['sessions:h1'])
    expect(sessions.listenerCount()).toBe(1)
    unsub()
  })

  it('unregisterNewTabProviderSource removes one source by id and notifies; unknown ids are a no-op', () => {
    const a = makeSource('a', ['x'])
    const b = makeSource('b', ['y'])
    registerNewTabProviderSource(a.source)
    registerNewTabProviderSource(b.source)
    let calls = 0
    const unsub = subscribeNewTabProviders(() => { calls++ })

    unregisterNewTabProviderSource('a')
    expect(calls).toBe(1)
    expect(getNewTabProviders().map((p) => p.id)).toEqual(['b:y'])
    expect(a.listenerCount()).toBe(0)
    expect(b.listenerCount()).toBe(1)

    unregisterNewTabProviderSource('missing')
    expect(calls).toBe(1)
    unsub()
  })
})
