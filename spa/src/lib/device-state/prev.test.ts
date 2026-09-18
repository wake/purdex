import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createSession, listSessions } from '../host-api'
import type { PaneContent, Tab } from '../../types/tab'
import type { WorkspaceSnapshot } from '../snapshot/types'
import { readPrevSnapshot } from '../snapshot/storage'
import { browserStorage } from '../storage/browser-backend'
import { undoLastRestore } from '../snapshot/restore'
import { useTabStore } from '../../stores/useTabStore'
import { useWorkspaceStore } from '../../features/workspace/store'
import { useSessionStore } from '../../stores/useSessionStore'
import { useHostStore } from '../../stores/useHostStore'
import { buildDeviceStatePrev, writeDeviceStatePrev } from './prev'

vi.mock('../host-api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../host-api')>()),
  listSessions: vi.fn(),
  createSession: vi.fn(),
}))

function tmuxTab(id: string, hostId: string, sessionCode: string): Tab {
  return {
    id,
    pinned: false,
    locked: false,
    createdAt: 0,
    layout: {
      type: 'leaf',
      pane: {
        id: `pane-${id}`,
        content: { kind: 'tmux-session', hostId, sessionCode, mode: 'terminal', cachedName: 'work', tmuxInstance: 'inst-1' },
      },
    },
  }
}

function world(): WorkspaceSnapshot {
  return {
    version: 1,
    capturedAt: 5,
    tabs: { t1: tmuxTab('t1', 'h1', 'c1') },
    tabOrder: ['t1'],
    activeTabId: 't1',
    workspaces: [{ id: 'ws1', name: 'ws1', tabs: ['t1'], activeTabId: 't1' }],
    activeWorkspaceId: 'ws1',
    sessionMeta: {
      h1: {
        c1: { hostId: 'h1', sessionCode: 'c1', name: 'work', mode: 'terminal', cwd: '/tmp', restorable: true },
        c2: { hostId: 'h1', sessionCode: 'c2', name: 'other', mode: 'terminal', cwd: '/srv', restorable: true },
      },
      h2: {
        c9: { hostId: 'h2', sessionCode: 'c9', name: 'far', mode: 'terminal', cwd: '/x', restorable: false },
      },
    },
  }
}

const resetStores = (): void => {
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
  useWorkspaceStore.setState({ workspaces: [], activeWorkspaceId: null })
  useSessionStore.setState({ sessions: {}, activeHostId: null, activeCode: null })
}

describe('buildDeviceStatePrev', () => {
  beforeEach(() => {
    localStorage.clear()
  })

  afterEach(() => {
    vi.restoreAllMocks()
    localStorage.clear()
  })

  it('returns the build output with every sessionMeta entry restorable:false and never touches storage', async () => {
    const built = world()
    const pristine = structuredClone(built)
    const build = vi.fn(async () => built)
    const setItem = vi.spyOn(browserStorage, 'setItem')

    const prev = await buildDeviceStatePrev(42, build)

    expect(build).toHaveBeenCalledWith(42)
    const expected = structuredClone(pristine)
    for (const perHost of Object.values(expected.sessionMeta)) {
      for (const m of Object.values(perHost)) m.restorable = false
    }
    expect(prev).toEqual(expected)
    expect(built).toEqual(pristine)
    expect(setItem).not.toHaveBeenCalled()
    expect(readPrevSnapshot()).toBeNull()
  })
})

describe('writeDeviceStatePrev', () => {
  beforeEach(() => {
    localStorage.clear()
    vi.mocked(listSessions).mockReset()
    vi.mocked(createSession).mockReset()
    resetStores()
  })

  afterEach(() => {
    resetStores()
    localStorage.clear()
  })

  it('stores -prev with every sessionMeta entry restorable:false, other fields preserved, build output untouched', async () => {
    const built = world()
    const pristine = structuredClone(built)
    const build = vi.fn(async () => built)

    await writeDeviceStatePrev(42, build)

    expect(build).toHaveBeenCalledWith(42)
    const prev = readPrevSnapshot()
    expect(prev).not.toBeNull()
    for (const perHost of Object.values(prev!.sessionMeta)) {
      for (const m of Object.values(perHost)) expect(m.restorable).toBe(false)
    }
    // Everything except `restorable` is preserved.
    const expected = structuredClone(pristine)
    for (const perHost of Object.values(expected.sessionMeta)) {
      for (const m of Object.values(perHost)) m.restorable = false
    }
    expect(prev).toEqual(expected)
    expect(prev!.sessionMeta.h1.c1.cwd).toBe('/tmp')
    // The snapshot returned by build is not mutated.
    expect(built).toEqual(pristine)
  })

  it('undo after a device-state replace never calls createSession; dead pane ends terminated', async () => {
    useHostStore.setState({
      hosts: { h1: { id: 'h1', name: 'h1', ip: '127.0.0.1', port: 7860, order: 0 } },
      hostOrder: ['h1'],
    })
    const snap = world()
    useTabStore.setState({ tabs: snap.tabs, tabOrder: snap.tabOrder, activeTabId: snap.activeTabId, visitHistory: [] })
    useWorkspaceStore.setState({ workspaces: snap.workspaces, activeWorkspaceId: snap.activeWorkspaceId })

    await writeDeviceStatePrev(1, async () => world())

    vi.mocked(listSessions).mockResolvedValue([]) // no live sessions anywhere
    vi.mocked(createSession).mockResolvedValue({
      code: 'zz', name: 'work', cwd: '/tmp', mode: 'terminal',
    })

    const report = await undoLastRestore({ now: 2, buildSnapshotFn: async () => world() })

    expect(createSession).not.toHaveBeenCalled()
    expect(report).not.toBeNull()
    expect(report!.rebuilt).toBe(0)
    const pane = (useTabStore.getState().tabs.t1.layout as { pane: { content: PaneContent } }).pane.content
    expect(pane.kind).toBe('tmux-session')
    if (pane.kind === 'tmux-session') {
      expect(pane.sessionCode).toBe('c1')
      expect(pane.terminated).toBe('tmux-restarted')
    }
  })
})
