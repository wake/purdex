import { describe, it, expect, beforeEach } from 'vitest'
import {
  registerNewTabProvider,
  getNewTabProviders,
  clearNewTabRegistry,
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
})
