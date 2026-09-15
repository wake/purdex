import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { useNewTabBootstrap } from './useNewTabBootstrap'
import { useNewTabLayoutStore } from '../stores/useNewTabLayoutStore'
import { useHostStore } from '../stores/useHostStore'
import { clearNewTabRegistry, registerNewTabProvider, registerNewTabProviderSource } from '../lib/new-tab-registry'
import { createHostSessionProviderSource } from '../lib/session-new-tab-providers'

vi.mock('./useSessionWatch', () => ({ useSessionWatch: vi.fn() }))

const host = (id: string, name: string, order: number) => ({ id, name, ip: '1', port: 7860, order })
const Stub = () => null

beforeEach(() => {
  clearNewTabRegistry()
  useNewTabLayoutStore.setState(useNewTabLayoutStore.getInitialState(), true)
  useHostStore.setState({ hosts: { h1: host('h1', 'mlab', 0) }, hostOrder: ['h1'], activeHostId: 'h1' })
  registerNewTabProvider({ id: 'browser', label: 'b', icon: 'Globe', order: -10, component: Stub })
  registerNewTabProviderSource(createHostSessionProviderSource())
})

afterEach(() => clearNewTabRegistry())

const all1col = () => useNewTabLayoutStore.getState().profiles['1col'].columns.flat()

describe('useNewTabBootstrap — per-host session blocks', () => {
  it('places a block for each host on first run', () => {
    renderHook(() => useNewTabBootstrap())
    expect(all1col()).toEqual(['browser', 'sessions:h1'])
  })

  it('adds a block when a host is added', () => {
    renderHook(() => useNewTabBootstrap())
    act(() => {
      useHostStore.setState({ hosts: { h1: host('h1', 'mlab', 0), h2: host('h2', 'air', 1) }, hostOrder: ['h1', 'h2'] })
    })
    expect(all1col()).toEqual(['browser', 'sessions:h1', 'sessions:h2'])
    expect(useNewTabLayoutStore.getState().knownIds).toContain('sessions:h2')
  })

  it('prunes a removed host’s block and the legacy sessions id', () => {
    useNewTabLayoutStore.setState({
      profiles: {
        '3col': { enabled: false, columns: [['sessions'], ['sessions:h1'], ['sessions:old']] },
        '2col': { enabled: false, columns: [['sessions', 'sessions:h1'], []] },
        '1col': { enabled: true, columns: [['browser', 'sessions', 'sessions:old', 'sessions:h1']] },
      },
      knownIds: ['browser', 'sessions', 'sessions:old', 'sessions:h1'],
    })
    renderHook(() => useNewTabBootstrap())
    const s = useNewTabLayoutStore.getState()
    expect(s.knownIds).toEqual(['browser', 'sessions:h1'])
    expect(s.profiles['1col'].columns).toEqual([['browser', 'sessions:h1']])
    expect(s.profiles['3col'].columns.flat()).toEqual(['sessions:h1'])

    act(() => { useHostStore.setState({ hosts: {}, hostOrder: [] }) })
    expect(useNewTabLayoutStore.getState().profiles['1col'].columns).toEqual([['browser']])
  })

  it('keeps a user-removed host block removed across unrelated host updates', () => {
    renderHook(() => useNewTabBootstrap())
    act(() => { useNewTabLayoutStore.getState().removeModule('1col', 'sessions:h1') })
    act(() => { useHostStore.setState({ hosts: { h1: host('h1', 'renamed', 0) } }) })
    expect(all1col()).toEqual(['browser'])
  })
})
