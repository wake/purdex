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

const all1col = () => useNewTabLayoutStore.getState().presets['1col'].columns.flat()

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
      presets: {
        '3col': { enabled: false, columns: [['sessions'], ['sessions:h1'], ['sessions:old']] },
        '2col': { enabled: false, columns: [['sessions', 'sessions:h1'], []] },
        '1col': { enabled: true, columns: [['browser', 'sessions', 'sessions:old', 'sessions:h1']] },
      },
      knownIds: ['browser', 'sessions', 'sessions:old', 'sessions:h1'],
    })
    renderHook(() => useNewTabBootstrap())
    const s = useNewTabLayoutStore.getState()
    expect(s.knownIds).toEqual(['browser', 'sessions:h1'])
    expect(s.presets['1col'].columns).toEqual([['browser', 'sessions:h1']])
    expect(s.presets['3col'].columns.flat()).toEqual(['sessions:h1'])

    act(() => { useHostStore.setState({ hosts: {}, hostOrder: [] }) })
    expect(useNewTabLayoutStore.getState().presets['1col'].columns).toEqual([['browser']])
  })

  it('does not prune or place host blocks until the host store has hydrated', () => {
    let finish: (() => void) | undefined
    const hydrated = vi.spyOn(useHostStore.persist, 'hasHydrated').mockReturnValue(false)
    const onFinish = vi.spyOn(useHostStore.persist, 'onFinishHydration').mockImplementation((cb) => {
      finish = () => cb(useHostStore.getState())
      return () => { finish = undefined }
    })
    try {
      // Pre-hydration host store holds only a transient default host.
      useHostStore.setState({ hosts: { tmp: host('tmp', 'default', 0) }, hostOrder: ['tmp'] })
      useNewTabLayoutStore.setState({
        presets: {
          '3col': { enabled: false, columns: [[], [], []] },
          '2col': { enabled: false, columns: [[], []] },
          '1col': { enabled: true, columns: [['sessions:persisted', 'browser']] },
        },
        knownIds: ['sessions:persisted', 'browser'],
      })
      renderHook(() => useNewTabBootstrap())
      expect(all1col()).toEqual(['sessions:persisted', 'browser'])
      expect(useNewTabLayoutStore.getState().knownIds).not.toContain('sessions:tmp')

      // Hydration lands the real host list (setState fires before hasHydrated flips).
      act(() => { useHostStore.setState({ hosts: { persisted: host('persisted', 'mlab', 0) }, hostOrder: ['persisted'] }) })
      expect(all1col()).toEqual(['sessions:persisted', 'browser'])
      hydrated.mockReturnValue(true)
      act(() => { finish?.() })
      expect(all1col()).toEqual(['sessions:persisted', 'browser'])
      expect(useNewTabLayoutStore.getState().knownIds).toEqual(['sessions:persisted', 'browser'])
    } finally {
      hydrated.mockRestore()
      onFinish.mockRestore()
    }
  })

  it('migrates a placed legacy sessions block in place to every host, in each preset', () => {
    useHostStore.setState({ hosts: { h1: host('h1', 'mlab', 0), h2: host('h2', 'air', 1) }, hostOrder: ['h1', 'h2'] })
    useNewTabLayoutStore.setState({
      presets: {
        '3col': { enabled: true, columns: [['browser'], ['sessions'], []] },
        '2col': { enabled: false, columns: [['sessions', 'browser'], []] },
        '1col': { enabled: true, columns: [['browser', 'sessions']] },
      },
      knownIds: ['browser', 'sessions'],
    })
    renderHook(() => useNewTabBootstrap())
    const s = useNewTabLayoutStore.getState()
    expect(s.presets['3col'].columns).toEqual([['browser'], ['sessions:h1', 'sessions:h2'], []])
    expect(s.presets['2col'].columns).toEqual([['sessions:h1', 'sessions:h2', 'browser'], []])
    expect(s.presets['1col'].columns).toEqual([['browser', 'sessions:h1', 'sessions:h2']])
    expect(s.knownIds).toEqual(['browser', 'sessions:h1', 'sessions:h2'])
  })

  it('keeps a user-removed legacy sessions block removed: host ids known but not placed', () => {
    useHostStore.setState({ hosts: { h1: host('h1', 'mlab', 0), h2: host('h2', 'air', 1) }, hostOrder: ['h1', 'h2'] })
    useNewTabLayoutStore.setState({
      presets: {
        '3col': { enabled: false, columns: [['browser'], [], []] },
        '2col': { enabled: false, columns: [['browser'], []] },
        '1col': { enabled: true, columns: [['browser']] },
      },
      knownIds: ['browser', 'sessions'],
    })
    renderHook(() => useNewTabBootstrap())
    const s = useNewTabLayoutStore.getState()
    for (const key of ['3col', '2col', '1col'] as const) {
      expect(s.presets[key].columns.flat()).toEqual(['browser'])
    }
    expect(s.knownIds).toEqual(['browser', 'sessions:h1', 'sessions:h2'])
  })

  it('defers the legacy migration until hosts hydrate, then uses the real host list', () => {
    let finish: (() => void) | undefined
    const hydrated = vi.spyOn(useHostStore.persist, 'hasHydrated').mockReturnValue(false)
    const onFinish = vi.spyOn(useHostStore.persist, 'onFinishHydration').mockImplementation((cb) => {
      finish = () => cb(useHostStore.getState())
      return () => { finish = undefined }
    })
    try {
      useHostStore.setState({ hosts: { tmp: host('tmp', 'default', 0) }, hostOrder: ['tmp'] })
      useNewTabLayoutStore.setState({
        presets: {
          '3col': { enabled: false, columns: [[], [], []] },
          '2col': { enabled: false, columns: [[], []] },
          '1col': { enabled: true, columns: [['sessions', 'browser']] },
        },
        knownIds: ['sessions', 'browser'],
      })
      renderHook(() => useNewTabBootstrap())
      expect(all1col()).toEqual(['sessions', 'browser'])
      act(() => { useHostStore.setState({ hosts: { real: host('real', 'mlab', 0) }, hostOrder: ['real'] }) })
      hydrated.mockReturnValue(true)
      act(() => { finish?.() })
      expect(all1col()).toEqual(['sessions:real', 'browser'])
      expect(useNewTabLayoutStore.getState().knownIds).toEqual(['browser', 'sessions:real'])
    } finally {
      hydrated.mockRestore()
      onFinish.mockRestore()
    }
  })

  it('keeps a user-removed host block removed across unrelated host updates', () => {
    renderHook(() => useNewTabBootstrap())
    act(() => { useNewTabLayoutStore.getState().removeModule('1col', 'sessions:h1') })
    act(() => { useHostStore.setState({ hosts: { h1: host('h1', 'renamed', 0) } }) })
    expect(all1col()).toEqual(['browser'])
  })
})
