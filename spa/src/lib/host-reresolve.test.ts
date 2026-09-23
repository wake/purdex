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
import { syncIdOfSync } from './profile/host-identity'
import type { PaneContent, PaneLayout, Tab } from '../types/tab'
import {
  HOST_RERESOLVE_LOCK_OWNER,
  HOST_RERESOLVE_RETRY_MS,
  __resetHostReresolveForTest,
  requestHostReresolve,
  runHostReresolve,
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
  useTabStore.setState({
    tabs: {
      t1: tab('t1', { type: 'split', id: 's', direction: 'h', sizes: [50, 50], children: [leaf('p1', tmux(WIRE)), leaf('p2', tmux(UNKNOWN))] }),
      t2: tab('t2', leaf('p3', { kind: 'editor', source: { type: 'daemon', hostId: WIRE }, filePath: '/a' })),
      t3: tab('t3', leaf('p4', { kind: 'execution', executionId: 'e', host: WIRE })),
      t4: tab('t4', leaf('p5', tmux(LOCAL))),
    },
    tabOrder: ['t1', 't2', 't3', 't4'],
  })
  useLocalProfilesStore.setState({
    parkedMaster: world({ m1: tab('m1', leaf('pm', tmux(WIRE))), m2: tab('m2', leaf('pm2', tmux(UNKNOWN))) }),
    slaves: { s1: { id: 's1', name: 'S', createdAt: 1, world: world({ x1: tab('x1', leaf('px', { kind: 'execution', executionId: 'e', host: WIRE })) }) } },
    slaveOrder: ['s1'],
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
