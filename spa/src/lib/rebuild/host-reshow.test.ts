// spa/src/lib/rebuild/host-reshow.test.ts — showing a host again recovers its sessions (host ownership H2d-4, §0.21
// step 3): on each hidden → shown transition, the EXISTING `recoverHostSessions` once per daemon, from a store
// subscriber — so a show that arrives by a `settings` apply recovers too.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useHostStore, type HostConfig } from '../../stores/useHostStore'
import { useShownHostsStore } from '../../stores/useShownHostsStore'
import { useTabStore } from '../../stores/useTabStore'
import { useWorkspaceStore } from '../../features/workspace/store'
import { MASTER_PROFILE_ID, useLocalProfilesStore, type ParkedWorld } from '../../stores/useLocalProfilesStore'
import { useRebuildStore } from '../../stores/useRebuildStore'
import { useSessionStore } from '../../stores/useSessionStore'
import { STORAGE_KEYS } from '../storage/keys'
import { syncIdOfSync } from '../profile/host-identity'
import { setHostShown } from '../shown-hosts'
import type { FreshSessions, Session } from '../host-api'
import type { Tab, TmuxSessionContent } from '../../types/tab'
import { openAttachGate } from './attach-gate'
import { __resetForTests as __resetSessionVersion, note } from './session-version'
import { noteReconciledSessions } from './revive'

vi.mock('./cwd-probe', () => ({ probeMissingCwds: vi.fn(), probeSessionCwd: vi.fn(), resetCwdProbes: vi.fn() }))
vi.mock('./provenance-probe', () => ({ probeSessionProvenance: vi.fn(), resetProvenanceProbes: vi.fn() }))

const { listSessionsFresh } = vi.hoisted(() => ({ listSessionsFresh: vi.fn<(hostId: string) => Promise<FreshSessions>>() }))
vi.mock('../host-api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../host-api')>()),
  listSessionsFresh: (hostId: string) => listSessionsFresh(hostId),
}))

// `recoverHostSessions` is spied but stays the REAL function underneath: the unit cases stub it, the integration
// cases let it run (refresh → reconcile → revive).
const real = vi.hoisted(() => ({ recover: null as null | ((hostId: string) => Promise<void>) }))
vi.mock('./refresh-sessions', async (importOriginal) => {
  const mod = await importOriginal<typeof import('./refresh-sessions')>()
  real.recover = mod.recoverHostSessions
  return { ...mod, recoverHostSessions: vi.fn(mod.recoverHostSessions) }
})

const { recoverHostSessions, __resetRefreshForTests } = await import('./refresh-sessions')
const { startHostReshowRecovery, __resetHostReshowForTest } = await import('./host-reshow')
const { createOperationLockObserver } = await import('../../hooks/useMultiHostEventWs')
const recover = vi.mocked(recoverHostSessions)

const DAEMON_X = 'air-lab:26aaaa'
const X_WIRE = syncIdOfSync(DAEMON_X)
const X = 'hx' // has DAEMON_X
const X2 = 'hx2' // a second local row claiming DAEMON_X (identity conflict pair)
const M = 'hm' // no daemonId
const Y = 'hy' // another daemon
const Y_WIRE = syncIdOfSync('mini-lab:27bbbb')
const EPOCH = '9f3c1a0b7d2e4c61'

const host = (id: string, over: Partial<HostConfig> = {}): HostConfig => ({ id, name: id, ip: '10.0.0.1', port: 7860, token: 'tok', order: 0, ...over })
const calls = () => recover.mock.calls.map(([id]) => id)

let stops: Array<() => void> = []
const start = () => { stops.push(startHostReshowRecovery()) }

beforeEach(() => {
  localStorage.clear()
  __resetHostReshowForTest()
  __resetRefreshForTests()
  __resetSessionVersion()
  listSessionsFresh.mockReset()
  recover.mockReset().mockImplementation(async () => {})
  useHostStore.setState({
    hosts: { [X]: host(X, { daemonId: DAEMON_X }), [M]: host(M, { order: 1 }), [Y]: host(Y, { daemonId: 'mini-lab:27bbbb', order: 2 }) },
    hostOrder: [X, M, Y],
    activeHostId: M,
    runtime: {},
  })
  useShownHostsStore.setState({ ids: [M], relabelStamp: 0 })
  useRebuildStore.setState({ operations: {}, lockedBy: null, lockGrant: null })
  masterSettled()
})

const EMPTY: ParkedWorld = { workspaces: [], tabs: {}, activeWorkspaceId: null, activeTabId: null }
const S = 'slave1'

/** The master on screen, settled; `S` parked with `list`. */
function masterSettled(list: string[] = []): void {
  useLocalProfilesStore.setState({ slaves: { [S]: { id: S, name: 'S', createdAt: 1, shownHostIds: list, world: EMPTY } }, slaveOrder: [S], activeProfileId: MASTER_PROFILE_ID, parkedMaster: null, worldEpoch: 0, relabelCount: 0 })
  useTabStore.setState({ worldId: MASTER_PROFILE_ID, worldEpoch: 0 })
  useWorkspaceStore.setState({ worldId: MASTER_PROFILE_ID, worldEpoch: 0 })
}

afterEach(() => {
  for (const stop of stops) stop()
  stops = []
  vi.restoreAllMocks()
  __resetHostReshowForTest()
  __resetRefreshForTests()
  useHostStore.getState().reset()
  useLocalProfilesStore.setState({ slaves: {}, slaveOrder: [], activeProfileId: MASTER_PROFILE_ID, parkedMaster: null, worldEpoch: 0, relabelCount: 0 })
  useTabStore.setState({ worldId: MASTER_PROFILE_ID, worldEpoch: 0 })
  useWorkspaceStore.setState({ worldId: MASTER_PROFILE_ID, worldEpoch: 0 })
})

describe('startHostReshowRecovery — one recovery per hidden → shown transition', () => {
  it('showing X calls recoverHostSessions once with X\'s local id; the same write again calls nothing', () => {
    start()
    setHostShown(X, true)
    expect(calls()).toEqual([X])
    useShownHostsStore.getState().show(X_WIRE)
    useShownHostsStore.setState({ ids: [...useShownHostsStore.getState().ids] }) // a new array, same answer
    expect(calls()).toEqual([X])
  })

  it('shown → hidden calls nothing', () => {
    useShownHostsStore.setState({ ids: [M, X_WIRE] })
    start()
    setHostShown(X, false)
    expect(recover).not.toHaveBeenCalled()
  })

  it('a show that arrives by a settings apply (storage written, store rehydrated) recovers once', async () => {
    start()
    localStorage.setItem(STORAGE_KEYS.SHOWN_HOSTS, JSON.stringify({ state: { ids: [M, X_WIRE] }, version: 1 }))
    await useShownHostsStore.persist.rehydrate()
    expect(useShownHostsStore.getState().ids).toEqual([M, X_WIRE])
    expect(calls()).toEqual([X])
  })

  it('a host added (hidden) calls nothing; a host added whose wire id was already listed is no transition either', () => {
    useShownHostsStore.setState({ ids: [M, Y_WIRE] })
    useHostStore.setState((s) => ({ hosts: { [X]: s.hosts[X], [M]: s.hosts[M] }, hostOrder: [X, M] })) // Y not here yet
    start()
    useHostStore.getState().addHost({ name: 'new', ip: '10.0.0.9', port: 7860 })
    useHostStore.setState((s) => ({ hosts: { ...s.hosts, [Y]: host(Y, { daemonId: 'mini-lab:27bbbb', order: 2 }) }, hostOrder: [...s.hostOrder, Y] }))
    expect(recover).not.toHaveBeenCalled()
  })

  it('a daemonId learned for a host shown under its local id calls nothing', () => {
    useShownHostsStore.setState({ ids: [M] })
    start()
    useHostStore.setState((s) => ({ hosts: { ...s.hosts, [M]: { ...s.hosts[M], daemonId: 'mlab:99cccc' } } }))
    useShownHostsStore.getState().rekey([[M, syncIdOfSync('mlab:99cccc')]])
    expect(recover).not.toHaveBeenCalled()
  })

  it('the boot hydration of a stored list holding X calls nothing (the baseline is taken after hydration)', () => {
    let finish: (() => void) | undefined
    const hydrated = vi.spyOn(useShownHostsStore.persist, 'hasHydrated').mockReturnValue(false)
    vi.spyOn(useShownHostsStore.persist, 'onFinishHydration').mockImplementation((cb) => {
      finish = () => cb(useShownHostsStore.getState())
      return () => { finish = undefined }
    })
    start()
    useShownHostsStore.setState({ ids: [M, X_WIRE] }) // the stored list lands
    hydrated.mockReturnValue(true)
    finish?.()
    expect(recover).not.toHaveBeenCalled()
    setHostShown(X, false)
    setHostShown(X, true)
    expect(calls()).toEqual([X]) // later transitions count
  })

  // Reconcile and revive filter panes by the LOCAL host id they are given, so each local row of a daemon two rows
  // claim (an identity conflict) needs its own call — one call for the daemon would leave the other row's panes on
  // Rebuild (H2d-4b attacker review).
  it('two local rows of one daemon shown by one write → one call per row; hidden and shown again → one more each', () => {
    useHostStore.setState((s) => ({
      hosts: { ...s.hosts, [X2]: host(X2, { daemonId: DAEMON_X, order: 3 }) },
      hostOrder: [M, X2, X, Y],
    }))
    start()
    useShownHostsStore.getState().show(X_WIRE)
    expect(calls().sort()).toEqual([X, X2].sort())
    useShownHostsStore.getState().hide(X_WIRE)
    useShownHostsStore.getState().show(X_WIRE)
    expect(calls().sort()).toEqual([X, X, X2, X2].sort())
  })

  it('two different daemons shown by one write → one call each', () => {
    start()
    useShownHostsStore.setState({ ids: [M, X_WIRE, Y_WIRE] })
    expect(calls().sort()).toEqual([X, Y].sort())
  })

  it('installing twice still gives one call per transition', () => {
    start()
    start()
    setHostShown(X, true)
    expect(calls()).toEqual([X])
  })

  it('never writes the tab store', () => {
    const tabs = useTabStore.getState().tabs
    start()
    setHostShown(X, true)
    setHostShown(X, false)
    expect(useTabStore.getState().tabs).toBe(tabs)
  })
})

// Per-workbench shown hosts (2026-09-25 plan, A6): the list judged is the CURRENT one — the workbench on screen's.
describe('startHostReshowRecovery — per workbench (A6)', () => {
  /** `S` on screen, in the order a cross-window switch may arrive in: the pointer, then the tags. */
  function switchToSlave(): void {
    useLocalProfilesStore.setState({ slaves: { [S]: { ...useLocalProfilesStore.getState().slaves[S], world: null } }, activeProfileId: S, parkedMaster: EMPTY, worldEpoch: 1 })
    useTabStore.setState({ worldId: S, worldEpoch: 1 })
    useWorkspaceStore.setState({ worldId: S, worldEpoch: 1 })
  }

  it("a world switch that turns X from hidden to shown recovers X — once, although the stores arrive one by one", () => {
    masterSettled([M, X_WIRE])
    start()
    switchToSlave()
    expect(calls()).toEqual([X])
  })

  it('a host shown in both workbenches: a switch is no transition (the unsettled moments in between do not count)', () => {
    useShownHostsStore.setState({ ids: [M, X_WIRE] })
    masterSettled([M, X_WIRE])
    start()
    switchToSlave()
    expect(recover).not.toHaveBeenCalled()
  })

  it('shown → hidden by a switch calls nothing; two local rows of one daemon get one call each', () => {
    useHostStore.setState((s) => ({ hosts: { ...s.hosts, [X2]: host(X2, { daemonId: DAEMON_X, order: 3 }) }, hostOrder: [M, X2, X, Y] }))
    useShownHostsStore.setState({ ids: [M, X_WIRE] })
    masterSettled([M])
    start()
    switchToSlave()
    expect(recover).not.toHaveBeenCalled()
    useLocalProfilesStore.getState().setSlaveShownHosts(S, (ids) => [...ids, X_WIRE])
    expect(calls().sort()).toEqual([X, X2].sort())
  })

  it("showing X in the slave on screen (its own list) recovers X; the master's store is untouched", () => {
    masterSettled([M])
    switchToSlave()
    start()
    const master = useShownHostsStore.getState()
    expect(setHostShown(X, true)).toBe(true)
    expect(calls()).toEqual([X])
    expect(useShownHostsStore.getState()).toBe(master)
  })

  it('a write to a PARKED slave\'s list is no transition on screen', () => {
    start()
    useLocalProfilesStore.getState().setSlaveShownHosts(S, () => [M, X_WIRE])
    expect(recover).not.toHaveBeenCalled()
  })

  // The gate: the baseline is taken once FOUR stores have hydrated — the host store (identity resolution: which local
  // row a d1_ id means), the shown store, the local profiles and the tab store (whose list, and is it settled). Before
  // hydration each holds a stale value; every order of their arrival must add no call; later transitions count.
  describe('the hydration gate — every order of the four stores', () => {
    type Gated = { hasHydrated: () => boolean; onFinishHydration: (cb: (s: never) => void) => () => void }
    const GATED = { host: useHostStore, shown: useShownHostsStore, local: useLocalProfilesStore, tab: useTabStore } as const
    type Key = keyof typeof GATED
    const permutations = (keys: Key[]): Key[][] => (keys.length <= 1 ? [keys] : keys.flatMap((k) => permutations(keys.filter((x) => x !== k)).map((rest) => [k, ...rest])))

    /** What each store holds before (stale) and after (stored) its hydration. */
    const land: Record<Key, () => void> = {
      host: () => useHostStore.setState((s) => ({ hosts: { ...s.hosts, [X]: host(X, { daemonId: DAEMON_X }) } })), // X's daemon learned from storage
      shown: () => useShownHostsStore.setState({ ids: [M, X_WIRE] }),
      local: () => useLocalProfilesStore.setState({ relabelCount: 0 }),
      tab: () => useTabStore.setState({ worldId: MASTER_PROFILE_ID, worldEpoch: 0 }),
    }
    const stale = () => {
      useHostStore.setState((s) => ({ hosts: { ...s.hosts, [X]: host(X) } })) // no daemonId in memory yet
      useShownHostsStore.setState({ ids: [M] })
    }

    it.each(permutations(['host', 'shown', 'local', 'tab']).map((order) => [order.join(' → '), order] as const))('%s: no call; a later show counts', (_label, order) => {
      const hydrated: Record<Key, boolean> = { host: false, shown: false, local: false, tab: false }
      const finish: Partial<Record<Key, () => void>> = {}
      for (const key of Object.keys(GATED) as Key[]) {
        const persist = (GATED[key] as unknown as { persist: Gated }).persist
        vi.spyOn(persist, 'hasHydrated').mockImplementation(() => hydrated[key])
        vi.spyOn(persist, 'onFinishHydration').mockImplementation((cb) => {
          finish[key] = () => cb(undefined as never)
          return () => { delete finish[key] }
        })
      }
      stale()
      start()
      for (const key of order) {
        land[key]()
        hydrated[key] = true
        finish[key]?.()
      }
      expect(recover).not.toHaveBeenCalled()
      setHostShown(X, false)
      setHostShown(X, true)
      expect(calls()).toEqual([X])
    })
  })
})

describe('startHostReshowRecovery — integration with the real refresh (no sessions frame needed)', () => {
  const LATE: Session = { code: 'late01', name: 'late', cwd: '', mode: 'terminal', tmux_instance: '222:2000' }
  const dead: TmuxSessionContent = {
    kind: 'tmux-session', hostId: X, sessionCode: 'dead01', mode: 'terminal', cachedName: 'late', tmuxInstance: '111:1000',
    terminated: 'tmux-restarted',
  }
  const onScreen = (): TmuxSessionContent => {
    const l = useTabStore.getState().tabs.t1.layout
    if (l.type !== 'leaf' || l.pane.content.kind !== 'tmux-session') throw new Error('fixture')
    return l.pane.content
  }
  let unsubscribeLock: () => void = () => {}

  beforeEach(() => {
    recover.mockImplementation((hostId) => real.recover!(hostId))
    const tab: Tab = { id: 't1', pinned: false, locked: false, createdAt: 1, layout: { type: 'leaf', pane: { id: 'p1', content: dead } } }
    useTabStore.setState({ tabs: { t1: tab }, tabOrder: ['t1'], activeTabId: 't1', visitHistory: [] })
    useSessionStore.setState({ sessions: {} })
    openAttachGate(X)
    noteReconciledSessions(X, []) // the last list X reconciled (while hidden) has no `late`
    listSessionsFresh.mockResolvedValue({ kind: 'versioned', epoch: EPOCH, seq: 2, sessions: [LATE] })
    unsubscribeLock = useRebuildStore.subscribe(createOperationLockObserver())
  })

  afterEach(() => { unsubscribeLock() })

  it('a pane marked terminated while X was hidden, its session alive → after show it is repointed live', async () => {
    note(X, { epoch: EPOCH, seq: 1 }) // the live connection reconciled a versioned list
    start()
    setHostShown(X, true)
    await vi.waitFor(() => expect(onScreen()).toMatchObject({ sessionCode: 'late01', tmuxInstance: '222:2000' }))
    expect(onScreen().terminated).toBeUndefined()
    expect(listSessionsFresh).toHaveBeenCalledTimes(1)
    expect(listSessionsFresh).toHaveBeenCalledWith(X)
  })

  it('a conflict pair whose FIRST row in hostOrder is not the pane\'s host → the pane on the second row still revives', async () => {
    useHostStore.setState((s) => ({
      hosts: { ...s.hosts, [X2]: host(X2, { daemonId: DAEMON_X, order: 3 }) },
      hostOrder: [M, X2, X, Y],
    }))
    openAttachGate(X2)
    noteReconciledSessions(X2, [])
    note(X, { epoch: EPOCH, seq: 1 })
    note(X2, { epoch: EPOCH, seq: 1 })
    start()
    useShownHostsStore.getState().show(X_WIRE)
    await vi.waitFor(() => expect(onScreen()).toMatchObject({ sessionCode: 'late01', tmuxInstance: '222:2000' }))
    expect(listSessionsFresh).toHaveBeenCalledWith(X)
  })

  // X is not "versioned & live" here (an old daemon's list, say): the release alone would only re-run the revive pass
  // over the list last reconciled, which lacks `late` — only the deferred recovery fetches a fresh one.
  it('with the operation lock held the recovery defers to its release', async () => {
    const grant = useRebuildStore.getState().acquireOperationLock('test-op')
    start()
    setHostShown(X, true)
    await Promise.resolve()
    expect(listSessionsFresh).not.toHaveBeenCalled()
    expect(onScreen().terminated).toBe('tmux-restarted')
    useRebuildStore.getState().releaseOperationLock(grant)
    await vi.waitFor(() => expect(onScreen()).toMatchObject({ sessionCode: 'late01' }))
    expect(listSessionsFresh).toHaveBeenCalledTimes(1)
  })
})
