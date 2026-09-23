import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest'
import { requestAtOf, useHostStore } from '../../stores/useHostStore'
import { useWorkspaceStore } from '../../features/workspace/store'
import { useTabStore } from '../../stores/useTabStore'
import { useUISettingsStore } from '../../stores/useUISettingsStore'
import { useEditorSettingsStore } from '../../stores/useEditorSettingsStore'
import { useThemeStore } from '../../stores/useThemeStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { useNotificationSettingsStore } from '../../stores/useNotificationSettingsStore'
import { useWorkspaceSettingsStore } from '../../stores/useWorkspaceSettingsStore'
import { useHostSettingsStore } from '../../stores/useHostSettingsStore'
import { useNewTabLayoutStore } from '../../stores/useNewTabLayoutStore'
import { useLayoutStore } from '../../stores/useLayoutStore'
import type { PaneLayout, Tab, Workspace } from '../../types/tab'
import { PROJECTIONS } from './projections'
import { UNSYNCED_SETTINGS_KEYS, startCollector, watchUnsyncedStores, type Collector, type SectionReport } from './collector'

// A `crypto.subtle` digest resolves off the microtask queue and fake timers
// cannot flush it: the structural key is
// an equivalent identity. `gate` lets one test hold a chosen hash back.
const h = vi.hoisted(() => ({
  gate: null as ((payload: unknown) => Promise<void> | undefined) | null,
  registered: [] as string[],
}))

vi.mock('./hash', async (importOriginal) => {
  const actual = await importOriginal<typeof import('./hash')>()
  return {
    ...actual,
    hashSection: vi.fn(async (payload: unknown) => {
      const key = actual.structuralKey(payload)
      await h.gate?.(payload)
      return key
    }),
  }
})

// Records every `syncManager.register` key (the registry itself is private to
// the closure in storage/sync.ts), calling through so the stores still work.
vi.mock('../storage/sync', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../storage/sync')>()
  const register = actual.syncManager.register
  actual.syncManager.register = (key, store) => {
    h.registered.push(key)
    register(key, store)
  }
  return actual
})

const { structuralKey } = await vi.importActual<typeof import('./hash')>('./hash')

const SETTINGS_STORES = [
  useUISettingsStore, useThemeStore, useI18nStore, useNotificationSettingsStore,
  useWorkspaceSettingsStore, useHostSettingsStore, useNewTabLayoutStore, useLayoutStore,
] as const

const EIGHT_KEYS = [
  'purdex-host-settings', 'purdex-i18n', 'purdex-layout', 'purdex-newtab-layout',
  'purdex-notification-settings', 'purdex-themes', 'purdex-ui-settings', 'purdex-workspace-settings',
]

function host(id: string, name = id) {
  return { id, name, ip: '10.0.0.1', port: 7860, order: 0 }
}

function leaf(id: string): PaneLayout {
  return { type: 'leaf', pane: { id: `p-${id}`, content: { kind: 'new-tab' } } } as PaneLayout
}

function tab(id: string, layout: PaneLayout = leaf(id)): Tab {
  return { id, pinned: false, locked: false, createdAt: 0, layout }
}

function split(id: string, sizes: number[]): PaneLayout {
  return { type: 'split', id: `s-${id}`, direction: 'h', children: [leaf(`${id}a`), leaf(`${id}b`)], sizes }
}

function ws(id: string, tabs: string[]): Workspace {
  return { id, name: id, tabs, activeTabId: null, moduleConfig: {} }
}

function patchTab(id: string, patch: Partial<Tab>): void {
  const s = useTabStore.getState()
  useTabStore.setState({ tabs: { ...s.tabs, [id]: { ...s.tabs[id], ...patch } } })
}

function setWorkspaces(fn: (list: Workspace[]) => Workspace[]): void {
  useWorkspaceStore.setState({ workspaces: fn(useWorkspaceStore.getState().workspaces) })
}

let initialSettings: object[] = []
let reports: SectionReport[] = []
let problems: { kind: string; detail: string }[] = []
let collector: Collector | null = null

function start(): Collector {
  collector = startCollector({ onSection: (r) => reports.push(r), onProblem: (p) => problems.push(p) })
  return collector
}

const keys = (): string[] => reports.map((r) => r.key).sort()

/**
 * The collector's pending timers. jsdom's `localStorage.setItem` arms a 0 ms
 * timer of its own (the `storage` event dispatch) on every persisted `setState`;
 * those are flushed first — the collector's debounce is far longer than 0 ms.
 */
async function pendingTimers(): Promise<number> {
  await vi.advanceTimersByTimeAsync(0)
  return vi.getTimerCount()
}

/** A change that must not even arm a timer, let alone report. */
async function expectIgnored(change: () => void): Promise<void> {
  start()
  change()
  expect(await pendingTimers()).toBe(0)
  await vi.advanceTimersByTimeAsync(5000)
  expect(reports).toEqual([])
}

beforeAll(() => {
  initialSettings = SETTINGS_STORES.map((s) => s.getState())
})

beforeEach(() => {
  vi.useFakeTimers()
  h.gate = null
  reports = []
  problems = []
  SETTINGS_STORES.forEach((s, i) => (s as { setState: (v: object, replace: true) => void }).setState(initialSettings[i], true))
  useHostStore.setState({ hosts: { h1: host('h1') }, hostOrder: ['h1'], activeHostId: 'h1', devHostId: null, runtime: {} })
  useTabStore.setState({
    tabs: { t1: tab('t1'), t2: tab('t2'), t3: tab('t3', split('t3', [50, 50])) },
    tabOrder: ['t1', 't2', 't3'], activeTabId: 't1', visitHistory: [],
  })
  useWorkspaceStore.setState({ workspaces: [ws('A', ['t1', 't3']), ws('B', ['t2'])], activeWorkspaceId: 'A' })
})

afterEach(() => {
  collector?.stop()
  collector = null
  vi.useRealTimers()
})

describe('startCollector — debounce', () => {
  it('reports at 500 ms, not at 499', async () => {
    start()
    useHostStore.setState({ hosts: { h1: host('h1', 'renamed') } })
    await vi.advanceTimersByTimeAsync(499)
    expect(reports).toEqual([])
    await vi.advanceTimersByTimeAsync(1)
    expect(keys()).toEqual(['hosts'])
    expect((reports[0].payload as { hosts: Record<string, { name: string }> }).hosts.h1.name).toBe('renamed')
  })

  it('a second change inside the window restarts it (trailing)', async () => {
    start()
    useHostStore.setState({ hosts: { h1: host('h1', 'a') } })
    await vi.advanceTimersByTimeAsync(400)
    useHostStore.setState({ hosts: { h1: host('h1', 'b') } })
    await vi.advanceTimersByTimeAsync(499)
    expect(reports).toEqual([])
    await vi.advanceTimersByTimeAsync(1)
    expect(reports).toHaveLength(1)
    expect((reports[0].payload as { hosts: Record<string, { name: string }> }).hosts.h1.name).toBe('b')
  })

  it('a learned daemonId schedules `hosts` and travels in its payload (host-daemon-id D6)', async () => {
    start()
    useHostStore.getState().observeDaemonId('h1', 'mini:abc123', requestAtOf(useHostStore.getState().hosts.h1))
    await vi.advanceTimersByTimeAsync(500)
    expect(keys()).toEqual(['hosts'])
    expect((reports[0].payload as { hosts: Record<string, { daemonId?: string }> }).hosts.h1.daemonId).toBe('mini:abc123')
  })

  it('honours debounceMs', async () => {
    collector = startCollector({ onSection: (r) => reports.push(r), debounceMs: 50 })
    useHostStore.setState({ hostOrder: [] })
    await vi.advanceTimersByTimeAsync(50)
    expect(keys()).toEqual(['hosts'])
  })

  it('is per section: a busy `hosts` does not postpone a scheduled `settings`', async () => {
    start()
    useUISettingsStore.setState({ keepAliveCount: 5 })
    for (let i = 0; i < 4; i++) {
      await vi.advanceTimersByTimeAsync(100)
      useHostStore.setState({ hosts: { h1: host('h1', `n${i}`) } })
    }
    await vi.advanceTimersByTimeAsync(100) // t=500: settings is due, hosts last moved at t=400
    expect(keys()).toEqual(['settings'])
    await vi.advanceTimersByTimeAsync(400)
    expect(keys()).toEqual(['hosts', 'settings'])
  })
})

describe('startCollector — changes that schedule nothing', () => {
  it('host runtime', () => expectIgnored(() => useHostStore.setState({ runtime: { h1: { status: 'connected' } } })))
  it('a daemonId mismatch flag — runtime only, never in `hosts` (host-daemon-id D2)', async () => {
    useHostStore.setState({ hosts: { h1: { ...host('h1'), daemonId: 'mini:stored' } } })
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const hostsBefore = useHostStore.getState().hosts
    await expectIgnored(() => useHostStore.getState().observeDaemonId('h1', 'mini:other', requestAtOf(useHostStore.getState().hosts.h1)))
    // not vacuous: the flag WAS raised, and `hosts` kept its reference
    expect(useHostStore.getState().runtime.h1?.daemonIdMismatch).toEqual({ stored: 'mini:stored', observed: 'mini:other', endpoint: '10.0.0.1:7860' })
    expect(useHostStore.getState().hosts).toBe(hostsBefore)
    warn.mockRestore()
  })
  it('activeHostId', () => expectIgnored(() => useHostStore.setState({ activeHostId: null })))
  it('devHostId', () => expectIgnored(() => useHostStore.setState({ devHostId: 'h1' })))
  it('activeWorkspaceId', () => expectIgnored(() => useWorkspaceStore.setState({ activeWorkspaceId: 'B' })))
  it("a workspace's activeTabId", () =>
    expectIgnored(() => setWorkspaces((l) => l.map((w) => (w.id === 'A' ? { ...w, activeTabId: 't3' } : w)))))
  it('tab store activeTabId', () => expectIgnored(() => useTabStore.setState({ activeTabId: 't2' })))
  it('visitHistory', () => expectIgnored(() => useTabStore.setState({ visitHistory: ['t1', 't2'] })))
  it('tabOrder', () => expectIgnored(() => useTabStore.setState({ tabOrder: ['t3', 't2', 't1'] })))
  it('terminalSettingsVersion', () => expectIgnored(() => useUISettingsStore.getState().bumpTerminalSettingsVersion()))
  it('activeEditingPreset', () => expectIgnored(() => useNewTabLayoutStore.setState({ activeEditingPreset: '2col' })))
  it('knownIds', () => expectIgnored(() => useNewTabLayoutStore.setState({ knownIds: ['x'] })))
  it('layout regions', () =>
    expectIgnored(() => useLayoutStore.setState({ regions: { ...useLayoutStore.getState().regions } })))
})

describe('startCollector — tabs and workspaces', () => {
  it('a split `sizes` change schedules, but the hash is unchanged so nothing is reported', async () => {
    const c = start()
    await c.primeAll()
    reports = []
    patchTab('t3', { layout: split('t3', [30, 70]) })
    expect(await pendingTimers()).toBeGreaterThan(0)
    await vi.advanceTimersByTimeAsync(5000)
    expect(reports).toEqual([])
  })

  it('a changed tab reports only its own tabs.<id>', async () => {
    start()
    patchTab('t2', { pinned: true })
    await vi.advanceTimersByTimeAsync(500)
    expect(keys()).toEqual(['tabs.B'])
    expect((reports[0].payload as { tabs: Record<string, Tab> }).tabs.t2.pinned).toBe(true)
  })

  it('moving a tab from A to B reports tabs.A and tabs.B', async () => {
    const c = start()
    await c.primeAll()
    reports = []
    setWorkspaces((l) => l.map((w) => (w.id === 'A' ? { ...w, tabs: ['t3'] } : { ...w, tabs: ['t2', 't1'] })))
    await vi.advanceTimersByTimeAsync(500)
    expect(keys()).toEqual(['tabs.A', 'tabs.B'])
  })

  it('a new workspace reports `workspaces` and its tabs.<id>', async () => {
    const c = start()
    await c.primeAll()
    reports = []
    setWorkspaces((l) => [...l, ws('C', [])])
    await vi.advanceTimersByTimeAsync(500)
    expect(keys()).toEqual(['tabs.C', 'workspaces'])
    expect(reports.find((r) => r.key === 'tabs.C')?.payload).toEqual({ order: [], tabs: {} })
  })

  it('a removed workspace reports `workspaces` and a null tabs.<id>, once', async () => {
    const c = start()
    await c.primeAll()
    reports = []
    setWorkspaces((l) => l.filter((w) => w.id !== 'B'))
    await vi.advanceTimersByTimeAsync(500)
    expect(keys()).toEqual(['tabs.B', 'workspaces'])
    expect(reports.find((r) => r.key === 'tabs.B')).toEqual({ key: 'tabs.B', hash: null, payload: null })
    reports = []
    patchTab('t2', { pinned: true }) // t2 now belongs to nobody
    setWorkspaces((l) => l.map((w) => ({ ...w })))
    await vi.advanceTimersByTimeAsync(5000)
    expect(reports).toEqual([])
  })

  it('a renamed workspace reports `workspaces` only', async () => {
    const c = start()
    await c.primeAll()
    reports = []
    setWorkspaces((l) => l.map((w) => (w.id === 'A' ? { ...w, name: 'Alpha' } : w)))
    await vi.advanceTimersByTimeAsync(500)
    expect(keys()).toEqual(['workspaces'])
  })

  it('a workspace id the daemon would reject is device-local: not in `workspaces`, no tabs.*, reported once', async () => {
    const c = start()
    await c.primeAll()
    const before = reports.find((r) => r.key === 'workspaces')
    reports = []
    setWorkspaces((l) => [...l, ws('bad id!', ['t2'])])
    await vi.advanceTimersByTimeAsync(500)
    expect(keys()).toEqual([]) // the payload did not change: the workspace is not in it
    expect(problems.filter((p) => p.kind === 'invalid-workspace-id')).toEqual([{ kind: 'invalid-workspace-id', detail: 'bad id!' }])
    await c.primeAll()
    const after = reports.find((r) => r.key === 'workspaces')
    expect(after?.hash).toBe(before?.hash)
    expect(JSON.stringify(after?.payload)).not.toContain('bad id!')
    await c.primeAll()
    await c.primeAll()
    expect([...new Set(keys())]).toEqual(['hosts', 'settings', 'tabs.A', 'tabs.B', 'workspaces'])
    patchTab('t2', { locked: true })
    await vi.advanceTimersByTimeAsync(500)
    expect(problems.filter((p) => p.kind === 'invalid-workspace-id')).toEqual([
      { kind: 'invalid-workspace-id', detail: 'bad id!' },
    ])
  })

  // Every tab belongs to a workspace (spec §4.3); one that is in none is the app's to adopt
  // (features/workspace/lib/adopt-standalone.ts), not the profile's to count. It still enters no section.
  // (Was: "…and are reported once per count" — the `standalone-tabs` census, gone in P3c-2.)
  it('a tab in no workspace enters no section, and is nothing the collector reports', async () => {
    const c = start()
    useTabStore.setState({ tabs: { ...useTabStore.getState().tabs, s1: tab('s1') } })
    await vi.advanceTimersByTimeAsync(500)
    expect(reports).toEqual([])
    expect(problems).toEqual([])
    await c.primeAll()
    patchTab('s1', { pinned: true })
    await vi.advanceTimersByTimeAsync(500)
    expect(JSON.stringify(reports)).not.toContain('"s1"')
    useTabStore.setState({ tabs: { ...useTabStore.getState().tabs, s2: tab('s2') } })
    await vi.advanceTimersByTimeAsync(500)
    expect(JSON.stringify(reports)).not.toContain('"s2"')
    expect(problems).toEqual([])
  })
})

describe('startCollector — settings', () => {
  it('every settings store with a listed field schedules `settings`', async () => {
    const changes: (() => void)[] = [
      () => useUISettingsStore.setState({ keepAliveCount: 7 }),
      () => useThemeStore.setState({ customThemes: {} }),
      () => useI18nStore.setState({ customLocales: {} }),
      () => useNotificationSettingsStore.setState({ agents: {} }),
      () => useWorkspaceSettingsStore.setState({ workspaces: {} }),
      () => useHostSettingsStore.setState({ hosts: {} }),
      () => useNewTabLayoutStore.setState({ presets: { ...useNewTabLayoutStore.getState().presets } }),
      () => useLayoutStore.setState({ tabPosition: 'left', activityBarWidth: 'wide' }),
    ]
    start()
    for (const change of changes) {
      expect(await pendingTimers()).toBe(0)
      change()
      expect(await pendingTimers()).toBe(1)
      await vi.advanceTimersByTimeAsync(500)
    }
  })

  it('a device-local store schedules nothing: editor preferences are not part of the profile', async () => {
    start()
    useEditorSettingsStore.setState({ fontSize: 21 })
    expect(await pendingTimers()).toBe(0)
  })

  it('always carries all eight stores, whichever one changed', async () => {
    start()
    useUISettingsStore.setState({ keepAliveCount: 5 })
    await vi.advanceTimersByTimeAsync(500)
    expect(keys()).toEqual(['settings'])
    const payload = reports[0].payload as Record<string, Record<string, unknown>>
    expect(Object.keys(payload).sort()).toEqual(EIGHT_KEYS)
    expect(payload['purdex-ui-settings'].keepAliveCount).toBe(5)
    expect(payload).not.toHaveProperty('purdex-editor-settings')
    expect(payload['purdex-ui-settings']).not.toHaveProperty('terminalSettingsVersion')
    expect(payload['purdex-layout']).toEqual({ tabPosition: 'top' })
  })
})

describe('startCollector — workspace-scoped settings follow the master workspace set', () => {
  const scoped = (r: SectionReport | undefined): unknown => (r?.payload as Record<string, Record<string, unknown>> | undefined)?.['purdex-workspace-settings']?.workspaces

  it('carries the entries of the master workspaces only — not an orphan, not an unsyncable id', async () => {
    setWorkspaces((l) => [...l, ws('bad id!', [])])
    useWorkspaceSettingsStore.setState({ workspaces: { A: { files: { root: '/a' } }, orphan: { files: { root: '/o' } }, 'bad id!': { files: { root: '/b' } } } })
    const c = start()
    await c.primeAll()
    expect(scoped(reports.find((r) => r.key === 'settings'))).toEqual({ A: { files: { root: '/a' } } })
  })

  it('a workspace that APPEARS re-evaluates `settings`: an entry already stored under its id now travels', async () => {
    useWorkspaceSettingsStore.setState({ workspaces: { C: { files: { root: '/c' } } } })
    const c = start()
    await c.primeAll()
    expect(scoped(reports.find((r) => r.key === 'settings'))).toEqual({})
    reports = []
    setWorkspaces((l) => [...l, ws('C', [])]) // the settings stores are not touched
    await vi.advanceTimersByTimeAsync(500)
    expect(keys()).toEqual(['settings', 'tabs.C', 'workspaces'])
    expect(scoped(reports.find((r) => r.key === 'settings'))).toEqual({ C: { files: { root: '/c' } } })
  })

  it('a workspace that GOES re-evaluates `settings` even when nothing cleared its entry', async () => {
    useWorkspaceSettingsStore.setState({ workspaces: { A: { files: { root: '/a' } }, B: { files: { root: '/b' } } } })
    const c = start()
    await c.primeAll()
    reports = []
    setWorkspaces((l) => l.filter((w) => w.id !== 'B')) // a raw write: no `clearWorkspace`
    await vi.advanceTimersByTimeAsync(500)
    expect(keys()).toEqual(['settings', 'tabs.B', 'workspaces'])
    expect(scoped(reports.find((r) => r.key === 'settings'))).toEqual({ A: { files: { root: '/a' } } })
    expect(useWorkspaceSettingsStore.getState().workspaces).toHaveProperty('B') // the orphan stays on the device, unsent
  })

  it('a reorder or a rename is not a change of the set: `settings` is not rebuilt', async () => {
    const c = start()
    await c.primeAll()
    h.gate = (payload) => (Object.hasOwn(payload as object, 'purdex-ui-settings') ? Promise.reject(new Error('settings was rebuilt')) : undefined)
    setWorkspaces((l) => [...l].reverse().map((w) => ({ ...w, name: `${w.name}!` })))
    await vi.advanceTimersByTimeAsync(500)
    expect(problems).toEqual([])
    expect(keys()).toContain('workspaces')
  })
})

describe('startCollector — primeAll', () => {
  it('reports every section, changed or not, and cancels pending timers', async () => {
    const c = start()
    await c.primeAll()
    expect(keys()).toEqual(['hosts', 'settings', 'tabs.A', 'tabs.B', 'workspaces'])
    for (const r of reports) expect(r.hash).toBe(structuralKey(r.payload))
    reports = []
    useHostStore.setState({ hostOrder: [] })
    expect(await pendingTimers()).toBe(1)
    await c.primeAll()
    expect(await pendingTimers()).toBe(0)
    expect(keys()).toEqual(['hosts', 'settings', 'tabs.A', 'tabs.B', 'workspaces'])
    reports = []
    await vi.advanceTimersByTimeAsync(5000)
    expect(reports).toEqual([])
  })

  it('reports null for a section that vanished since the last report', async () => {
    const c = start()
    await c.primeAll()
    reports = []
    setWorkspaces((l) => l.filter((w) => w.id !== 'B'))
    await c.primeAll()
    expect(reports.find((r) => r.key === 'tabs.B')).toEqual({ key: 'tabs.B', hash: null, payload: null })
    reports = []
    await c.primeAll()
    expect(keys()).toEqual(['hosts', 'settings', 'tabs.A', 'workspaces'])
  })
})

describe('startCollector — async hash', () => {
  it('payload and hash come from the same snapshot even if the store moves during the hash', async () => {
    let release = (): void => {}
    h.gate = () => new Promise<void>((r) => (release = r))
    start()
    useHostStore.setState({ hosts: { h1: host('h1', 'first') } })
    await vi.advanceTimersByTimeAsync(500)
    expect(reports).toEqual([])
    h.gate = null
    useHostStore.setState({ hosts: { h1: host('h1', 'second') } })
    release()
    await vi.advanceTimersByTimeAsync(0)
    expect(reports).toHaveLength(1)
    expect((reports[0].payload as { hosts: Record<string, { name: string }> }).hosts.h1.name).toBe('first')
    expect(reports[0].hash).toBe(structuralKey(reports[0].payload))
    await vi.advanceTimersByTimeAsync(500)
    expect(reports).toHaveLength(2)
    expect(reports[1].hash).toBe(structuralKey(reports[1].payload))
    expect(reports[1].hash).toContain('second')
  })

  it('an older hash that completes after a newer one is dropped', async () => {
    let releaseOld = (): void => {}
    h.gate = (payload) =>
      JSON.stringify(payload).includes('"old"') ? new Promise<void>((r) => (releaseOld = r)) : undefined
    start()
    useHostStore.setState({ hosts: { h1: host('h1', 'old') } })
    await vi.advanceTimersByTimeAsync(500) // old: in flight, held
    useHostStore.setState({ hosts: { h1: host('h1', 'new') } })
    await vi.advanceTimersByTimeAsync(500) // new: reported
    expect(reports).toHaveLength(1)
    releaseOld()
    await vi.advanceTimersByTimeAsync(0)
    expect(reports).toHaveLength(1)
    expect(reports[0].hash).toContain('new')
  })
})

describe('startCollector — stop', () => {
  it('leaves no timer and reports nothing, not even for a hash already in flight', async () => {
    let release = (): void => {}
    h.gate = (payload) => (JSON.stringify(payload).includes('"held"') ? new Promise<void>((r) => (release = r)) : undefined)
    const c = start()
    useHostStore.setState({ hosts: { h1: host('h1', 'held') } })
    await vi.advanceTimersByTimeAsync(500) // hosts hash in flight
    useUISettingsStore.setState({ keepAliveCount: 4 }) // a pending timer
    expect(await pendingTimers()).toBe(1)
    c.stop()
    expect(await pendingTimers()).toBe(0)
    release()
    await vi.advanceTimersByTimeAsync(5000)
    patchTab('t1', { pinned: true }) // unsubscribed
    expect(await pendingTimers()).toBe(0)
    await c.primeAll()
    expect(reports).toEqual([])
  })
})

describe('watchUnsyncedStores', () => {
  // The list is EMPTY today: the one projected store that never registered with
  // syncManager was `purdex-editor-settings`, and it left the profile (it is
  // device-local by its own header). The mechanism stays for the day a projected
  // store forgets to register — the first test is what notices.
  let unwatch: (() => void) | null = null

  function storageEvent(key: string | null): void {
    window.dispatchEvent(new StorageEvent('storage', { key }))
  }

  beforeEach(() => vi.useRealTimers())
  afterEach(() => {
    unwatch?.()
    unwatch = null
    vi.restoreAllMocks()
  })

  it('UNSYNCED_SETTINGS_KEYS is exactly the projected stores that never registered with syncManager — none today', () => {
    // Fails the day a projected store starts (or stops) calling syncManager.register.
    const projected = [...new Set(PROJECTIONS.settings.map((p) => p.slice(0, p.indexOf('.'))))]
    expect(projected.sort()).toEqual(EIGHT_KEYS)
    expect(h.registered).toContain('purdex-ui-settings') // the recorder is live
    expect([...UNSYNCED_SETTINGS_KEYS].sort()).toEqual(projected.filter((k) => !h.registered.includes(k)).sort())
    expect(UNSYNCED_SETTINGS_KEYS).toEqual([])
  })

  it('rehydrates nothing: neither a registered store a second time, nor the device-local editor store', () => {
    const spies = [...SETTINGS_STORES, useEditorSettingsStore].map((s) => vi.spyOn(s.persist, 'rehydrate'))
    unwatch = watchUnsyncedStores()
    for (const key of [...EIGHT_KEYS, 'purdex-editor-settings', 'something-else', null]) storageEvent(key)
    for (const spy of spies) expect(spy).not.toHaveBeenCalled()
  })

  it('removes its listener when unwatched', () => {
    const add = vi.spyOn(window, 'addEventListener')
    const remove = vi.spyOn(window, 'removeEventListener')
    watchUnsyncedStores()()
    const listener = add.mock.calls.find(([type]) => type === 'storage')?.[1]
    expect(listener).toBeDefined()
    expect(remove).toHaveBeenCalledWith('storage', listener)
  })
})
