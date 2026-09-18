import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createSession, listSessions } from '../host-api'
import type { Session } from '../host-api'
import type { PaneContent, Tab } from '../../types/tab'
import type { WorkspaceSnapshot } from '../snapshot/types'
import { RestoreError } from '../snapshot/types'
import { SNAPSHOT_PREV_KEY, readPrevSnapshot, writePrevSnapshot } from '../snapshot/storage'
import { browserStorage } from '../storage/browser-backend'
import { useTabStore } from '../../stores/useTabStore'
import { useWorkspaceStore } from '../../features/workspace/store'
import { useSessionStore } from '../../stores/useSessionStore'
import { useHostStore } from '../../stores/useHostStore'
import { useRebuildStore } from '../../stores/useRebuildStore'
import { undoLastRestore } from '../snapshot/restore'
import type { DeviceStateMergeReport, DeviceStateRestoreReport } from './restore'
import { DEVICE_STATE_LOCK_OWNER, restoreDeviceStateMerge, restoreDeviceStateReplace } from './restore'

vi.mock('../host-api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../host-api')>()),
  listSessions: vi.fn(),
  createSession: vi.fn(),
}))

function tmuxTab(id: string, hostId: string, sessionCode: string, name: string): Tab {
  return {
    id,
    pinned: false,
    locked: false,
    createdAt: 0,
    layout: {
      type: 'leaf',
      pane: {
        id: `pane-${id}`,
        content: { kind: 'tmux-session', hostId, sessionCode, mode: 'terminal', cachedName: name, tmuxInstance: 'old-inst' },
      },
    },
  }
}

function paneOf(tab: Tab): PaneContent {
  return (tab.layout as { pane: { content: PaneContent } }).pane.content
}

function session(overrides: Partial<Session> & { code: string }): Session {
  return {
    name: overrides.code,
    cwd: '/tmp',
    mode: 'terminal',
    tmux_instance: 'new-inst',
    ...overrides,
  }
}

/** The world currently on this computer (before replace). */
function localWorld(): WorkspaceSnapshot {
  return {
    version: 1,
    capturedAt: 1,
    tabs: { L1: tmuxTab('L1', 'h1', 'lc1', 'local') },
    tabOrder: ['L1'],
    activeTabId: 'L1',
    workspaces: [{ id: 'wsL', name: 'local ws', tabs: ['L1'], activeTabId: 'L1' }],
    activeWorkspaceId: 'wsL',
    sessionMeta: {
      h1: { lc1: { hostId: 'h1', sessionCode: 'lc1', name: 'local', mode: 'terminal', cwd: '/l', restorable: true } },
    },
  }
}

/** Incoming device-state payload from another computer. */
function incoming(): WorkspaceSnapshot {
  return {
    version: 1,
    capturedAt: 9,
    tabs: {
      A: tmuxTab('A', 'h1', 'oldA', 'alpha'), // same name live under a different code
      B: tmuxTab('B', 'h1', 'oldB', 'beta'), // no live match
      C: tmuxTab('C', 'gone', 'oldC', 'gamma'), // unknown host
    },
    tabOrder: ['A', 'B', 'C'],
    activeTabId: 'A',
    workspaces: [{ id: 'wsR', name: 'remote ws', tabs: ['A', 'B'], activeTabId: 'B' }],
    activeWorkspaceId: 'wsR',
    sessionMeta: {
      h1: {
        oldA: { hostId: 'h1', sessionCode: 'oldA', name: 'alpha', mode: 'terminal', cwd: '/a', restorable: true },
        oldB: { hostId: 'h1', sessionCode: 'oldB', name: 'beta', mode: 'terminal', cwd: '/b', restorable: true },
      },
      gone: {
        oldC: { hostId: 'gone', sessionCode: 'oldC', name: 'gamma', mode: 'terminal', cwd: '/c', restorable: true },
      },
    },
  }
}

const seedLocal = (): void => {
  const w = localWorld()
  useTabStore.setState({ tabs: w.tabs, tabOrder: w.tabOrder, activeTabId: w.activeTabId, visitHistory: ['L1'] })
  useWorkspaceStore.setState({ workspaces: w.workspaces, activeWorkspaceId: w.activeWorkspaceId })
}

const resetStores = (): void => {
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
  useWorkspaceStore.setState({ workspaces: [], activeWorkspaceId: null })
  useSessionStore.setState({ sessions: {}, activeHostId: null, activeCode: null })
  useRebuildStore.setState({ operations: {}, lockedBy: null, lockGrant: null })
  useHostStore.setState({ hosts: {}, hostOrder: [] })
}

const storeView = () => ({
  tab: {
    tabs: useTabStore.getState().tabs,
    tabOrder: useTabStore.getState().tabOrder,
    activeTabId: useTabStore.getState().activeTabId,
  },
  ws: {
    workspaces: useWorkspaceStore.getState().workspaces,
    activeWorkspaceId: useWorkspaceStore.getState().activeWorkspaceId,
  },
})

const seededPrev = (): WorkspaceSnapshot => ({ ...localWorld(), capturedAt: 777 })

/**
 * A buildSnapshot stand-in that, like the real one, reads the stores
 * synchronously at its start and then awaits; `onRead(n)` runs after the read
 * on the n-th call, simulating the user editing the world during the await.
 */
const storeBuild = (onRead?: (call: number) => void) => {
  let call = 0
  return vi.fn(async (now: number): Promise<WorkspaceSnapshot> => {
    const { tabs, tabOrder, activeTabId } = useTabStore.getState()
    const { workspaces, activeWorkspaceId } = useWorkspaceStore.getState()
    onRead?.(++call)
    await Promise.resolve()
    return { version: 1, capturedAt: now, tabs, tabOrder, activeTabId, workspaces, activeWorkspaceId, sessionMeta: {} }
  })
}

/** Spy on storage writes; returns a getter for how many hit the `-prev` key. */
const countPrevWrites = (): (() => number) => {
  const spy = vi.spyOn(browserStorage, 'setItem')
  return () => spy.mock.calls.filter(([key]) => key === SNAPSHOT_PREV_KEY).length
}

const openTab = (id: string): Tab => {
  const tab = tmuxTab(id, 'h1', `code-${id}`, id)
  useTabStore.setState({
    tabs: { ...useTabStore.getState().tabs, [id]: tab },
    tabOrder: [...useTabStore.getState().tabOrder, id],
  })
  return tab
}

describe('restoreDeviceStateReplace', () => {
  beforeEach(() => {
    localStorage.clear()
    vi.mocked(listSessions).mockReset()
    vi.mocked(createSession).mockReset()
    resetStores()
    useHostStore.setState({
      hosts: { h1: { id: 'h1', name: 'h1', ip: '127.0.0.1', port: 7860, order: 0 } },
      hostOrder: ['h1'],
    })
    seedLocal()
    writePrevSnapshot(seededPrev())
  })

  afterEach(() => {
    expect(createSession).not.toHaveBeenCalled()
    vi.restoreAllMocks()
    resetStores()
    localStorage.clear()
  })

  it('exposes the lock owner names', () => {
    expect(DEVICE_STATE_LOCK_OWNER).toEqual({
      replace: 'snapshot:deviceStateReplace',
      merge: 'snapshot:deviceStateMerge',
    })
  })

  it('refuses while another owner holds the lock; stores and -prev unchanged', async () => {
    const before = storeView()
    useRebuildStore.getState().acquireOperationLock('rebuild:p1')

    await expect(restoreDeviceStateReplace(incoming(), { now: 1, buildSnapshotFn: async () => localWorld() }))
      .rejects.toThrow('snapshot:deviceStateReplace refused: another operation is already running (rebuild:p1)')

    expect(storeView()).toEqual(before)
    expect(readPrevSnapshot()).toEqual(seededPrev())
    expect(listSessions).not.toHaveBeenCalled()
    expect(useRebuildStore.getState().lockedBy).toBe('rebuild:p1')
  })

  const missingSessionMeta = (): unknown => {
    const { sessionMeta: _omit, ...rest } = incoming()
    void _omit
    return rest
  }

  it.each([
    ['null', () => null],
    ['missing sessionMeta', missingSessionMeta],
    ['version 2', () => ({ ...incoming(), version: 2 })],
  ])('malformed payload (%s) → throws; stores, -prev untouched; listSessions never called', async (_n, make) => {
    const before = storeView()
    const build = vi.fn(async () => localWorld())

    await expect(restoreDeviceStateReplace(make(), { now: 1, buildSnapshotFn: build }))
      .rejects.toThrow('malformed device state payload')

    expect(storeView()).toEqual(before)
    expect(readPrevSnapshot()).toEqual(seededPrev())
    expect(listSessions).not.toHaveBeenCalled()
    expect(build).not.toHaveBeenCalled()
    expect(useRebuildStore.getState().lockedBy).toBeNull()
  })

  it('happy path: host-removed, re-pointed, terminated; stores replaced; session store synced; lock released', async () => {
    vi.mocked(listSessions).mockResolvedValue([session({ code: 'newA', name: 'alpha' })])

    const report: DeviceStateRestoreReport = await restoreDeviceStateReplace(incoming(), {
      now: 5,
      buildSnapshotFn: async () => localWorld(),
    })

    expect(report).toEqual({ reattached: 1, rebuilt: 0, failed: 1, hostRemoved: 1, rebuiltButUnattached: [] })
    expect(listSessions).toHaveBeenCalledTimes(1)
    expect(listSessions).toHaveBeenCalledWith('h1')

    const { tabs, tabOrder, activeTabId } = useTabStore.getState()
    expect(Object.keys(tabs).sort()).toEqual(['A', 'B', 'C'])
    expect(tabOrder).toEqual(['A', 'B', 'C'])
    expect(activeTabId).toBe('A')

    const a = paneOf(tabs.A)
    expect(a.kind === 'tmux-session' && a.sessionCode).toBe('newA')
    expect(a.kind === 'tmux-session' && a.tmuxInstance).toBe('new-inst')
    expect(a.kind === 'tmux-session' && a.terminated).toBeUndefined()

    const b = paneOf(tabs.B)
    expect(b.kind === 'tmux-session' && b.sessionCode).toBe('oldB')
    expect(b.kind === 'tmux-session' && b.terminated).toBe('tmux-restarted')

    const c = paneOf(tabs.C)
    expect(c.kind === 'tmux-session' && c.terminated).toBe('host-removed')

    expect(useWorkspaceStore.getState().workspaces).toEqual(incoming().workspaces)
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBe('wsR')

    expect(useSessionStore.getState().sessions.h1?.map((s) => s.code)).toEqual(['newA'])
    expect(useRebuildStore.getState().lockedBy).toBeNull()
  })

  it('writes structure-only -prev of the current world before stores change', async () => {
    vi.mocked(listSessions).mockResolvedValue([])
    const prevAtTabSet: Array<WorkspaceSnapshot | null> = []
    const tabSet = useTabStore.setState
    vi.spyOn(useTabStore, 'setState').mockImplementation((...args: Parameters<typeof tabSet>) => {
      prevAtTabSet.push(readPrevSnapshot())
      return tabSet(...args)
    })

    await restoreDeviceStateReplace(incoming(), { now: 5, buildSnapshotFn: async () => localWorld() })

    expect(prevAtTabSet.length).toBeGreaterThan(0)
    const prev = prevAtTabSet[0]
    expect(prev?.capturedAt).toBe(1) // the local world, not the seeded one
    expect(prev?.tabOrder).toEqual(['L1'])
    expect(prev?.sessionMeta.h1.lc1.restorable).toBe(false)
    expect(readPrevSnapshot()?.sessionMeta.h1.lc1.restorable).toBe(false)
  })

  it('replaceTabSnapshot failure → RestoreError with hostRemoved; stores keep the original world', async () => {
    vi.mocked(listSessions).mockResolvedValue([])
    const bad = { ...incoming(), tabOrder: ['A', 'B', 'C', 'missing'] }
    const before = storeView()

    let caught: unknown
    try {
      await restoreDeviceStateReplace(bad, { now: 5, buildSnapshotFn: async () => localWorld() })
    } catch (e) {
      caught = e
    }

    expect(caught).toBeInstanceOf(RestoreError)
    const report = (caught as RestoreError).report as Partial<DeviceStateRestoreReport>
    expect(report).toEqual({ reattached: 0, rebuilt: 0, failed: 2, hostRemoved: 1, rebuiltButUnattached: [] })
    expect((caught as RestoreError).cause).toBeInstanceOf(Error)
    expect(listSessions).toHaveBeenCalled() // failure happened AFTER reattach
    expect(storeView()).toEqual(before)
    expect(useSessionStore.getState().sessions).toEqual({})
    expect(useRebuildStore.getState().lockedBy).toBeNull()
  })

  it('-prev write failure → RestoreError; stores untouched', async () => {
    vi.mocked(listSessions).mockResolvedValue([])
    const before = storeView()

    await expect(restoreDeviceStateReplace(incoming(), {
      now: 5,
      buildSnapshotFn: async () => { throw new Error('build boom') },
    })).rejects.toBeInstanceOf(RestoreError)

    expect(storeView()).toEqual(before)
    expect(readPrevSnapshot()).toEqual(seededPrev())
  })

  it('stable world → the backup is built exactly once', async () => {
    vi.mocked(listSessions).mockResolvedValue([])
    const build = storeBuild()
    const prevWrites = countPrevWrites()

    await restoreDeviceStateReplace(incoming(), { now: 5, buildSnapshotFn: build })

    expect(build).toHaveBeenCalledTimes(1)
    expect(prevWrites()).toBe(1)
    expect(readPrevSnapshot()?.tabOrder).toEqual(['L1'])
  })

  it('tab opened while the backup is being built → -prev is rebuilt and contains it', async () => {
    vi.mocked(listSessions).mockResolvedValue([])
    const build = storeBuild((call) => { if (call === 1) openTab('LATE') })
    const prevWrites = countPrevWrites()

    await restoreDeviceStateReplace(incoming(), { now: 5, buildSnapshotFn: build })

    expect(build).toHaveBeenCalledTimes(2)
    expect(prevWrites()).toBe(1) // the unstable first capture is never persisted
    const prev = readPrevSnapshot()
    expect(prev?.tabOrder).toEqual(['L1', 'LATE'])
    expect(prev?.tabs.LATE).toBeDefined()
    expect(useTabStore.getState().tabOrder).toEqual(['A', 'B', 'C'])
  })

  it('world keeps changing during every backup build → RestoreError after 3 builds; nothing applied', async () => {
    vi.mocked(listSessions).mockResolvedValue([])
    const build = storeBuild((call) => { openTab(`U${call}`) })
    const tabSet = vi.spyOn(useTabStore, 'setState')
    const prevWrites = countPrevWrites()

    let caught: unknown
    try {
      await restoreDeviceStateReplace(incoming(), { now: 5, buildSnapshotFn: build })
    } catch (e) {
      caught = e
    }

    expect(caught).toBeInstanceOf(RestoreError)
    expect(((caught as RestoreError).cause as Error).message).toBe('workspace changed during restore; try again')
    expect((caught as RestoreError).report).toEqual({
      reattached: 0, rebuilt: 0, failed: 2, hostRemoved: 1, rebuiltButUnattached: [],
    })
    expect(build).toHaveBeenCalledTimes(3)
    expect(tabSet).toHaveBeenCalledTimes(3) // only the user's own edits; no replaceTabSnapshot
    expect(useTabStore.getState().tabOrder).toEqual(['L1', 'U1', 'U2', 'U3'])
    // A refused restore must not overwrite the user's existing undo backup.
    expect(prevWrites()).toBe(0)
    expect(readPrevSnapshot()).toEqual(seededPrev())
    expect(useWorkspaceStore.getState().workspaces).toEqual(localWorld().workspaces)
    expect(useSessionStore.getState().sessions).toEqual({})
    expect(useRebuildStore.getState().lockedBy).toBeNull()
  })
})

describe('restoreDeviceStateMerge', () => {
  /** Current world: workspace "Work" holding one tmux tab on h1 named "local". */
  const seedMergeLocal = (): void => {
    useTabStore.setState({
      tabs: { L1: tmuxTab('L1', 'h1', 'lc1', 'local') },
      tabOrder: ['L1'],
      activeTabId: 'L1',
      visitHistory: ['L1'],
    })
    useWorkspaceStore.setState({
      workspaces: [{ id: 'wsW', name: 'Work', tabs: ['L1'], activeTabId: 'L1' }],
      activeWorkspaceId: 'wsW',
    })
  }

  /** Incoming: "Work" (A new, D identical to L1) + missing workspace "Other" (B dead, C unknown host). */
  const mergeIncoming = (): WorkspaceSnapshot => ({
    version: 1,
    capturedAt: 9,
    tabs: {
      A: tmuxTab('A', 'h1', 'oldA', 'alpha'),
      D: tmuxTab('D', 'h1', 'oldL', 'local'),
      B: tmuxTab('B', 'h1', 'oldB', 'beta'),
      C: tmuxTab('C', 'gone', 'oldC', 'gamma'),
    },
    tabOrder: ['A', 'D', 'B', 'C'],
    activeTabId: 'A',
    workspaces: [
      { id: 'wsR1', name: 'Work', tabs: ['A', 'D'], activeTabId: 'A' },
      { id: 'wsR2', name: 'Other', tabs: ['B', 'C'], activeTabId: 'C' },
    ],
    activeWorkspaceId: 'wsR2',
    sessionMeta: {
      h1: {
        oldA: { hostId: 'h1', sessionCode: 'oldA', name: 'alpha', mode: 'terminal', cwd: '/a', restorable: true },
        oldL: { hostId: 'h1', sessionCode: 'oldL', name: 'local', mode: 'terminal', cwd: '/l', restorable: true },
        oldB: { hostId: 'h1', sessionCode: 'oldB', name: 'beta', mode: 'terminal', cwd: '/b', restorable: true },
      },
      gone: {
        oldC: { hostId: 'gone', sessionCode: 'oldC', name: 'gamma', mode: 'terminal', cwd: '/c', restorable: true },
      },
    },
  })

  const counter = (): (() => string) => {
    let n = 0
    return () => `n${++n}`
  }

  const deps = (extra?: Partial<Parameters<typeof restoreDeviceStateMerge>[1]>) => ({
    now: 5,
    buildSnapshotFn: async () => localWorld(),
    idGen: counter(),
    ...extra,
  })

  beforeEach(() => {
    localStorage.clear()
    vi.mocked(listSessions).mockReset()
    vi.mocked(createSession).mockReset()
    resetStores()
    useHostStore.setState({
      hosts: { h1: { id: 'h1', name: 'h1', ip: '127.0.0.1', port: 7860, order: 0 } },
      hostOrder: ['h1'],
    })
    seedMergeLocal()
    writePrevSnapshot(seededPrev())
  })

  afterEach(() => {
    expect(createSession).not.toHaveBeenCalled()
    vi.restoreAllMocks()
    resetStores()
    localStorage.clear()
  })

  it('refuses while another owner holds the lock; stores and -prev unchanged', async () => {
    const before = storeView()
    useRebuildStore.getState().acquireOperationLock('rebuild:p1')

    await expect(restoreDeviceStateMerge(mergeIncoming(), deps()))
      .rejects.toThrow('snapshot:deviceStateMerge refused: another operation is already running (rebuild:p1)')

    expect(storeView()).toEqual(before)
    expect(readPrevSnapshot()).toEqual(seededPrev())
    expect(listSessions).not.toHaveBeenCalled()
    expect(useRebuildStore.getState().lockedBy).toBe('rebuild:p1')
  })

  it.each([
    ['null', () => null],
    ['version 2', () => ({ ...mergeIncoming(), version: 2 })],
  ])('malformed payload (%s) → throws; nothing changed; listSessions never called', async (_n, make) => {
    const before = storeView()
    const build = vi.fn(async () => localWorld())

    await expect(restoreDeviceStateMerge(make(), deps({ buildSnapshotFn: build })))
      .rejects.toThrow('malformed device state payload')

    expect(storeView()).toEqual(before)
    expect(readPrevSnapshot()).toEqual(seededPrev())
    expect(listSessions).not.toHaveBeenCalled()
    expect(build).not.toHaveBeenCalled()
    expect(useRebuildStore.getState().lockedBy).toBeNull()
  })

  it('happy path: appends missing tabs, skips identical, adds workspace, keeps active ids, re-points by name', async () => {
    vi.mocked(listSessions).mockResolvedValue([session({ code: 'newA', name: 'alpha' })])

    const report: DeviceStateMergeReport = await restoreDeviceStateMerge(mergeIncoming(), deps())

    expect(report).toEqual({
      reattached: 1,
      rebuilt: 0,
      failed: 2,
      hostRemoved: 1,
      rebuiltButUnattached: [],
      addedWorkspaces: 1,
      addedTabs: 3,
      skippedTabs: 1,
    })
    expect(listSessions).toHaveBeenCalledTimes(1)
    expect(listSessions).toHaveBeenCalledWith('h1')

    const { tabs, tabOrder, activeTabId } = useTabStore.getState()
    const { workspaces, activeWorkspaceId } = useWorkspaceStore.getState()
    expect(activeTabId).toBe('L1')
    expect(activeWorkspaceId).toBe('wsW')
    expect(tabOrder).toHaveLength(4)
    expect(tabOrder[0]).toBe('L1')
    expect(Object.keys(tabs)).toHaveLength(4)
    expect(tabs.L1).toEqual(tmuxTab('L1', 'h1', 'lc1', 'local'))

    expect(workspaces.map((w) => w.name)).toEqual(['Work', 'Other'])
    const work = workspaces[0]
    expect(work.id).toBe('wsW')
    expect(work.activeTabId).toBe('L1')
    expect(work.tabs).toHaveLength(2)
    expect(work.tabs[0]).toBe('L1')

    // The appended "Work" tab is incoming A, re-pointed to the live session's new code.
    const a = paneOf(tabs[work.tabs[1]])
    expect(a.kind === 'tmux-session' && a.cachedName).toBe('alpha')
    expect(a.kind === 'tmux-session' && a.sessionCode).toBe('newA')
    expect(a.kind === 'tmux-session' && a.tmuxInstance).toBe('new-inst')
    expect(a.kind === 'tmux-session' && a.terminated).toBeUndefined()

    const other = workspaces[1]
    expect(other.tabs).toHaveLength(2)
    const b = paneOf(tabs[other.tabs[0]])
    expect(b.kind === 'tmux-session' && b.cachedName).toBe('beta')
    expect(b.kind === 'tmux-session' && b.terminated).toBe('tmux-restarted')
    const c = paneOf(tabs[other.tabs[1]])
    expect(c.kind === 'tmux-session' && c.cachedName).toBe('gamma')
    expect(c.kind === 'tmux-session' && c.terminated).toBe('host-removed')
    expect(other.activeTabId).toBe(other.tabs[1])

    // No incoming ids leak into the merged world.
    for (const id of ['A', 'B', 'C', 'D']) expect(tabs[id]).toBeUndefined()

    expect(useSessionStore.getState().sessions.h1?.map((s) => s.code)).toEqual(['newA'])
    expect(useRebuildStore.getState().lockedBy).toBeNull()
  })

  it('keeps a tab opened while listSessions is pending', async () => {
    let resolve!: (s: Session[]) => void
    vi.mocked(listSessions).mockReturnValue(new Promise<Session[]>((r) => { resolve = r }))

    const pending = restoreDeviceStateMerge(mergeIncoming(), deps())
    await vi.waitFor(() => expect(listSessions).toHaveBeenCalled())

    const late = tmuxTab('LATE', 'h1', 'lateCode', 'late')
    useTabStore.setState({
      tabs: { ...useTabStore.getState().tabs, LATE: late },
      tabOrder: [...useTabStore.getState().tabOrder, 'LATE'],
    })

    resolve([])
    const report = await pending

    const { tabs, tabOrder } = useTabStore.getState()
    expect(tabs.LATE).toEqual(late)
    expect(tabOrder.slice(0, 2)).toEqual(['L1', 'LATE'])
    expect(report.addedTabs).toBe(3)
  })

  it('tab opened while the backup is being built → -prev rebuilt with it, and the merge keeps it', async () => {
    vi.mocked(listSessions).mockResolvedValue([])
    let late: Tab | undefined
    const build = storeBuild((call) => { if (call === 1) late = openTab('LATE') })
    const prevWrites = countPrevWrites()

    await restoreDeviceStateMerge(mergeIncoming(), deps({ buildSnapshotFn: build }))

    expect(build).toHaveBeenCalledTimes(2)
    expect(prevWrites()).toBe(1)
    const prev = readPrevSnapshot()
    expect(prev?.tabOrder).toEqual(['L1', 'LATE'])
    expect(prev?.tabs.LATE).toEqual(late)
    const { tabs, tabOrder } = useTabStore.getState()
    expect(tabs.LATE).toEqual(late)
    expect(tabOrder.slice(0, 2)).toEqual(['L1', 'LATE'])
  })

  it('stable world → the backup is built exactly once', async () => {
    vi.mocked(listSessions).mockResolvedValue([])
    const build = storeBuild()
    const prevWrites = countPrevWrites()

    await restoreDeviceStateMerge(mergeIncoming(), deps({ buildSnapshotFn: build }))

    expect(build).toHaveBeenCalledTimes(1)
    expect(prevWrites()).toBe(1)
  })

  it('world keeps changing during every backup build → RestoreError after 3 builds; existing -prev untouched', async () => {
    vi.mocked(listSessions).mockResolvedValue([])
    const build = storeBuild((call) => { openTab(`U${call}`) })
    const prevWrites = countPrevWrites()

    let caught: unknown
    try {
      await restoreDeviceStateMerge(mergeIncoming(), deps({ buildSnapshotFn: build }))
    } catch (e) {
      caught = e
    }

    expect(caught).toBeInstanceOf(RestoreError)
    expect(((caught as RestoreError).cause as Error).message).toBe('workspace changed during restore; try again')
    expect(build).toHaveBeenCalledTimes(3)
    expect(prevWrites()).toBe(0)
    expect(readPrevSnapshot()).toEqual(seededPrev())
    expect(useTabStore.getState().tabOrder).toEqual(['L1', 'U1', 'U2', 'U3'])
    expect(useWorkspaceStore.getState().workspaces.map((w) => w.id)).toEqual(['wsW'])
    expect(useRebuildStore.getState().lockedBy).toBeNull()
  })

  it('writes structure-only -prev of the current world before stores change', async () => {
    vi.mocked(listSessions).mockResolvedValue([])
    const prevAtTabSet: Array<WorkspaceSnapshot | null> = []
    const tabSet = useTabStore.setState
    vi.spyOn(useTabStore, 'setState').mockImplementation((...args: Parameters<typeof tabSet>) => {
      prevAtTabSet.push(readPrevSnapshot())
      return tabSet(...args)
    })

    await restoreDeviceStateMerge(mergeIncoming(), deps())

    expect(prevAtTabSet.length).toBeGreaterThan(0)
    const prev = prevAtTabSet[0]
    expect(prev?.capturedAt).toBe(1)
    expect(prev?.tabOrder).toEqual(['L1'])
    expect(prev?.sessionMeta.h1.lc1.restorable).toBe(false)
    expect(readPrevSnapshot()?.sessionMeta.h1.lc1.restorable).toBe(false)
  })

  it('replaceTabSnapshot failure → RestoreError with hostRemoved and zero merge counts; stores rolled back', async () => {
    vi.mocked(listSessions).mockResolvedValue([session({ code: 'newA', name: 'alpha' })])
    const before = storeView()
    const tabSetCalls = vi.spyOn(useTabStore, 'setState')
    const wsSet = useWorkspaceStore.setState
    vi.spyOn(useWorkspaceStore, 'setState')
      .mockImplementationOnce(() => { throw new Error('ws boom') })
      .mockImplementation((...args: Parameters<typeof wsSet>) => wsSet(...args))

    let caught: unknown
    try {
      await restoreDeviceStateMerge(mergeIncoming(), deps())
    } catch (e) {
      caught = e
    }

    expect(caught).toBeInstanceOf(RestoreError)
    expect((caught as RestoreError).report).toEqual({
      reattached: 1,
      rebuilt: 0,
      failed: 2,
      hostRemoved: 1,
      rebuiltButUnattached: [],
      addedWorkspaces: 0,
      addedTabs: 0,
      skippedTabs: 0,
    })
    expect((caught as RestoreError).cause).toBeInstanceOf(Error)
    expect(tabSetCalls).toHaveBeenCalledTimes(2) // applied, then rolled back
    expect(storeView()).toEqual(before)
    expect(useSessionStore.getState().sessions).toEqual({})
    expect(useRebuildStore.getState().lockedBy).toBeNull()
  })

  it('Undo after merge replays the structure-only -prev and never calls createSession', async () => {
    vi.mocked(listSessions).mockResolvedValue([session({ code: 'newA', name: 'alpha' })])
    await restoreDeviceStateMerge(mergeIncoming(), deps())
    expect(useTabStore.getState().tabOrder).toHaveLength(4)

    vi.mocked(listSessions).mockReset()
    vi.mocked(listSessions).mockResolvedValue([])
    const undo = await undoLastRestore({ now: 7, buildSnapshotFn: async () => localWorld() })

    expect(undo).not.toBeNull()
    expect(createSession).not.toHaveBeenCalled()
    expect(useTabStore.getState().tabOrder).toEqual(['L1'])
    expect(useWorkspaceStore.getState().workspaces.map((w) => w.id)).toEqual(['wsL'])
    expect(useRebuildStore.getState().lockedBy).toBeNull()
  })
})
