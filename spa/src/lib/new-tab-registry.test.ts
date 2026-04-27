import { describe, it, expect, beforeEach } from 'vitest'
import {
  registerNewTabProvider,
  getNewTabProviders,
  clearNewTabRegistry,
  unregisterNewTabProvidersByModule,
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
