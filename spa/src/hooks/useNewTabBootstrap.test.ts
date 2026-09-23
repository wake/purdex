import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { useNewTabBootstrap } from './useNewTabBootstrap'
import { useNewTabLayoutStore } from '../stores/useNewTabLayoutStore'
import { useHostStore } from '../stores/useHostStore'
import { clearNewTabRegistry, registerNewTabProvider, registerNewTabProviderSource } from '../lib/new-tab-registry'
import { createHostSessionProviderSource } from '../lib/session-new-tab-providers'
import { createHeadlessProviderSource } from '../lib/headless-new-tab-providers'
import { syncIdOfSync } from '../lib/profile/host-identity'
import { runHostReresolve } from '../lib/host-reresolve'

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

  // Host ownership §3.2: a host-bearing column whose host is not on this device is kept as is — never pruned —
  // so it comes back when the host is added here, and a device lacking a host does not delete it for every device.
  // Only the legacy single `sessions` id still goes (through its migration).
  it('keeps the block of a host not on this device (and of a removed host); the legacy sessions id still goes', () => {
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
    expect(s.knownIds).toEqual(['browser', 'sessions:old', 'sessions:h1'])
    expect(s.presets['1col'].columns).toEqual([['browser', 'sessions:old', 'sessions:h1']])
    expect(s.presets['3col'].columns).toEqual([[], ['sessions:h1'], ['sessions:old']])

    act(() => { useHostStore.setState({ hosts: {}, hostOrder: [] }) })
    expect(useNewTabLayoutStore.getState().presets['1col'].columns).toEqual([['browser', 'sessions:old', 'sessions:h1']])
  })

  it('with the host store hydrated, sessions:/headless: columns of a host not on this device survive run()', () => {
    registerNewTabProviderSource(createHeadlessProviderSource())
    const layout = {
      presets: {
        '3col': { enabled: false, columns: [['sessions:d1_unknown'], ['headless:d1_unknown'], []] },
        '2col': { enabled: false, columns: [['sessions:h1', 'headless:d1_unknown'], ['sessions:d1_unknown']] },
        '1col': { enabled: true, columns: [['browser', 'sessions:h1', 'headless:h1', 'sessions:d1_unknown', 'headless:d1_unknown']] },
      },
      knownIds: ['browser', 'sessions:h1', 'headless:h1', 'sessions:d1_unknown', 'headless:d1_unknown'],
    }
    useNewTabLayoutStore.setState(layout)
    expect(useHostStore.persist.hasHydrated()).toBe(true)
    renderHook(() => useNewTabBootstrap())
    const s = useNewTabLayoutStore.getState()
    expect(s.presets).toEqual(layout.presets)
    expect(s.knownIds).toEqual(layout.knownIds)
  })

  // Host ownership §3.3 / plan §0.3: the pass is asynchronous (lock, retry), so this bootstrap can see a new host
  // before its column — kept verbatim under the host's wire id — is rewritten. It must not place a second one.
  it('a host arrives whose sessions:/headless:<wire id> is already placed → no second column; after the pass, exactly one', () => {
    registerNewTabProviderSource(createHeadlessProviderSource())
    const DAEMON = 'air-lab:26bbbb'
    const W = syncIdOfSync(DAEMON)
    useNewTabLayoutStore.setState({
      presets: {
        '3col': { enabled: false, columns: [[`sessions:${W}`], [], [`headless:${W}`]] },
        '2col': { enabled: false, columns: [[`sessions:${W}`], []] },
        '1col': { enabled: true, columns: [['browser', 'sessions:h1', 'headless:h1', `sessions:${W}`]] },
      },
      knownIds: ['browser', 'sessions:h1', 'headless:h1', `sessions:${W}`, `headless:${W}`],
    })
    renderHook(() => useNewTabBootstrap())
    act(() => {
      useHostStore.setState({ hosts: { h1: host('h1', 'mlab', 0), h2: { ...host('h2', 'air', 1), daemonId: DAEMON } }, hostOrder: ['h1', 'h2'] })
    })
    const placed = () => (['3col', '2col', '1col'] as const).flatMap((k) => useNewTabLayoutStore.getState().presets[k].columns.flat())
    expect(placed()).not.toContain('sessions:h2')
    expect(placed()).not.toContain('headless:h2')
    expect(useNewTabLayoutStore.getState().knownIds).not.toContain('sessions:h2')

    act(() => { runHostReresolve() })
    const s = useNewTabLayoutStore.getState()
    expect(s.presets['1col'].columns).toEqual([['browser', 'sessions:h1', 'headless:h1', 'sessions:h2']])
    expect(s.presets['3col'].columns).toEqual([['sessions:h2'], [], ['headless:h2']])
    expect(s.knownIds.filter((id) => id.endsWith(':h2'))).toEqual(['sessions:h2', 'headless:h2'])
    expect(JSON.stringify(s.presets)).not.toContain(W)
  })

  // PR #1406 attacker medium (TOCTOU): the wire column lands AFTER the bootstrap looked at the layout and before it
  // placed — here from inside the registry read (the step between the two), as a write of another window's
  // rehydrate would. The check runs inside the placing action, on the latest state: still no second block.
  it('a wire column landing between the check and the placement: no second block', () => {
    const DAEMON = 'air-lab:26dddd'
    const W = syncIdOfSync(DAEMON)
    let armed = false
    let reads = 0
    registerNewTabProviderSource({
      id: 'lander',
      getProviders: () => {
        // the run's second registry read (after the stale scan) is its placement read
        if (armed && ++reads === 2) {
          const st = useNewTabLayoutStore.getState()
          useNewTabLayoutStore.setState({ presets: { ...st.presets, '1col': { enabled: true, columns: [[...st.presets['1col'].columns[0], `sessions:${W}`]] } } })
        }
        return []
      },
      subscribe: () => () => {},
      ownsId: () => false,
    })
    renderHook(() => useNewTabBootstrap())
    armed = true
    act(() => {
      useHostStore.setState({ hosts: { h1: host('h1', 'mlab', 0), h2: { ...host('h2', 'air', 1), daemonId: DAEMON } }, hostOrder: ['h1', 'h2'] })
    })
    expect(reads).toBeGreaterThanOrEqual(2)
    expect(all1col()).toContain(`sessions:${W}`)
    expect(all1col()).not.toContain('sessions:h2')
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
