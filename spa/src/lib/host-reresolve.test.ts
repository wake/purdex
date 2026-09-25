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
import { useHostLookStore } from '../stores/useHostLookStore'
import { useShownHostsStore } from '../stores/useShownHostsStore'
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
  reresolveRestoredHost,
  rewriteHostRefs,
  runHostReresolve,
  startHostReresolve,
} from './host-reresolve'
import { hostLookOf } from './host-look'
import { rekeyShownHosts } from './shown-hosts'
import { promoteToMaster } from './profile/switch-active'
import { buildSectionPayload } from './profile/collector'

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
      on: { id: 'on', name: 'On', createdAt: 1, shownHostIds: [], world: null },
      s1: { id: 's1', name: 'S', createdAt: 1, shownHostIds: [], world: world({ x1: tab('x1', leaf('px', { kind: 'execution', executionId: 'e', host: WIRE })) }) },
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
  useHostLookStore.setState({ looks: {} })
  useShownHostsStore.setState({ ids: [] })
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

// H2c-2 T5 (spec §4.3, plan §0.11 / §0.12): the look store is keyed by WIRE id, so the pass moves a look the other
// way from its map — local id → `d1_…` once the host's daemonId is known — as its own step after the ref rewrite.
describe('the look re-key (H2c-2)', () => {
  it('a local-id entry moves to the d1_ key of the host whose daemonId is known', () => {
    useHostLookStore.setState({ looks: { [LOCAL]: { name: 'air26', icon: 'Cloud' } } })
    expect(runHostReresolve()).toBe('done')
    expect(useHostLookStore.getState().looks).toEqual({ [WIRE]: { name: 'air26', icon: 'Cloud' } })
    expect(JSON.parse(localStorage.getItem(STORAGE_KEYS.HOST_LOOKS)!).state.looks).toEqual({ [WIRE]: { name: 'air26', icon: 'Cloud' } })
  })

  it('an existing d1_ entry wins; the local-id entry is dropped', () => {
    useHostLookStore.setState({ looks: { [LOCAL]: { name: 'local' }, [WIRE]: { name: 'workbench' } } })
    runHostReresolve()
    expect(useHostLookStore.getState().looks).toEqual({ [WIRE]: { name: 'workbench' } })
  })

  it('a host without a daemonId keeps its local-id entry; an unknown key is untouched', () => {
    useHostStore.setState({ hosts: { [LOCAL]: host(LOCAL, { daemonId: DAEMON }), plain: host('plain') }, hostOrder: [LOCAL, 'plain'] })
    useHostLookStore.setState({ looks: { plain: { name: 'P' }, [UNKNOWN]: { name: 'U' } } })
    const looks = useHostLookStore.getState().looks
    runHostReresolve()
    expect(useHostLookStore.getState().looks).toBe(looks)
  })

  it('under an identity conflict nothing moves', () => {
    useHostStore.setState({ hosts: { [LOCAL]: host(LOCAL, { daemonId: DAEMON }), dup: host('dup', { daemonId: DAEMON }) }, hostOrder: [LOCAL, 'dup'] })
    useHostLookStore.setState({ looks: { [LOCAL]: { name: 'local' }, dup: { name: 'dup' } } })
    const looks = useHostLookStore.getState().looks
    expect(runHostReresolve()).toBe('conflict')
    expect(useHostLookStore.getState().looks).toBe(looks)
  })

  it('the ref map never moves a look: a d1_ key whose refs the pass rewrites to the local id stays a d1_ key', () => {
    // The map sends WIRE → LOCAL (every ref above moves); a look keyed by WIRE must not follow it — the deletion
    // direction (local → wire, H1c) reuses the map rewrite and must touch no look (decision 9).
    useHostLookStore.setState({ looks: { [WIRE]: { name: 'workbench' } } })
    const looks = useHostLookStore.getState().looks
    runHostReresolve()
    expect(hostIdsIn(useTabStore.getState().tabs)).not.toContain(WIRE)
    expect(useHostLookStore.getState().looks).toBe(looks)
  })

  it('takes the operation lock for the look move alone (it is a write like any other)', () => {
    runHostReresolve() // every ref moved: nothing else to write
    useHostLookStore.setState({ looks: { [LOCAL]: { name: 'x' } } })
    const seen: (string | null)[] = []
    const unsub = useRebuildStore.subscribe((st) => seen.push(st.lockedBy))
    runHostReresolve()
    unsub()
    expect(seen).toContain(HOST_RERESOLVE_LOCK_OWNER)
    expect(useHostLookStore.getState().looks).toEqual({ [WIRE]: { name: 'x' } })
  })

  it('the lock held elsewhere → busy, no look moved', () => {
    useHostLookStore.setState({ looks: { [LOCAL]: { name: 'x' } } })
    const grant = useRebuildStore.getState().acquireOperationLock('someone-else')
    expect(runHostReresolve()).toBe('busy')
    expect(useHostLookStore.getState().looks).toEqual({ [LOCAL]: { name: 'x' } })
    useRebuildStore.getState().releaseOperationLock(grant)
  })

  it('idempotent: a second run writes nothing', () => {
    useHostLookStore.setState({ looks: { [LOCAL]: { name: 'x' } } })
    runHostReresolve()
    const looks = useHostLookStore.getState().looks
    const writes = vi.fn()
    const unsub = useHostLookStore.subscribe(writes)
    runHostReresolve()
    unsub()
    expect(writes).not.toHaveBeenCalled()
    expect(useHostLookStore.getState().looks).toBe(looks)
  })

  it('a look another window wrote under the local id (storage only) still moves: the store is re-read first', () => {
    useHostLookStore.setState({ looks: {} })
    localStorage.setItem(STORAGE_KEYS.HOST_LOOKS, JSON.stringify({ state: { looks: { [LOCAL]: { name: 'elsewhere' } } }, version: 1 }))
    runHostReresolve()
    expect(useHostLookStore.getState().looks).toEqual({ [WIRE]: { name: 'elsewhere' } })
  })

  it('a failed look write puts every store back (all or nothing); the retry lands', () => {
    vi.useFakeTimers()
    vi.spyOn(console, 'warn').mockImplementation(() => {})
    useHostLookStore.setState({ looks: { [LOCAL]: { name: 'x' } } })
    const tabs = useTabStore.getState().tabs
    const real = Storage.prototype.setItem
    let left = 1
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(function (this: Storage, k: string, v: string) {
      if (k === STORAGE_KEYS.HOST_LOOKS && left > 0) {
        left--
        throw new DOMException('quota', 'QuotaExceededError')
      }
      real.call(this, k, v)
    })
    requestHostReresolve()
    expect(useHostLookStore.getState().looks).toEqual({ [LOCAL]: { name: 'x' } })
    expect(useTabStore.getState().tabs).toEqual(tabs)
    vi.advanceTimersByTime(HOST_RERESOLVE_RETRY_MS)
    expect(useHostLookStore.getState().looks).toEqual({ [WIRE]: { name: 'x' } })
    expect(hostIdsIn(useTabStore.getState().tabs)).not.toContain(WIRE)
    vi.restoreAllMocks()
  })
})

// H2d-1 T4 (spec §4.3, plan §0.11 / §0.12): the shown-hosts ids are WIRE ids, so the pass moves a listed local id to
// the host's `d1_…` once its daemonId is known — in place, deduped keeping the first — as its own step next to the look
// re-key, never inside the ref rewrite a deletion reuses.
describe('the shown-hosts re-key (H2d-1)', () => {
  const shownNow = () => ({ ids: useShownHostsStore.getState().ids })

  it('a listed local id becomes the d1_ id of the host whose daemonId is known, in place; memory and storage agree', () => {
    useShownHostsStore.setState({ ids: [UNKNOWN, LOCAL, 'tail'] })
    expect(runHostReresolve()).toBe('done')
    expect(shownNow()).toEqual({ ids: [UNKNOWN, WIRE, 'tail'] })
    expect(JSON.parse(localStorage.getItem(STORAGE_KEYS.SHOWN_HOSTS)!).state).toEqual({ ids: [UNKNOWN, WIRE, 'tail'], relabelStamp: 0 })
  })

  it('the d1_ id already listed: the local id is dropped (deduped, the first kept)', () => {
    useShownHostsStore.setState({ ids: [WIRE, UNKNOWN, LOCAL] })
    runHostReresolve()
    expect(shownNow()).toEqual({ ids: [WIRE, UNKNOWN] })
  })

  it('a host without a daemonId keeps its local id; an unknown id is untouched; nothing written', () => {
    useHostStore.setState({ hosts: { [LOCAL]: host(LOCAL, { daemonId: DAEMON }), plain: host('plain') }, hostOrder: [LOCAL, 'plain'] })
    useShownHostsStore.setState({ ids: ['plain', UNKNOWN, WIRE] })
    const before = useShownHostsStore.getState()
    runHostReresolve()
    expect(useShownHostsStore.getState()).toBe(before)
  })

  it('under an identity conflict nothing moves', () => {
    useHostStore.setState({ hosts: { [LOCAL]: host(LOCAL, { daemonId: DAEMON }), dup: host('dup', { daemonId: DAEMON }) }, hostOrder: [LOCAL, 'dup'] })
    useShownHostsStore.setState({ ids: [LOCAL, 'dup'] })
    const before = useShownHostsStore.getState()
    expect(runHostReresolve()).toBe('conflict')
    expect(useShownHostsStore.getState()).toBe(before)
  })

  it('takes the operation lock for the shown-id move alone; the lock held elsewhere → busy, nothing moved', () => {
    runHostReresolve() // every ref moved: nothing else to write
    useShownHostsStore.setState({ ids: [LOCAL] })
    const grant = useRebuildStore.getState().acquireOperationLock('someone-else')
    expect(runHostReresolve()).toBe('busy')
    expect(shownNow().ids).toEqual([LOCAL])
    useRebuildStore.getState().releaseOperationLock(grant)
    const seen: (string | null)[] = []
    const unsub = useRebuildStore.subscribe((st) => seen.push(st.lockedBy))
    runHostReresolve()
    unsub()
    expect(seen).toContain(HOST_RERESOLVE_LOCK_OWNER)
    expect(shownNow().ids).toEqual([WIRE])
  })

  it('idempotent: a second run writes nothing', () => {
    useShownHostsStore.setState({ ids: [LOCAL] })
    runHostReresolve()
    const writes = vi.fn()
    const unsub = useShownHostsStore.subscribe(writes)
    runHostReresolve()
    unsub()
    expect(writes).not.toHaveBeenCalled()
  })

  it('nothing listed that moves → rekeyShownHosts is null and the pass takes no lock for it', () => {
    runHostReresolve() // every ref moved: nothing else to write
    for (const ids of [[], [UNKNOWN, WIRE]]) {
      useShownHostsStore.setState({ ids })
      expect(rekeyShownHosts(useHostStore.getState().hosts)).toBeNull()
      const seen: (string | null)[] = []
      const unsub = useRebuildStore.subscribe((st) => seen.push(st.lockedBy))
      expect(runHostReresolve()).toBe('done')
      unsub()
      expect(seen).toEqual([])
      expect(shownNow().ids).toEqual(ids)
    }
  })

  it('an id another window listed (storage only) still moves: the store is re-read first', () => {
    localStorage.setItem(STORAGE_KEYS.SHOWN_HOSTS, JSON.stringify({ state: { ids: [LOCAL] }, version: 1 }))
    runHostReresolve()
    expect(shownNow()).toEqual({ ids: [WIRE] })
  })

  it('a failed shown-hosts write puts every store back (all or nothing); the retry lands', () => {
    vi.useFakeTimers()
    vi.spyOn(console, 'warn').mockImplementation(() => {})
    useShownHostsStore.setState({ ids: [LOCAL] })
    const tabs = useTabStore.getState().tabs
    const real = Storage.prototype.setItem
    let left = 1
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(function (this: Storage, k: string, v: string) {
      if (k === STORAGE_KEYS.SHOWN_HOSTS && left > 0) {
        left--
        throw new DOMException('quota', 'QuotaExceededError')
      }
      real.call(this, k, v)
    })
    try {
      requestHostReresolve()
      expect(shownNow().ids).toEqual([LOCAL])
      expect(useTabStore.getState().tabs).toEqual(tabs)
      vi.advanceTimersByTime(HOST_RERESOLVE_RETRY_MS)
      expect(shownNow().ids).toEqual([WIRE])
    } finally {
      vi.restoreAllMocks() // a failure must not leave the setItem spy on the next test
    }
  })
})

// Per-workbench shown hosts (2026-09-25 plan, A5): each local workbench's own list is re-keyed with the master's, by
// the same moves, in the same step — two stores, one step, one undo that restores both.
describe('the shown-hosts re-key — every workbench\'s list (A5)', () => {
  const lists = () => Object.fromEntries(Object.entries(useLocalProfilesStore.getState().slaves).map(([id, s]) => [id, s.shownHostIds]))
  const setLists = (byId: Record<string, string[]>) =>
    useLocalProfilesStore.setState((st) => ({ slaves: Object.fromEntries(Object.entries(st.slaves).map(([id, s]) => [id, { ...s, shownHostIds: byId[id] ?? s.shownHostIds }])) }))

  it("a slave's listed local id becomes the d1_ id, in place, dedup as the master's; memory and storage agree", () => {
    useShownHostsStore.setState({ ids: [LOCAL] })
    setLists({ on: [UNKNOWN, LOCAL, 'tail'], s1: [WIRE, LOCAL] })
    expect(runHostReresolve()).toBe('done')
    expect(useShownHostsStore.getState().ids).toEqual([WIRE])
    expect(lists()).toEqual({ on: [UNKNOWN, WIRE, 'tail'], s1: [WIRE] })
    const stored = JSON.parse(localStorage.getItem(STORAGE_KEYS.LOCAL_PROFILES)!).state.slaves
    expect(stored.on.shownHostIds).toEqual([UNKNOWN, WIRE, 'tail'])
  })

  it('only a slave lists the id: it still moves (the step exists for the slaves alone)', () => {
    setLists({ s1: [LOCAL] })
    expect(rekeyShownHosts(useHostStore.getState().hosts)).not.toBeNull()
    runHostReresolve()
    expect(lists().s1).toEqual([WIRE])
    expect(useShownHostsStore.getState().ids).toEqual([])
  })

  it("the slave write is read AT COMMIT: the parked-worlds write of the same pass is kept, not clobbered (m3)", () => {
    setLists({ s1: [LOCAL] }) // s1's parked world holds WIRE refs: planRewrite's 'parked worlds' write rewrites them
    expect(runHostReresolve()).toBe('done')
    const s1 = useLocalProfilesStore.getState().slaves.s1
    expect(hostIdsIn(s1.world)).toEqual([LOCAL]) // the world rewritten (WIRE → LOCAL)…
    expect(s1.shownHostIds).toEqual([WIRE]) // …and the list re-keyed (LOCAL → WIRE), both
  })

  it('a deletion (rewriteHostRefs) never re-keys a list — the master\'s or a slave\'s', () => {
    useShownHostsStore.setState({ ids: [LOCAL] })
    setLists({ s1: [LOCAL], on: [WIRE] })
    expect(rewriteHostRefs({ [LOCAL]: WIRE })).toBe('ok')
    expect(useShownHostsStore.getState().ids).toEqual([LOCAL])
    expect(lists()).toEqual({ on: [WIRE], s1: [LOCAL] })
  })

  describe('two stores, one step', () => {
    /** Nothing else moves: the shown-hosts step is the only one (a first pass moved every other ref). */
    beforeEach(() => {
      runHostReresolve()
      useShownHostsStore.setState({ ids: [LOCAL] })
      setLists({ s1: [LOCAL] })
    })
    afterEach(() => { vi.restoreAllMocks() })

    /** The step's slave write — the only local-profiles write of this pass — sets memory, then its storage throws. */
    function failSlaveWrite(times: number): void {
      const real = Storage.prototype.setItem
      let left = times
      vi.spyOn(Storage.prototype, 'setItem').mockImplementation(function (this: Storage, k: string, v: string) {
        if (k === STORAGE_KEYS.LOCAL_PROFILES && left > 0) {
          left--
          throw new DOMException('quota', 'QuotaExceededError')
        }
        real.call(this, k, v)
      })
    }

    it('the second write (the slaves) fails → both stores restored; write-failed; the retry lands (m8)', () => {
      vi.useFakeTimers()
      vi.spyOn(console, 'warn').mockImplementation(() => {})
      failSlaveWrite(1)
      requestHostReresolve()
      expect(useShownHostsStore.getState().ids).toEqual([LOCAL])
      expect(lists().s1).toEqual([LOCAL])
      vi.advanceTimersByTime(HOST_RERESOLVE_RETRY_MS)
      expect(useShownHostsStore.getState().ids).toEqual([WIRE])
      expect(lists().s1).toEqual([WIRE])
    })

    it('the undo fails on its first restore → it still attempts the second, then throws one error: rollback-failed; the rerun rolls forward (m12)', () => {
      vi.useFakeTimers()
      vi.spyOn(console, 'error').mockImplementation(() => {})
      vi.spyOn(console, 'warn').mockImplementation(() => {})
      failSlaveWrite(1)
      const real = useShownHostsStore.setState
      let call = 0
      vi.spyOn(useShownHostsStore, 'setState').mockImplementation((...args) => {
        if (++call === 2) throw new Error('master restore failed') // call 1: the commit's write; call 2: its restore
        real(...(args as Parameters<typeof real>))
      })
      expect(runHostReresolve()).toBe('rollback-failed')
      expect(lists().s1).toEqual([LOCAL]) // the second restore was attempted although the first threw
      expect(String(vi.mocked(console.error).mock.calls[0][0])).toMatch(/shown hosts/)
      requestHostReresolve()
      expect(useShownHostsStore.getState().ids).toEqual([WIRE])
      expect(lists().s1).toEqual([WIRE])
    })
  })

  // PR-A2 review (attacker high, critic upheld): the step writes FIELD BY FIELD — each slave's `shownHostIds` — on the
  // local-profiles state it re-reads from storage right before computing; its undo puts a list back only where it is
  // still exactly what the step wrote. Another renderer's write is kept.
  describe('another renderer wrote meanwhile', () => {
    beforeEach(() => {
      runHostReresolve() // nothing else moves
      useShownHostsStore.setState({ ids: [LOCAL] })
      setLists({ on: [LOCAL], s1: [LOCAL] })
    })
    afterEach(() => { vi.restoreAllMocks() })

    /** What another renderer persisted: `mutate` over the stored local-profiles state, written to storage only. */
    function otherRendererWrites(mutate: (state: { slaves: Record<string, Record<string, unknown>>; [k: string]: unknown }) => void): void {
      const env = JSON.parse(localStorage.getItem(STORAGE_KEYS.LOCAL_PROFILES)!)
      mutate(env.state)
      localStorage.setItem(STORAGE_KEYS.LOCAL_PROFILES, JSON.stringify(env))
    }

    it('its write in storage (not memory) between plan and commit survives, and the re-key still lands', () => {
      const step = rekeyShownHosts(useHostStore.getState().hosts)
      if (step === null) throw new Error('nothing to move')
      const otherWorld = world({ z1: tab('z1', leaf('pz', tmux(UNKNOWN))) })
      otherRendererWrites((st) => {
        st.slaves.on.shownHostIds = ['d1_toggled'] // a show / hide in another workbench
        st.slaves.s1.name = 'Renamed there' // a record rebuilt elsewhere (a rename, a promote's relabel)
        st.slaves.s1.world = otherWorld // a world written there (a switch parks the screen)
      })
      expect(lists().on).toEqual([LOCAL]) // memory has not heard
      step.commit()
      const s1 = useLocalProfilesStore.getState().slaves.s1
      expect(lists().on).toEqual(['d1_toggled'])
      expect(s1.name).toBe('Renamed there')
      expect(s1.world).toEqual(otherWorld)
      expect(s1.shownHostIds).toEqual([WIRE])
      const stored = JSON.parse(localStorage.getItem(STORAGE_KEYS.LOCAL_PROFILES)!).state.slaves
      expect(stored.on.shownHostIds).toEqual(['d1_toggled'])
      expect(stored.s1).toMatchObject({ name: 'Renamed there', shownHostIds: [WIRE] })
    })

    it("the undo after a concurrent change to one slave's list: that slave keeps it, the others are put back", () => {
      const step = rekeyShownHosts(useHostStore.getState().hosts)
      if (step === null) throw new Error('nothing to move')
      step.commit()
      expect(lists()).toEqual({ on: [WIRE], s1: [WIRE] })
      useLocalProfilesStore.getState().setSlaveShownHosts('on', () => ['d1_concurrent'])
      useLocalProfilesStore.getState().setProfileAppearance('s1', { name: 'Renamed meanwhile' })
      step.undo()
      expect(lists()).toEqual({ on: ['d1_concurrent'], s1: [LOCAL] })
      expect(useLocalProfilesStore.getState().slaves.s1.name).toBe('Renamed meanwhile') // no other field touched
    })

    it("the master's list likewise: put back only while it is what the step wrote", () => {
      const step = rekeyShownHosts(useHostStore.getState().hosts)
      if (step === null) throw new Error('nothing to move')
      step.commit()
      useShownHostsStore.getState().show('d1_concurrent')
      step.undo()
      expect(useShownHostsStore.getState().ids).toEqual([WIRE, 'd1_concurrent'])
      expect(lists().s1).toEqual([LOCAL])
    })
  })

  // The critic's cross-device case: a list naming a host by its LOCAL id must reach the SOT as the d1_ id once the
  // workbench becomes the master — the other device only knows the host by its daemon.
  it('a slave list with a local id → daemonId learned → re-keyed to d1_ → promoted → the master list and the settings payload carry the d1_ id', async () => {
    useHostStore.setState({ hosts: { [LOCAL]: host(LOCAL) }, hostOrder: [LOCAL] }) // no daemonId yet
    runHostReresolve()
    setLists({ s1: [LOCAL] })
    expect(runHostReresolve()).toBe('done')
    expect(lists().s1).toEqual([LOCAL]) // nothing to move: its daemon is not known
    useHostStore.setState({ hosts: { [LOCAL]: host(LOCAL, { daemonId: DAEMON }) } })
    expect(runHostReresolve()).toBe('done')
    expect(lists().s1).toEqual([WIRE])
    const promoted = await promoteToMaster('s1', 'Old master')
    expect(promoted.ok).toBe(true)
    expect(useShownHostsStore.getState().ids).toEqual([WIRE])
    const payload = buildSectionPayload('settings')?.payload as Record<string, unknown> | undefined
    expect(payload?.['purdex-shown-hosts']).toEqual({ ids: [WIRE] })
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

  // PR #1406 critic: a rollback that fails too leaves the stores partly rewritten — memory put back, storage not.
  // Not claimed atomic: the pass recomputes its targets from what is there and is idempotent, so running it again
  // rolls the state FORWARD to the target (and a reload's hydration pass does the same).
  describe('the rollback fails too', () => {
    let failing = true
    /** Host settings never write; any other write carrying `WIRE` back — an undo — fails too. */
    function failRollbacks(): void {
      failing = true
      const real = Storage.prototype.setItem
      vi.spyOn(Storage.prototype, 'setItem').mockImplementation(function (this: Storage, k: string, v: string) {
        if (failing && k === STORAGE_KEYS.HOST_SETTINGS) throw new DOMException('quota', 'QuotaExceededError')
        if (failing && v.includes(WIRE)) throw new DOMException('quota', 'QuotaExceededError')
        real.call(this, k, v)
      })
    }

    /** Every store's memory is what its storage holds, and neither names `WIRE` any more. */
    function expectConverged(): void {
      for (const store of [useTabStore, useLocalProfilesStore, useHostSettingsStore, useNewTabLayoutStore] as const) {
        const { name, version, partialize } = store.persist.getOptions() as { name: string; version: number; partialize?: (st: unknown) => unknown }
        const mine = JSON.stringify({ state: partialize ? partialize(store.getState()) : store.getState(), version })
        expect(localStorage.getItem(name)).toBe(mine)
        expect(mine).not.toContain(WIRE)
      }
    }

    let runs = 0
    let unsubRuns: () => void = () => {}
    beforeEach(() => {
      runs = 0
      unsubRuns = useRebuildStore.subscribe((st, prev) => { if (st.lockedBy === HOST_RERESOLVE_LOCK_OWNER && prev.lockedBy !== HOST_RERESOLVE_LOCK_OWNER) runs++ })
      vi.spyOn(console, 'error').mockImplementation(() => {})
      vi.spyOn(console, 'warn').mockImplementation(() => {})
    })
    afterEach(() => { unsubRuns() })

    it('is its own outcome, rollback-failed — reported, never thrown', () => {
      failRollbacks()
      expect(runHostReresolve()).toBe('rollback-failed')
      expect(String(vi.mocked(console.error).mock.calls[0][0])).toMatch(/rollback incomplete/)
    })

    it('the pass is rescheduled at once — no backoff; a failure after that backs off as usual', () => {
      vi.useFakeTimers()
      failRollbacks()
      requestHostReresolve()
      expect(runs).toBe(1)
      vi.advanceTimersByTime(0)
      expect(runs).toBe(2) // at once
      vi.advanceTimersByTime(HOST_RERESOLVE_RETRY_MS - 1)
      expect(runs).toBe(2) // the second one failed as a plain write → backoff
      vi.advanceTimersByTime(1)
      expect(runs).toBe(3)
    })

    it('once writes work again, the rerun rolls forward: memory and storage agree, at the target', () => {
      vi.useFakeTimers()
      failRollbacks()
      requestHostReresolve()
      failing = false
      vi.advanceTimersByTime(0)
      expectConverged()
    })

    it('the identity is not marked handled: a host-store change before the rerun asks again', () => {
      vi.useFakeTimers() // the at-once rerun stays pending
      useHostStore.setState({ hosts: { other: host('other') }, hostOrder: ['other'] }) // X not here yet: nothing moves
      const stop = startHostReresolve() // the signature without X settles
      try {
        failRollbacks()
        useHostStore.setState((st) => ({ hosts: { ...st.hosts, [LOCAL]: host(LOCAL, { daemonId: DAEMON, order: 1 }) }, hostOrder: [...st.hostOrder, LOCAL] }))
        expect(runs).toBe(1) // that pass: rollback-failed
        failing = false
        useHostStore.setState((st) => ({ hosts: { ...st.hosts, [LOCAL]: { ...st.hosts[LOCAL], name: 'renamed' } } }))
        expect(runs).toBe(2)
        expectConverged()
      } finally {
        stop()
      }
    })

    it('a reload over the partly rewritten storage: the hydration pass converges', () => {
      failRollbacks()
      expect(runHostReresolve()).toBe('rollback-failed')
      failing = false
      __resetHostReresolveForTest()
      // restart: memory comes back from what storage holds — part rewritten, part not
      for (const store of [useTabStore, useWorkspaceStore, useLocalProfilesStore, useHostSettingsStore, useNewTabLayoutStore]) void store.persist.rehydrate()
      expect(JSON.stringify([useHostSettingsStore.getState().hosts, useLocalProfilesStore.getState().parkedMaster])).toContain(WIRE)
      const stop = startHostReresolve()
      try {
        expectConverged()
      } finally {
        stop()
      }
    })
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

  it('learning a daemonId moves the host\'s local-id look to its d1_ key (H2c-2)', () => {
    addHostX({ daemonId: undefined })
    useHostLookStore.setState({ looks: { [LOCAL]: { name: 'air26' } } })
    stop = startHostReresolve()
    expect(useHostLookStore.getState().looks).toEqual({ [LOCAL]: { name: 'air26' } })
    useHostStore.setState((s) => ({ hosts: { ...s.hosts, [LOCAL]: { ...s.hosts[LOCAL], daemonId: DAEMON } } }))
    expect(useHostLookStore.getState().looks).toEqual({ [WIRE]: { name: 'air26' } })
  })

  it('waits for the look store: nothing runs while purdex-host-looks has not hydrated, then it does (H2c-2)', () => {
    let finish: (() => void) | undefined
    const hydrated = vi.spyOn(useHostLookStore.persist, 'hasHydrated').mockReturnValue(false)
    vi.spyOn(useHostLookStore.persist, 'onFinishHydration').mockImplementation((cb) => {
      finish = () => cb(useHostLookStore.getState())
      return () => { finish = undefined }
    })
    addHostX()
    stop = startHostReresolve()
    expect(runs).toBe(0)
    hydrated.mockReturnValue(true)
    finish?.()
    expect(runs).toBe(1)
  })

  it('the look store hydrating AFTER the first pass with a local-id key ends on the d1_ key (H2c-2)', () => {
    let finish: (() => void) | undefined
    vi.spyOn(useHostLookStore.persist, 'onFinishHydration').mockImplementation((cb) => {
      finish = () => cb(useHostLookStore.getState())
      return () => { finish = undefined }
    })
    addHostX()
    stop = startHostReresolve()
    useHostLookStore.setState({ looks: { [LOCAL]: { name: 'late' } } }) // another window's write landed
    finish?.()
    expect(useHostLookStore.getState().looks).toEqual({ [WIRE]: { name: 'late' } })
  })

  // Codex critic review-mufd6v5g-2gh633: the pass that moves looks[localId] → looks[d1_] can be delayed after the
  // daemonId is learned. In that window the UI shows the local-id entry and an edit lands there; the pass then moves
  // it — the edit included — and nothing is lost.
  describe('a look edited before the delayed re-key is kept (H2c-2)', () => {
    const RED = { console: { main: { color: '#ef4444', alpha: 60 } } }
    const learn = () => useHostStore.setState((s) => ({ hosts: { ...s.hosts, [LOCAL]: { ...s.hosts[LOCAL], daemonId: DAEMON } } }))

    it('the operation lock held elsewhere: the UI keeps the local look, the edit lands in it, the pass moves it intact', () => {
      vi.useFakeTimers()
      addHostX({ daemonId: undefined })
      useHostLookStore.setState({ looks: { [LOCAL]: { name: 'L', colors: RED, icon: 'Cloud' } } })
      stop = startHostReresolve()
      const grant = useRebuildStore.getState().acquireOperationLock('someone-else')
      learn()
      expect(useHostLookStore.getState().looks).toEqual({ [LOCAL]: { name: 'L', colors: RED, icon: 'Cloud' } }) // busy
      expect(hostLookOf(LOCAL)).toEqual({ name: 'L', colors: RED, icon: 'Cloud' })
      useHostStore.getState().setHostColor(LOCAL, '#3b82f6')
      const edited = { name: 'L', colors: { console: { main: { color: '#3b82f6', alpha: 60 } } }, icon: 'Cloud' }
      expect(useHostLookStore.getState().looks).toEqual({ [LOCAL]: edited })
      useRebuildStore.getState().releaseOperationLock(grant)
      vi.advanceTimersByTime(HOST_RERESOLVE_RETRY_MS)
      expect(useHostLookStore.getState().looks).toEqual({ [WIRE]: edited })
      expect(hostLookOf(LOCAL)).toEqual(edited)
    })

    it('the look store not hydrated yet: same — the edit lands in the local entry and the pass moves it once hydrated', () => {
      let finish: (() => void) | undefined
      const hydrated = vi.spyOn(useHostLookStore.persist, 'hasHydrated').mockReturnValue(false)
      vi.spyOn(useHostLookStore.persist, 'onFinishHydration').mockImplementation((cb) => {
        finish = () => cb(useHostLookStore.getState())
        return () => { finish = undefined }
      })
      addHostX({ daemonId: undefined })
      useHostLookStore.setState({ looks: { [LOCAL]: { name: 'L', colors: RED } } })
      stop = startHostReresolve()
      learn()
      expect(runs).toBe(0)
      expect(hostLookOf(LOCAL)).toEqual({ name: 'L', colors: RED })
      useHostStore.getState().setHostIcon(LOCAL, 'Cloud')
      expect(useHostLookStore.getState().looks).toEqual({ [LOCAL]: { name: 'L', colors: RED, icon: 'Cloud' } })
      hydrated.mockReturnValue(true)
      finish?.()
      expect(useHostLookStore.getState().looks).toEqual({ [WIRE]: { name: 'L', colors: RED, icon: 'Cloud' } })
    })
  })

  it('learning a daemonId moves the host\'s listed local id to its d1_ id (H2d-1)', () => {
    addHostX({ daemonId: undefined })
    useShownHostsStore.setState({ ids: [LOCAL] })
    stop = startHostReresolve()
    expect(useShownHostsStore.getState().ids).toEqual([LOCAL])
    useHostStore.setState((s) => ({ hosts: { ...s.hosts, [LOCAL]: { ...s.hosts[LOCAL], daemonId: DAEMON } } }))
    expect(useShownHostsStore.getState().ids).toEqual([WIRE])
  })

  it('waits for the shown-hosts store: nothing runs while purdex-shown-hosts has not hydrated, then it does (H2d-1)', () => {
    let finish: (() => void) | undefined
    const hydrated = vi.spyOn(useShownHostsStore.persist, 'hasHydrated').mockReturnValue(false)
    vi.spyOn(useShownHostsStore.persist, 'onFinishHydration').mockImplementation((cb) => {
      finish = () => cb(useShownHostsStore.getState())
      return () => { finish = undefined }
    })
    addHostX()
    stop = startHostReresolve()
    expect(runs).toBe(0)
    hydrated.mockReturnValue(true)
    finish?.()
    expect(runs).toBe(1)
  })

  it('the shown-hosts store hydrating AFTER the first pass with a local id ends on the d1_ id (H2d-1)', () => {
    let finish: (() => void) | undefined
    vi.spyOn(useShownHostsStore.persist, 'onFinishHydration').mockImplementation((cb) => {
      finish = () => cb(useShownHostsStore.getState())
      return () => { finish = undefined }
    })
    addHostX()
    stop = startHostReresolve()
    useShownHostsStore.setState({ ids: [LOCAL] }) // another window's write landed
    finish?.()
    expect(useShownHostsStore.getState().ids).toEqual([WIRE])
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

// Plan H1c T1: the pass's core with an EXPLICIT map — the deletion direction (local id → wire id, host ownership spec
// §3.4). Same stores, same collision rules as the pass (plan §0.11), no lock, no re-read: the caller decides.
describe('rewriteHostRefs (explicit map — the deletion direction)', () => {
  /** Every store holds refs on `LOCAL` (on screen, parked master, parked slave), plus `UNKNOWN` ones. */
  function seedLocal(): void {
    useTabStore.setState({
      tabs: {
        t1: tab('t1', { type: 'split', id: 's', direction: 'h', sizes: [50, 50], children: [leaf('p1', tmux(LOCAL)), leaf('p2', tmux(UNKNOWN))] }),
        t2: tab('t2', leaf('p3', { kind: 'editor', source: { type: 'daemon', hostId: LOCAL }, filePath: '/a' })),
        t3: tab('t3', leaf('p4', { kind: 'execution', executionId: 'e', host: LOCAL })),
        t4: tab('t4', leaf('p5', { kind: 'execution', executionId: 'legacy', host: '' })),
      },
      tabOrder: ['t1', 't2', 't3', 't4'],
    })
    useLocalProfilesStore.setState({
      parkedMaster: world({ m1: tab('m1', leaf('pm', tmux(LOCAL))), m2: tab('m2', leaf('pm2', tmux(UNKNOWN))) }),
      slaves: {
        on: { id: 'on', name: 'On', createdAt: 1, shownHostIds: [], world: null },
        s1: { id: 's1', name: 'S', createdAt: 1, shownHostIds: [], world: world({ x1: tab('x1', leaf('px', { kind: 'execution', executionId: 'e', host: LOCAL })) }) },
      },
    })
    useHostSettingsStore.setState({ hosts: { [LOCAL]: { editor: { homePath: '/l' } }, [UNKNOWN]: { editor: { homePath: '/u' } } } })
    useNewTabLayoutStore.setState({
      presets: {
        '3col': { enabled: true, columns: [[`sessions:${LOCAL}`], [`headless:${LOCAL}`], [`sessions:${UNKNOWN}`]] },
        '2col': { enabled: false, columns: [['browser'], []] },
        '1col': { enabled: true, columns: [['browser', `sessions:${LOCAL}`, `headless:${UNKNOWN}`]] },
      },
      knownIds: ['browser', `sessions:${LOCAL}`, `headless:${LOCAL}`, `sessions:${UNKNOWN}`],
    })
  }

  beforeEach(seedLocal)

  it('moves every ref on the mapped id — on screen, parked master, parked slave — and nothing else', () => {
    expect(rewriteHostRefs({ [LOCAL]: WIRE })).toBe('ok')
    expect(hostIdsIn(useTabStore.getState().tabs).sort()).toEqual([WIRE, WIRE, WIRE, UNKNOWN, ''].sort())
    const { parkedMaster, slaves } = useLocalProfilesStore.getState()
    expect(hostIdsIn(parkedMaster).sort()).toEqual([WIRE, UNKNOWN].sort())
    expect(hostIdsIn(slaves.s1.world)).toEqual([WIRE])
  })

  it('moves no look: the look store is keyed by wire id and only the re-resolve pass re-keys it (decision 9, M9)', () => {
    // A deletion rewrites refs local → wire; the host's look entries must stay exactly where they are, under either
    // key — the look re-key belongs to `passBody`, never to `planRewrite`, which this explicit-map rewrite reuses.
    useHostLookStore.setState({ looks: { [LOCAL]: { name: 'local look', icon: 'Cloud' }, [UNKNOWN]: { name: 'other' } } })
    const looks = useHostLookStore.getState().looks
    const writes = vi.fn()
    const unsub = useHostLookStore.subscribe(writes)
    expect(rewriteHostRefs({ [LOCAL]: WIRE })).toBe('ok')
    unsub()
    expect(hostIdsIn(useTabStore.getState().tabs)).toContain(WIRE)
    expect(useHostLookStore.getState().looks).toBe(looks)
    expect(writes).not.toHaveBeenCalled()
  })

  it('moves no shown id: the shown-hosts store is keyed by wire id and only the re-resolve pass re-keys it (H2d-1 M7)', () => {
    // A deletion rewrites refs local → wire; a listed local id must stay exactly as it is — the shown-id re-key belongs
    // to `passBody`, never to `planRewrite`, which this explicit-map rewrite reuses.
    useShownHostsStore.setState({ ids: [LOCAL, UNKNOWN] })
    const before = useShownHostsStore.getState()
    const writes = vi.fn()
    const unsub = useShownHostsStore.subscribe(writes)
    expect(rewriteHostRefs({ [LOCAL]: WIRE })).toBe('ok')
    unsub()
    expect(hostIdsIn(useTabStore.getState().tabs)).toContain(WIRE)
    expect(useShownHostsStore.getState()).toBe(before)
    expect(writes).not.toHaveBeenCalled()
  })

  it('re-keys host settings and renames both column kinds in every preset and knownIds', () => {
    rewriteHostRefs({ [LOCAL]: WIRE })
    expect(useHostSettingsStore.getState().hosts).toEqual({ [WIRE]: { editor: { homePath: '/l' } }, [UNKNOWN]: { editor: { homePath: '/u' } } })
    const { presets, knownIds } = useNewTabLayoutStore.getState()
    expect(presets['3col'].columns).toEqual([[`sessions:${WIRE}`], [`headless:${WIRE}`], [`sessions:${UNKNOWN}`]])
    expect(presets['1col'].columns).toEqual([['browser', `sessions:${WIRE}`, `headless:${UNKNOWN}`]])
    expect(knownIds).toEqual(['browser', `sessions:${WIRE}`, `headless:${WIRE}`, `sessions:${UNKNOWN}`])
  })

  it('both forms of one host (plan §0.11): the sync-id settings entry wins; the wire-form column wins its place', () => {
    useHostSettingsStore.setState({ hosts: { [LOCAL]: { editor: { homePath: '/local' } }, [WIRE]: { editor: { homePath: '/wire' } } } })
    useNewTabLayoutStore.setState({
      presets: {
        '3col': { enabled: true, columns: [[`sessions:${LOCAL}`], [`sessions:${WIRE}`], []] },
        '2col': { enabled: false, columns: [[`headless:${WIRE}`, `headless:${LOCAL}`], []] },
        '1col': { enabled: true, columns: [['browser']] },
      },
      knownIds: [`sessions:${LOCAL}`, `sessions:${WIRE}`],
    })
    rewriteHostRefs({ [LOCAL]: WIRE })
    expect(useHostSettingsStore.getState().hosts).toEqual({ [WIRE]: { editor: { homePath: '/wire' } } })
    const { presets, knownIds } = useNewTabLayoutStore.getState()
    expect(presets['3col'].columns).toEqual([[], [`sessions:${WIRE}`], []])
    expect(presets['2col'].columns).toEqual([[`headless:${WIRE}`], []])
    expect(knownIds).toEqual([`sessions:${WIRE}`])
  })

  it('takes no lock and holds none (the deletion is lock-free — plan §0.1)', () => {
    const seen: (string | null)[] = []
    const unsub = useRebuildStore.subscribe((st) => { seen.push(st.lockedBy) })
    rewriteHostRefs({ [LOCAL]: WIRE })
    unsub()
    expect(seen).toEqual([])
  })

  it('works while somebody else holds the operation lock', () => {
    const grant = useRebuildStore.getState().acquireOperationLock('someone-else')
    expect(rewriteHostRefs({ [LOCAL]: WIRE })).toBe('ok')
    expect(hostIdsIn(useTabStore.getState().tabs)).not.toContain(LOCAL)
    useRebuildStore.getState().releaseOperationLock(grant)
  })

  it('a map that moves nothing writes nothing: every store keeps its object', () => {
    const before = snapshot()
    expect(rewriteHostRefs({ [LOCAL]: LOCAL, other: WIRE })).toBe('ok')
    const after = snapshot()
    expect(after.tabs).toBe(before.tabs)
    expect(after.profiles).toBe(before.profiles)
    expect(after.hostSettings).toBe(before.hostSettings)
    expect(after.newtab).toBe(before.newtab)
  })

  it('a key inherited from Object.prototype is not a mapping', () => {
    useTabStore.setState({ tabs: { c: tab('c', leaf('pc', tmux('constructor'))) }, tabOrder: ['c'] })
    const before = useTabStore.getState().tabs
    rewriteHostRefs({ [LOCAL]: WIRE })
    expect(useTabStore.getState().tabs.c).toBe(before.c)
  })

  it('a store write that throws is put back and reported — never thrown', () => {
    vi.spyOn(console, 'warn').mockImplementation(() => {})
    const real = Storage.prototype.setItem
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(function (this: Storage, k: string, v: string) {
      if (k === STORAGE_KEYS.HOST_SETTINGS) throw new DOMException('quota', 'QuotaExceededError')
      real.call(this, k, v)
    })
    const before = snapshot()
    try {
      expect(rewriteHostRefs({ [LOCAL]: WIRE })).toBe('write-failed')
    } finally {
      vi.restoreAllMocks()
    }
    expect(useTabStore.getState().tabs).toBe(before.tabs)
    expect(useLocalProfilesStore.getState().parkedMaster).toBe(before.profiles.parkedMaster)
    expect(useHostSettingsStore.getState().hosts).toBe(before.hostSettings)
  })
})

// Plan H1c T3 (§0.6): the undo's re-resolve — the pass body, for the ONE host that came back.
describe('reresolveRestoredHost', () => {
  const Y_DAEMON = 'why-lab:yyyyyy'
  const WIRE_Y = syncIdOfSync(Y_DAEMON)

  beforeEach(() => {
    useHostStore.setState({ hosts: { [LOCAL]: host(LOCAL, { daemonId: DAEMON }), yloc: host('yloc', { daemonId: Y_DAEMON, order: 1 }) }, hostOrder: [LOCAL, 'yloc'] })
    useTabStore.setState({ tabs: { ...useTabStore.getState().tabs, y: tab('y', leaf('py', tmux(WIRE_Y))) } })
  })

  it('moves the references onto that host only — another host\'s wire id is the plain pass\'s', () => {
    expect(reresolveRestoredHost(LOCAL)).toBe('done')
    const ids = hostIdsIn(useTabStore.getState().tabs)
    expect(ids).not.toContain(WIRE)
    expect(ids).toContain(WIRE_Y)
  })

  it('runs inside the caller\'s grant (nested), leaving the lock to its holder', () => {
    const grant = useRebuildStore.getState().acquireOperationLock('profile-sync')
    expect(reresolveRestoredHost(LOCAL, grant)).toBe('done')
    expect(hostIdsIn(useTabStore.getState().tabs)).not.toContain(WIRE)
    expect(useRebuildStore.getState().lockGrant).toBe(grant)
    useRebuildStore.getState().releaseOperationLock(grant)
  })

  it('without the grant, the lock held elsewhere: busy, nothing written', () => {
    const grant = useRebuildStore.getState().acquireOperationLock('profile-sync')
    const tabs = useTabStore.getState().tabs
    expect(reresolveRestoredHost(LOCAL)).toBe('busy')
    expect(useTabStore.getState().tabs).toBe(tabs)
    useRebuildStore.getState().releaseOperationLock(grant)
  })
})

