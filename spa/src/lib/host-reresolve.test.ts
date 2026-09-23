// spa/src/lib/host-reresolve.test.ts — the host re-resolve pass (host ownership spec §3.3, plan H1b T3).
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useHostStore } from '../stores/useHostStore'
import type { HostConfig } from '../stores/useHostStore'
import { useTabStore } from '../stores/useTabStore'
import { useLocalProfilesStore } from '../stores/useLocalProfilesStore'
import type { ParkedWorld } from '../stores/useLocalProfilesStore'
import { useHostSettingsStore } from '../stores/useHostSettingsStore'
import { useNewTabLayoutStore } from '../stores/useNewTabLayoutStore'
import { useRebuildStore } from '../stores/useRebuildStore'
import { useWorkspaceStore } from '../features/workspace/store'
import { __resetMasterWorldForTest } from './profile/master-world'
import { syncIdOfSync } from './profile/host-identity'
import { STORAGE_KEYS } from './storage'
import type { PaneContent, PaneLayout, Tab } from '../types/tab'
import {
  HOST_RERESOLVE_LOCK_OWNER,
  HOST_RERESOLVE_MAX_RETRY_MS,
  HOST_RERESOLVE_RETRY_MS,
  __resetHostReresolveForTest,
  requestHostReresolve,
  runHostReresolve,
  startHostReresolve,
} from './host-reresolve'

const DAEMON = 'air-lab:26aaaa'
const WIRE = syncIdOfSync(DAEMON)
const UNKNOWN = syncIdOfSync('nowhere:000000')
const LOCAL = 'loc001'

const host = (id: string, over: Partial<HostConfig> = {}): HostConfig => ({ id, name: id, ip: '10.0.0.1', port: 7860, order: 0, ...over })
const leaf = (id: string, content: PaneContent): PaneLayout => ({ type: 'leaf', pane: { id, content } })
const tmux = (hostId: string): PaneContent => ({ kind: 'tmux-session', hostId, sessionCode: 'c', mode: 'terminal', cachedName: 'n', tmuxInstance: 'i' })
const tab = (id: string, layout: PaneLayout): Tab => ({ id, pinned: false, locked: false, createdAt: 1, layout })
const world = (tabs: Record<string, Tab>): ParkedWorld => ({ tabs, workspaces: [{ id: 'w', name: 'w', tabs: Object.keys(tabs), activeTabId: null }], activeWorkspaceId: 'w', activeTabId: null })

function hostIdsIn(value: unknown): string[] {
  return [...JSON.stringify(value).matchAll(/"(?:hostId|host)":"([^"]*)"/g)].map((m) => m[1])
}

/** Every store the pass touches holds `WIRE`, `UNKNOWN` and `LOCAL` references. */
function seed(): void {
  useHostStore.setState({ hosts: { [LOCAL]: host(LOCAL, { daemonId: DAEMON }) }, hostOrder: [LOCAL], activeHostId: LOCAL })
  // A settled world: local profile `on` on screen, the master and another local profile (`s1`) parked.
  useTabStore.setState({
    tabs: {
      t1: tab('t1', { type: 'split', id: 's', direction: 'h', sizes: [50, 50], children: [leaf('p1', tmux(WIRE)), leaf('p2', tmux(UNKNOWN))] }),
      t2: tab('t2', leaf('p3', { kind: 'editor', source: { type: 'daemon', hostId: WIRE }, filePath: '/a' })),
      t3: tab('t3', leaf('p4', { kind: 'execution', executionId: 'e', host: WIRE })),
      t4: tab('t4', leaf('p5', tmux(LOCAL))),
    },
    tabOrder: ['t1', 't2', 't3', 't4'],
    worldId: 'on',
    worldEpoch: 1,
  })
  useWorkspaceStore.setState({ workspaces: [{ id: 'won', name: 'on', tabs: ['t1', 't2', 't3', 't4'], activeTabId: 't1' }], activeWorkspaceId: 'won', worldId: 'on', worldEpoch: 1 })
  useLocalProfilesStore.setState({
    activeProfileId: 'on',
    worldEpoch: 1,
    parkedMaster: world({ m1: tab('m1', leaf('pm', tmux(WIRE))), m2: tab('m2', leaf('pm2', tmux(UNKNOWN))) }),
    slaves: {
      on: { id: 'on', name: 'On', createdAt: 1, world: null },
      s1: { id: 's1', name: 'S', createdAt: 1, world: world({ x1: tab('x1', leaf('px', { kind: 'execution', executionId: 'e', host: WIRE })) }) },
    },
    slaveOrder: ['on', 's1'],
  })
  useHostSettingsStore.setState({ hosts: { [WIRE]: { editor: { homePath: '/w' } }, [UNKNOWN]: { editor: { homePath: '/u' } } } })
  useNewTabLayoutStore.setState({
    presets: {
      '3col': { enabled: true, columns: [[`sessions:${WIRE}`], [`headless:${WIRE}`], [`sessions:${UNKNOWN}`]] },
      '2col': { enabled: false, columns: [['browser'], []] },
      '1col': { enabled: true, columns: [['browser', `sessions:${WIRE}`, `headless:${UNKNOWN}`]] },
    },
    knownIds: ['browser', `sessions:${WIRE}`, `headless:${WIRE}`, `sessions:${UNKNOWN}`, `headless:${UNKNOWN}`],
  })
}

function snapshot() {
  return {
    tabs: useTabStore.getState().tabs,
    profiles: useLocalProfilesStore.getState(),
    hostSettings: useHostSettingsStore.getState().hosts,
    newtab: useNewTabLayoutStore.getState(),
  }
}

beforeEach(() => {
  localStorage.clear()
  __resetMasterWorldForTest()
  __resetHostReresolveForTest()
  useRebuildStore.setState({ lockedBy: null, lockGrant: null })
  seed()
})

afterEach(() => {
  vi.useRealTimers()
  __resetHostReresolveForTest()
})

describe('runHostReresolve', () => {
  it('rewrites a resolvable wire id to the local id in the tab store (every host-bearing field)', () => {
    expect(runHostReresolve()).toBe('done')
    expect(hostIdsIn(useTabStore.getState().tabs).sort()).toEqual([LOCAL, LOCAL, LOCAL, LOCAL, UNKNOWN].sort())
  })

  it('rewrites the parked master AND a parked slave', () => {
    runHostReresolve()
    const { parkedMaster, slaves } = useLocalProfilesStore.getState()
    expect(hostIdsIn(parkedMaster).sort()).toEqual([LOCAL, UNKNOWN].sort())
    expect(hostIdsIn(slaves.s1.world)).toEqual([LOCAL])
  })

  it('re-keys host settings and renames both column kinds in every preset and knownIds', () => {
    runHostReresolve()
    expect(useHostSettingsStore.getState().hosts).toEqual({ [LOCAL]: { editor: { homePath: '/w' } }, [UNKNOWN]: { editor: { homePath: '/u' } } })
    const { presets, knownIds } = useNewTabLayoutStore.getState()
    expect(presets['3col'].columns).toEqual([[`sessions:${LOCAL}`], [`headless:${LOCAL}`], [`sessions:${UNKNOWN}`]])
    expect(presets['1col'].columns).toEqual([['browser', `sessions:${LOCAL}`, `headless:${UNKNOWN}`]])
    expect(knownIds).toEqual(['browser', `sessions:${LOCAL}`, `headless:${LOCAL}`, `sessions:${UNKNOWN}`, `headless:${UNKNOWN}`])
  })

  it('host settings in both forms: the sync-id entry wins (the rekeyEntries rule, plan §0.11)', () => {
    useHostSettingsStore.setState({ hosts: { [LOCAL]: { editor: { homePath: '/local' } }, [WIRE]: { editor: { homePath: '/wire' } } } })
    runHostReresolve()
    expect(useHostSettingsStore.getState().hosts).toEqual({ [LOCAL]: { editor: { homePath: '/wire' } } })
  })

  it('an unresolvable id is untouched; so is an untouched store\'s object', () => {
    useTabStore.setState({ tabs: { u: tab('u', leaf('pu', tmux(UNKNOWN))) }, tabOrder: ['u'] })
    const u = useTabStore.getState().tabs
    runHostReresolve()
    expect(useTabStore.getState().tabs).toBe(u)
  })

  it('a LOCAL id is never re-resolved, even when it is another host\'s legacy alias', () => {
    // `aaaaaa` is a host here AND listed as the other host's alias: resolving it would move A's panes to B.
    useHostStore.setState({
      hosts: { aaaaaa: host('aaaaaa'), [LOCAL]: host(LOCAL, { daemonId: DAEMON, syncAliases: ['aaaaaa'], order: 1 }) },
      hostOrder: ['aaaaaa', LOCAL],
    })
    useTabStore.setState({ tabs: { a: tab('a', leaf('pa', tmux('aaaaaa'))) }, tabOrder: ['a'] })
    runHostReresolve()
    expect(hostIdsIn(useTabStore.getState().tabs)).toEqual(['aaaaaa'])
  })

  it('under an identity conflict: nothing is written — not even a non-conflicting host\'s refs — and no lock taken', () => {
    const Y_DAEMON = 'why-lab:yyyyyy'
    useHostStore.setState({
      hosts: { [LOCAL]: host(LOCAL, { daemonId: DAEMON }), dup: host('dup', { daemonId: DAEMON, order: 1 }), yloc: host('yloc', { daemonId: Y_DAEMON, order: 2 }) },
      hostOrder: [LOCAL, 'dup', 'yloc'],
    })
    useTabStore.setState({ tabs: { ...useTabStore.getState().tabs, y: tab('y', leaf('py', tmux(syncIdOfSync(Y_DAEMON)))) } })
    const before = snapshot()
    expect(runHostReresolve()).toBe('conflict')
    expect(snapshot()).toEqual(before)
    expect(snapshot().tabs).toBe(before.tabs)
    expect(useRebuildStore.getState().lockedBy).toBeNull()
  })

  it('holds the operation lock while it writes, and releases it', () => {
    let heldBy: string | null = null
    const unsub = useTabStore.subscribe(() => { heldBy = useRebuildStore.getState().lockedBy })
    runHostReresolve()
    unsub()
    expect(heldBy).toBe(HOST_RERESOLVE_LOCK_OWNER)
    expect(useRebuildStore.getState().lockedBy).toBeNull()
  })

  it('the lock held elsewhere → busy, nothing written', () => {
    const grant = useRebuildStore.getState().acquireOperationLock('someone-else')
    const before = snapshot()
    expect(runHostReresolve()).toBe('busy')
    expect(snapshot().tabs).toBe(before.tabs)
    useRebuildStore.getState().releaseOperationLock(grant)
  })

  it('nothing to move → the operation lock is never taken (a release reconciles every host\'s sessions)', () => {
    runHostReresolve()
    const seen: (string | null)[] = []
    const unsub = useRebuildStore.subscribe((st) => { seen.push(st.lockedBy) })
    expect(runHostReresolve()).toBe('done')
    unsub()
    expect(seen).toEqual([])
  })

  it('idempotent: a second run writes nothing', () => {
    runHostReresolve()
    const after = snapshot()
    expect(runHostReresolve()).toBe('done')
    const again = snapshot()
    expect(again.tabs).toBe(after.tabs)
    expect(again.profiles).toBe(after.profiles)
    expect(again.hostSettings).toBe(after.hostSettings)
    expect(again.newtab).toBe(after.newtab)
  })
})

// PR #1406 review (R1 P2, attacker high #2): the four stores are written as one unit. A write that fails — the
// persist's `setItem` throwing (quota, SecurityError) AFTER zustand already changed memory — puts back every store
// already written, the failing one included; the pass says `write-failed` instead of throwing, and is retried.
describe('a store write fails half-way', () => {
  const WRITTEN = [STORAGE_KEYS.TABS, STORAGE_KEYS.LOCAL_PROFILES, STORAGE_KEYS.HOST_SETTINGS, STORAGE_KEYS.NEW_TAB_LAYOUT] as const

  function failWrites(key: string, times: number): void {
    const real = Storage.prototype.setItem
    let left = times
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(function (this: Storage, k: string, v: string) {
      if (k === key && left > 0) {
        left--
        throw new DOMException('quota', 'QuotaExceededError')
      }
      real.call(this, k, v)
    })
  }

  function everything(): string {
    return JSON.stringify([
      useTabStore.getState().tabs,
      useLocalProfilesStore.getState().parkedMaster,
      useLocalProfilesStore.getState().slaves,
      useHostSettingsStore.getState().hosts,
      useNewTabLayoutStore.getState().presets,
      useNewTabLayoutStore.getState().knownIds,
      ...WRITTEN.map((k) => localStorage.getItem(k)),
    ])
  }

  afterEach(() => { vi.restoreAllMocks() })

  it.each(WRITTEN)('%s fails once → all or nothing: every store as before, memory and storage; then the retry lands', (key) => {
    vi.useFakeTimers()
    const before = everything()
    failWrites(key, 1)
    requestHostReresolve()
    expect(everything()).toBe(before)
    expect(useRebuildStore.getState().lockedBy).toBeNull()
    vi.advanceTimersByTime(HOST_RERESOLVE_RETRY_MS)
    expect(hostIdsIn(useTabStore.getState().tabs)).not.toContain(WIRE)
    expect(JSON.stringify(useNewTabLayoutStore.getState())).not.toContain(WIRE)
    expect(Object.keys(useHostSettingsStore.getState().hosts)).not.toContain(WIRE)
  })

  it('the pass answers write-failed; it never throws', () => {
    failWrites(STORAGE_KEYS.HOST_SETTINGS, 1)
    expect(runHostReresolve()).toBe('write-failed')
  })

  it('a rollback that fails too is reported, not thrown', () => {
    const report = vi.spyOn(console, 'error').mockImplementation(() => {})
    // tabs is written (and persisted) first; the host-settings write then fails, and so does putting tabs back
    const real = Storage.prototype.setItem
    let tabsWrites = 0
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(function (this: Storage, k: string, v: string) {
      if (k === STORAGE_KEYS.HOST_SETTINGS) throw new DOMException('quota', 'QuotaExceededError')
      if (k === STORAGE_KEYS.TABS && ++tabsWrites > 1) throw new DOMException('quota', 'QuotaExceededError')
      real.call(this, k, v)
    })
    expect(runHostReresolve()).toBe('write-failed')
    expect(report).toHaveBeenCalled()
    expect(String(report.mock.calls[0][0])).toMatch(/rollback incomplete/)
  })

  it('a write that keeps failing is retried with backoff, never tighter than every 500 ms, capped', () => {
    vi.useFakeTimers()
    vi.spyOn(console, 'error').mockImplementation(() => {})
    let attempts = 0
    const real = Storage.prototype.setItem
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(function (this: Storage, k: string, v: string) {
      if (k === STORAGE_KEYS.TABS) {
        attempts++
        throw new DOMException('quota', 'QuotaExceededError')
      }
      real.call(this, k, v)
    })
    requestHostReresolve()
    const writesPerAttempt = attempts
    vi.advanceTimersByTime(HOST_RERESOLVE_RETRY_MS)
    expect(attempts).toBe(writesPerAttempt * 2) // retry 1 at 500 ms
    vi.advanceTimersByTime(HOST_RERESOLVE_RETRY_MS)
    expect(attempts).toBe(writesPerAttempt * 2) // retry 2 not before 1000 ms
    vi.advanceTimersByTime(HOST_RERESOLVE_RETRY_MS)
    expect(attempts).toBe(writesPerAttempt * 3)
    vi.advanceTimersByTime(HOST_RERESOLVE_MAX_RETRY_MS * 10)
    expect(attempts).toBeLessThanOrEqual(writesPerAttempt * 20) // backed off to the cap: ~16 attempts, not ~600
  })
})

// PR #1406 attacker high #1, narrowed (no CAS — the #1256 residual stands): the pass re-reads what the stores hold
// ON DISK before it plans, so a value another window wrote before the pass began is kept, not overwritten.
describe('another window wrote first', () => {
  /** What another window's store wrote: storage only — this window's memory has not heard of it yet. */
  function writeElsewhere(key: string, edit: (state: Record<string, unknown>) => void): void {
    const envelope = JSON.parse(localStorage.getItem(key)!) as { state: Record<string, unknown>; version: number }
    edit(envelope.state)
    localStorage.setItem(key, JSON.stringify(envelope))
  }

  it('host settings: the other window\'s new key survives, and the wire key still moves', () => {
    writeElsewhere(STORAGE_KEYS.HOST_SETTINGS, (st) => { (st.hosts as Record<string, unknown>).fresh1 = { editor: { homePath: '/fresh' } } })
    runHostReresolve()
    expect(useHostSettingsStore.getState().hosts).toMatchObject({ fresh1: { editor: { homePath: '/fresh' } }, [LOCAL]: { editor: { homePath: '/w' } } })
    expect(JSON.parse(localStorage.getItem(STORAGE_KEYS.HOST_SETTINGS)!).state.hosts.fresh1).toBeDefined()
  })

  it('New Tab layout: a column the other window placed survives', () => {
    writeElsewhere(STORAGE_KEYS.NEW_TAB_LAYOUT, (st) => { ((st.presets as Record<string, { columns: string[][] }>)['2col'].columns[1]).push('editor') })
    runHostReresolve()
    expect(useNewTabLayoutStore.getState().presets['2col'].columns[1]).toEqual(['editor'])
  })

  it('tabs on screen: a tab the other window opened survives', () => {
    writeElsewhere(STORAGE_KEYS.TABS, (st) => { (st.tabs as Record<string, unknown>).fresh = tab('fresh', leaf('pf', tmux(WIRE))); (st.tabOrder as string[]).push('fresh') })
    runHostReresolve()
    expect(Object.keys(useTabStore.getState().tabs)).toContain('fresh')
    expect(hostIdsIn(useTabStore.getState().tabs)).not.toContain(WIRE)
  })

  it('parked worlds: a tab the other window parked survives', () => {
    writeElsewhere(STORAGE_KEYS.LOCAL_PROFILES, (st) => { ((st.parkedMaster as ParkedWorld).tabs).fresh = tab('fresh', leaf('pf', tmux(WIRE))) })
    runHostReresolve()
    const parked = useLocalProfilesStore.getState().parkedMaster!
    expect(Object.keys(parked.tabs)).toContain('fresh')
    expect(hostIdsIn(parked)).not.toContain(WIRE)
  })

  it('a world the re-read shows unsettled (another window mid-switch) is not written: busy', () => {
    writeElsewhere(STORAGE_KEYS.TABS, (st) => { st.worldEpoch = 7 })
    const before = useLocalProfilesStore.getState().parkedMaster
    expect(runHostReresolve()).toBe('busy')
    expect(useLocalProfilesStore.getState().parkedMaster).toBe(before)
  })
})

describe('requestHostReresolve', () => {
  it('busy → retried every 500 ms until the lock is free, then runs', () => {
    vi.useFakeTimers()
    const grant = useRebuildStore.getState().acquireOperationLock('someone-else')
    requestHostReresolve()
    expect(hostIdsIn(useTabStore.getState().tabs)).toContain(WIRE)
    vi.advanceTimersByTime(HOST_RERESOLVE_RETRY_MS)
    expect(hostIdsIn(useTabStore.getState().tabs)).toContain(WIRE) // still held
    useRebuildStore.getState().releaseOperationLock(grant)
    vi.advanceTimersByTime(HOST_RERESOLVE_RETRY_MS)
    expect(hostIdsIn(useTabStore.getState().tabs)).not.toContain(WIRE)
  })

  it('a newer request supersedes a pending retry: two busy requests make ONE retry; a run cancels the retry', () => {
    vi.useFakeTimers()
    let runs = 0
    const unsub = useRebuildStore.subscribe((st, prev) => { if (st.lockedBy === HOST_RERESOLVE_LOCK_OWNER && prev.lockedBy !== HOST_RERESOLVE_LOCK_OWNER) runs++ })
    try {
      let grant = useRebuildStore.getState().acquireOperationLock('someone-else')
      requestHostReresolve()
      requestHostReresolve()
      useRebuildStore.getState().releaseOperationLock(grant)
      vi.advanceTimersByTime(HOST_RERESOLVE_RETRY_MS * 3)
      expect(runs).toBe(1)

      seed() // something to move again
      grant = useRebuildStore.getState().acquireOperationLock('someone-else')
      requestHostReresolve()
      useRebuildStore.getState().releaseOperationLock(grant)
      requestHostReresolve() // runs now…
      expect(runs).toBe(2)
      vi.advanceTimersByTime(HOST_RERESOLVE_RETRY_MS * 3)
      expect(runs).toBe(2) // …and the pending retry is gone
    } finally {
      unsub()
    }
  })
})

describe('startHostReresolve (the triggers)', () => {
  let stop: (() => void) | null = null
  let runs = 0
  let unsubRuns: (() => void) | null = null

  beforeEach(() => {
    runs = 0
    unsubRuns = useRebuildStore.subscribe((st, prev) => { if (st.lockedBy === HOST_RERESOLVE_LOCK_OWNER && prev.lockedBy !== HOST_RERESOLVE_LOCK_OWNER) runs++ })
    // Start from a device that does NOT have the daemon yet: every WIRE ref is unresolvable.
    useHostStore.setState({ hosts: { other: host('other') }, hostOrder: ['other'], activeHostId: 'other' })
  })

  afterEach(() => {
    stop?.()
    stop = null
    unsubRuns?.()
    vi.restoreAllMocks()
  })

  const addHostX = (over: Partial<HostConfig> = {}) =>
    useHostStore.setState((s) => ({ hosts: { ...s.hosts, [LOCAL]: host(LOCAL, { daemonId: DAEMON, order: 1, ...over }) }, hostOrder: [...s.hostOrder, LOCAL] }))

  it('runs once at start when every store has hydrated', () => {
    addHostX()
    stop = startHostReresolve()
    expect(runs).toBe(1)
    expect(hostIdsIn(useTabStore.getState().tabs)).not.toContain(WIRE)
  })

  it('adding a host whose daemon the refs name → they point at it', () => {
    stop = startHostReresolve()
    addHostX()
    expect(hostIdsIn(useTabStore.getState().tabs)).not.toContain(WIRE)
    expect(Object.keys(useHostSettingsStore.getState().hosts)).toContain(LOCAL)
  })

  it('learning a daemonId triggers', () => {
    addHostX({ daemonId: undefined })
    stop = startHostReresolve()
    expect(hostIdsIn(useTabStore.getState().tabs)).toContain(WIRE)
    useHostStore.setState((s) => ({ hosts: { ...s.hosts, [LOCAL]: { ...s.hosts[LOCAL], daemonId: DAEMON } } }))
    expect(hostIdsIn(useTabStore.getState().tabs)).not.toContain(WIRE)
  })

  it('changing syncAliases triggers (a legacy id becomes resolvable)', () => {
    addHostX()
    useTabStore.setState({ tabs: { l: tab('l', leaf('pl', tmux('legacy1'))) }, tabOrder: ['l'] })
    stop = startHostReresolve()
    expect(hostIdsIn(useTabStore.getState().tabs)).toEqual(['legacy1'])
    useHostStore.setState((s) => ({ hosts: { ...s.hosts, [LOCAL]: { ...s.hosts[LOCAL], syncAliases: ['legacy1'] } } }))
    expect(hostIdsIn(useTabStore.getState().tabs)).toEqual([LOCAL])
  })

  it('a rename, or runtime churn, does not trigger', () => {
    addHostX()
    stop = startHostReresolve()
    const after = runs
    useHostStore.setState((s) => ({ hosts: { ...s.hosts, [LOCAL]: { ...s.hosts[LOCAL], name: 'renamed' } } }))
    useHostStore.getState().setRuntime(LOCAL, { status: 'connected' })
    expect(runs).toBe(after)
  })

  it('waits for every store it rewrites: nothing runs while purdex-host-settings has not hydrated, then it does', () => {
    let finish: (() => void) | undefined
    const hydrated = vi.spyOn(useHostSettingsStore.persist, 'hasHydrated').mockReturnValue(false)
    vi.spyOn(useHostSettingsStore.persist, 'onFinishHydration').mockImplementation((cb) => {
      finish = () => cb(useHostSettingsStore.getState())
      return () => { finish = undefined }
    })
    addHostX()
    stop = startHostReresolve()
    expect(runs).toBe(0)
    expect(Object.keys(useHostSettingsStore.getState().hosts)).toContain(WIRE)
    hydrated.mockReturnValue(true)
    finish?.()
    expect(runs).toBe(1)
    expect(Object.keys(useHostSettingsStore.getState().hosts)).not.toContain(WIRE)
  })

  it('purdex-host-settings hydrating AFTER the first pass with a d1_ key (host present) ends on the local id', () => {
    let finish: (() => void) | undefined
    vi.spyOn(useHostSettingsStore.persist, 'onFinishHydration').mockImplementation((cb) => {
      finish = () => cb(useHostSettingsStore.getState())
      return () => { finish = undefined }
    })
    addHostX()
    stop = startHostReresolve()
    expect(runs).toBe(1)
    // another window wrote it; the rehydrate lands the wire key
    useHostSettingsStore.setState({ hosts: { [WIRE]: { editor: { homePath: '/late' } } } })
    finish?.()
    expect(useHostSettingsStore.getState().hosts).toEqual({ [LOCAL]: { editor: { homePath: '/late' } } })
  })

  it('an identity whose pass failed is not marked handled: the next host-store change asks again', () => {
    vi.useFakeTimers() // the backoff retry stays pending: only the subscription can bring it back
    vi.spyOn(console, 'warn').mockImplementation(() => {})
    stop = startHostReresolve()
    const real = Storage.prototype.setItem
    let fail = true
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(function (this: Storage, k: string, v: string) {
      if (fail && k === STORAGE_KEYS.HOST_SETTINGS) throw new DOMException('quota', 'QuotaExceededError')
      real.call(this, k, v)
    })
    addHostX()
    expect(hostIdsIn(useTabStore.getState().tabs)).toContain(WIRE) // failed, rolled back
    fail = false
    useHostStore.setState((s) => ({ hosts: { ...s.hosts, [LOCAL]: { ...s.hosts[LOCAL], name: 'renamed' } } }))
    expect(hostIdsIn(useTabStore.getState().tabs)).not.toContain(WIRE)
  })

  it('stop() ends every subscription', () => {
    stop = startHostReresolve()
    stop()
    stop = null
    addHostX()
    expect(hostIdsIn(useTabStore.getState().tabs)).toContain(WIRE)
  })
})
