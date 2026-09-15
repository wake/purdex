import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createSession, listSessions } from '../host-api'
import type { Session } from '../host-api'
import type { PaneContent, Tab } from '../../types/tab'
import type { WorkspaceSnapshot } from '../snapshot/types'
import { RestoreError } from '../snapshot/types'
import { readPrevSnapshot, writePrevSnapshot } from '../snapshot/storage'
import { useTabStore } from '../../stores/useTabStore'
import { useWorkspaceStore } from '../../features/workspace/store'
import { useSessionStore } from '../../stores/useSessionStore'
import { useHostStore } from '../../stores/useHostStore'
import { useRebuildStore } from '../../stores/useRebuildStore'
import type { DeviceStateRestoreReport } from './restore'
import { DEVICE_STATE_LOCK_OWNER, restoreDeviceStateReplace } from './restore'

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
    cc_session_id: '',
    cc_model: '',
    has_relay: false,
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
})
