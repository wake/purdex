import { describe, it, expect, beforeEach, afterEach } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { useNewTabProviders } from './useNewTabProviders'
import {
  clearNewTabRegistry,
  registerNewTabProvider,
  registerNewTabProviderSource,
  type NewTabProviderSource,
} from '../lib/new-tab-registry'

const Stub = () => null

function source(id: string, ids: string[]): NewTabProviderSource & { emit: (next: string[]) => void } {
  let current = ids
  const listeners = new Set<() => void>()
  return {
    id,
    getProviders: () => current.map((p) => ({ id: p, label: p, icon: 'List', order: 0, component: Stub })),
    subscribe: (l) => { listeners.add(l); return () => { listeners.delete(l) } },
    ownsId: (p) => current.includes(p),
    emit: (next) => { current = next; listeners.forEach((l) => l()) },
  }
}

const ids = (r: { current: { id: string }[] }) => r.current.map((p) => p.id)

beforeEach(() => clearNewTabRegistry())
afterEach(() => clearNewTabRegistry())

describe('useNewTabProviders — registry changes after mount', () => {
  it('sees a source registered after mount, and follows that source’s own changes', () => {
    const { result } = renderHook(() => useNewTabProviders())
    expect(ids(result)).toEqual([])
    const s = source('late', ['a'])
    act(() => registerNewTabProviderSource(s))
    expect(ids(result)).toEqual(['a'])
    act(() => s.emit(['a', 'b']))
    expect(ids(result)).toEqual(['a', 'b'])
  })

  it('follows a source replaced by id and stops listening to the old one', () => {
    const old = source('s', ['old'])
    registerNewTabProviderSource(old)
    const { result } = renderHook(() => useNewTabProviders())
    const next = source('s', ['new'])
    act(() => registerNewTabProviderSource(next))
    expect(ids(result)).toEqual(['new'])
    act(() => old.emit(['stale']))
    expect(ids(result)).toEqual(['new'])
  })

  it('updates on clear and on static provider registration', () => {
    registerNewTabProviderSource(source('s', ['a']))
    const { result } = renderHook(() => useNewTabProviders())
    act(() => clearNewTabRegistry())
    expect(ids(result)).toEqual([])
    act(() => registerNewTabProvider({ id: 'static', label: 'S', icon: 'S', order: 0, component: Stub }))
    expect(ids(result)).toEqual(['static'])
  })
})
