import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { UNSORTED_WORKSPACE_ID, WATCH_WORKSPACE_ID, useWorkspaceStore } from './store'
import { usePoolStore } from '../../lib/pool/store'
import { usePoolHostsStore } from '../../stores/usePoolHostsStore'
import { createWatchReconciler, ABSENT_REMOVE_MS } from './lib/watch-area'
import { WATCH_GUARDIAN_TAB_ID, watchHostTabId } from '../../lib/watch/ids'
import { useTabStore } from '../../stores/useTabStore'
import { useHistoryStore } from '../../stores/useHistoryStore'
import { useUndoToast } from '../../stores/useUndoToast'
import { useTabMenuExtrasStore } from '../../stores/useTabMenuExtrasStore'
import { useTabWorkspaceActions } from './hooks'
import { getVisibleTabIds } from './lib/getVisibleTabIds'
import { closeTab } from '../../lib/tab-lifecycle'
import { isTabMoving } from '../../lib/team/moving-tabs'
import { createTab } from '../../types/tab'
import type { Tab } from '../../types/tab'

// The real closeTab, observed: the tear-off cases below assert whether the handler reached it.
vi.mock('../../lib/tab-lifecycle', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../lib/tab-lifecycle')>()
  return { ...actual, closeTab: vi.fn(actual.closeTab) }
})

describe('workspace tab recall', () => {
  beforeEach(() => {
    useWorkspaceStore.getState().reset()
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
  })

  it('handleSelectWorkspace uses latest store state, not stale closure', () => {
    const tab1 = createTab({ kind: 'dashboard' })
    const tab2 = createTab({ kind: 'hosts' })
    useTabStore.getState().addTab(tab1)
    useTabStore.getState().addTab(tab2)
    const ws1 = useWorkspaceStore.getState().addWorkspace('WS1')
    const ws2 = useWorkspaceStore.getState().addWorkspace('WS2')
    useWorkspaceStore.getState().addTabToWorkspace(ws1.id, tab1.id)
    useWorkspaceStore.getState().addTabToWorkspace(ws2.id, tab2.id)
    useWorkspaceStore.getState().setWorkspaceActiveTab(ws1.id, tab1.id)
    useWorkspaceStore.getState().setWorkspaceActiveTab(ws2.id, tab2.id)

    const displayTabs = [tab1, tab2]
    const { result } = renderHook(() => useTabWorkspaceActions(displayTabs))

    act(() => { result.current.handleSelectWorkspace(ws2.id) })
    expect(useTabStore.getState().activeTabId).toBe(tab2.id)
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBe(ws2.id)

    act(() => { useWorkspaceStore.getState().setWorkspaceActiveTab(ws1.id, tab1.id) })

    act(() => { result.current.handleSelectWorkspace(ws1.id) })
    expect(useTabStore.getState().activeTabId).toBe(tab1.id)
  })

  it('falls back to first tab when activeTabId points to closed tab', () => {
    const tab1 = createTab({ kind: 'dashboard' })
    const tab2 = createTab({ kind: 'hosts' })
    useTabStore.getState().addTab(tab1)
    useTabStore.getState().addTab(tab2)
    const ws = useWorkspaceStore.getState().addWorkspace('WS')
    useWorkspaceStore.getState().addTabToWorkspace(ws.id, tab1.id)
    useWorkspaceStore.getState().addTabToWorkspace(ws.id, tab2.id)
    useWorkspaceStore.getState().setWorkspaceActiveTab(ws.id, tab1.id)

    const displayTabs = [tab1, tab2] as Tab[]
    const { result } = renderHook(() => useTabWorkspaceActions(displayTabs))

    useWorkspaceStore.getState().removeTabFromWorkspace(ws.id, tab1.id)
    useTabStore.getState().closeTab(tab1.id)

    act(() => { result.current.handleSelectWorkspace(ws.id) })
    expect(useTabStore.getState().activeTabId).toBe(tab2.id)
  })

  it('clears activeTab when selecting an empty workspace while a tab of another workspace is active', () => {
    // (Was: "from a standalone tab". Every tab belongs to a workspace now — spec §4.3 — and the rule is the same.)
    const other = useWorkspaceStore.getState().addWorkspace('Other')
    const tab = createTab({ kind: 'dashboard' })
    useTabStore.getState().addTab(tab)
    useWorkspaceStore.getState().insertTab(tab.id, other.id)
    useTabStore.getState().setActiveTab(tab.id)

    // Empty workspace.
    const emptyWs = useWorkspaceStore.getState().addWorkspace('Empty')

    const { result } = renderHook(() => useTabWorkspaceActions([tab]))

    act(() => { result.current.handleSelectWorkspace(emptyWs.id) })

    expect(useWorkspaceStore.getState().activeWorkspaceId).toBe(emptyWs.id)
    // Must drop the other workspace's tab so it stops masking the workspace selection.
    expect(useTabStore.getState().activeTabId).toBeNull()
    expect(useWorkspaceStore.getState().findWorkspaceByTab(tab.id)?.id).toBe(other.id)
  })
})

// Every tab belongs to exactly one workspace (Profile Sync spec §4.3): the `+` button is a producer.
describe('handleAddTab', () => {
  beforeEach(() => {
    useWorkspaceStore.getState().reset()
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
  })

  const addOne = (): string => {
    const { result } = renderHook(() => useTabWorkspaceActions([]))
    act(() => { result.current.handleAddTab() })
    const { tabOrder, activeTabId } = useTabStore.getState()
    expect(tabOrder).toHaveLength(1)
    expect(activeTabId).toBe(tabOrder[0])
    return tabOrder[0]
  }

  it('puts the tab in the active workspace', () => {
    useWorkspaceStore.getState().addWorkspace('A')
    const b = useWorkspaceStore.getState().addWorkspace('B')
    useWorkspaceStore.getState().setActiveWorkspace(b.id)
    expect(useWorkspaceStore.getState().findWorkspaceByTab(addOne())?.id).toBe(b.id)
  })

  it('no active workspace → the first workspace', () => {
    const a = useWorkspaceStore.getState().addWorkspace('A')
    useWorkspaceStore.getState().addWorkspace('B')
    useWorkspaceStore.getState().setActiveWorkspace(null)
    expect(useWorkspaceStore.getState().findWorkspaceByTab(addOne())?.id).toBe(a.id)
  })

  it('no workspace at all → Unsorted is created, gets the tab and becomes active', () => {
    const tabId = addOne()
    const { workspaces, activeWorkspaceId } = useWorkspaceStore.getState()
    expect(workspaces.map((w) => w.id)).toEqual([UNSORTED_WORKSPACE_ID])
    expect(workspaces[0].tabs).toEqual([tabId])
    expect(activeWorkspaceId).toBe(UNSORTED_WORKSPACE_ID)
  })
})

describe('openSingletonAndSelect', () => {
  beforeEach(() => {
    useWorkspaceStore.getState().reset()
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
  })

  it('creates singleton tab, inserts into active workspace, and selects it', () => {
    const ws = useWorkspaceStore.getState().addWorkspace('WS1')
    useWorkspaceStore.getState().setActiveWorkspace(ws.id)

    const { result } = renderHook(() => useTabWorkspaceActions([]))

    let tabId: string
    act(() => {
      tabId = result.current.openSingletonAndSelect({ kind: 'hosts' })
    })

    // Tab was created
    expect(useTabStore.getState().tabs[tabId!]).toBeDefined()
    // Tab is active
    expect(useTabStore.getState().activeTabId).toBe(tabId!)
    // Tab is in workspace
    const updatedWs = useWorkspaceStore.getState().workspaces.find(w => w.id === ws.id)
    expect(updatedWs!.tabs).toContain(tabId!)
    // Workspace active tab is set
    expect(updatedWs!.activeTabId).toBe(tabId!)
  })

  it('reuses existing singleton tab instead of creating duplicate', () => {
    const ws = useWorkspaceStore.getState().addWorkspace('WS1')
    useWorkspaceStore.getState().setActiveWorkspace(ws.id)

    const { result } = renderHook(() => useTabWorkspaceActions([]))

    let tabId1: string
    let tabId2: string
    act(() => {
      tabId1 = result.current.openSingletonAndSelect({ kind: 'hosts' })
    })
    act(() => {
      tabId2 = result.current.openSingletonAndSelect({ kind: 'hosts' })
    })

    expect(tabId1!).toBe(tabId2!)
    expect(Object.keys(useTabStore.getState().tabs)).toHaveLength(1)
  })

  it('works without any workspace: the tab lands in a new Unsorted', () => {
    useWorkspaceStore.getState().reset()
    const { result } = renderHook(() => useTabWorkspaceActions([]))

    let tabId: string
    act(() => {
      tabId = result.current.openSingletonAndSelect({ kind: 'settings', scope: 'global' })
    })

    expect(useTabStore.getState().tabs[tabId!]).toBeDefined()
    expect(useTabStore.getState().activeTabId).toBe(tabId!)
    expect(useWorkspaceStore.getState().findWorkspaceByTab(tabId!)?.id).toBe(UNSORTED_WORKSPACE_ID)
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBe(UNSORTED_WORKSPACE_ID)
  })

  it('inserts tab into explicit wsId even when a different workspace is active', () => {
    const wsA = useWorkspaceStore.getState().addWorkspace('WS-A')
    const wsB = useWorkspaceStore.getState().addWorkspace('WS-B')
    useWorkspaceStore.getState().setActiveWorkspace(wsA.id)

    const { result } = renderHook(() => useTabWorkspaceActions([]))

    let tabId: string
    act(() => {
      tabId = result.current.openSingletonAndSelect(
        { kind: 'settings', scope: { workspaceId: wsB.id } },
        wsB.id,
      )
    })

    // Tab should be inserted into wsB, NOT the active wsA
    const updatedWsB = useWorkspaceStore.getState().workspaces.find(w => w.id === wsB.id)
    const updatedWsA = useWorkspaceStore.getState().workspaces.find(w => w.id === wsA.id)
    expect(updatedWsB!.tabs).toContain(tabId!)
    expect(updatedWsA!.tabs).not.toContain(tabId!)
  })
})

describe('handleAddTabToWorkspace', () => {
  beforeEach(() => {
    useWorkspaceStore.getState().reset()
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
  })

  it('creates a tab, adds to tab store, and inserts into given workspace', () => {
    const ws = useWorkspaceStore.getState().addWorkspace('A')

    const { result } = renderHook(() => useTabWorkspaceActions([]))
    act(() => {
      result.current.handleAddTabToWorkspace(ws.id)
    })

    const updated = useWorkspaceStore.getState().workspaces.find((w) => w.id === ws.id)!
    expect(updated.tabs.length).toBe(1)
    const newTabId = updated.tabs[0]
    expect(updated.activeTabId).toBe(newTabId)
    expect(useTabStore.getState().tabs[newTabId]).toBeDefined()
    expect(useTabStore.getState().activeTabId).toBe(newTabId)
  })

  it('switches activeWorkspaceId to the target workspace', () => {
    const wsA = useWorkspaceStore.getState().addWorkspace('A')
    const wsB = useWorkspaceStore.getState().addWorkspace('B')
    useWorkspaceStore.getState().setActiveWorkspace(wsA.id)

    const { result } = renderHook(() => useTabWorkspaceActions([]))
    act(() => {
      result.current.handleAddTabToWorkspace(wsB.id)
    })

    expect(useWorkspaceStore.getState().activeWorkspaceId).toBe(wsB.id)
  })
})

describe('handleReorderWorkspaceTabs', () => {
  beforeEach(() => {
    useWorkspaceStore.getState().reset()
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
  })

  it('delegates to workspace store reorderWorkspaceTabs', () => {
    const ws = useWorkspaceStore.getState().addWorkspace('A')
    const t1 = createTab({ kind: 'new-tab' })
    const t2 = createTab({ kind: 'new-tab' })
    const t3 = createTab({ kind: 'new-tab' })
    useTabStore.getState().addTab(t1)
    useTabStore.getState().addTab(t2)
    useTabStore.getState().addTab(t3)
    useWorkspaceStore.getState().addTabToWorkspace(ws.id, t1.id)
    useWorkspaceStore.getState().addTabToWorkspace(ws.id, t2.id)
    useWorkspaceStore.getState().addTabToWorkspace(ws.id, t3.id)

    const { result } = renderHook(() => useTabWorkspaceActions([]))
    act(() => {
      result.current.handleReorderWorkspaceTabs(ws.id, [t2.id, t1.id, t3.id])
    })

    const updated = useWorkspaceStore.getState().workspaces.find((w) => w.id === ws.id)!
    expect(updated.tabs).toEqual([t2.id, t1.id, t3.id])
  })
})

// The tab bar's own reorder. Every tab belongs to a workspace (Profile Sync spec §4.3): the bar lists the active
// workspace's tabs, and there is no list of workspace-less tabs for it to reorder.
describe('handleReorderTabs', () => {
  beforeEach(() => {
    useWorkspaceStore.getState().reset()
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
  })

  function twoTabs(): [Tab, Tab] {
    const a = createTab({ kind: 'dashboard' })
    const b = createTab({ kind: 'hosts' })
    useTabStore.getState().addTab(a)
    useTabStore.getState().addTab(b)
    return [a, b]
  }

  it('reorders the active workspace\'s tabs, and leaves the global tabOrder alone', () => {
    const [a, b] = twoTabs()
    const ws = useWorkspaceStore.getState().addWorkspace('WS')
    useWorkspaceStore.getState().insertTab(a.id, ws.id)
    useWorkspaceStore.getState().insertTab(b.id, ws.id)
    useWorkspaceStore.getState().setActiveWorkspace(ws.id)

    const { result } = renderHook(() => useTabWorkspaceActions([a, b]))
    act(() => { result.current.handleReorderTabs([b.id, a.id]) })

    expect(useWorkspaceStore.getState().workspaces.find((w) => w.id === ws.id)!.tabs).toEqual([b.id, a.id])
    expect(useTabStore.getState().tabOrder).toEqual([a.id, b.id])
  })

  it('with no active workspace it changes nothing (was: reordered the standalone tabs in tabOrder)', () => {
    const [a, b] = twoTabs()
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBeNull()

    const { result } = renderHook(() => useTabWorkspaceActions([a, b]))
    act(() => { result.current.handleReorderTabs([b.id, a.id]) })

    expect(useTabStore.getState().tabOrder).toEqual([a.id, b.id])
    expect(useWorkspaceStore.getState().workspaces).toEqual([])
  })
})

// MON-6b: dragging host tabs in the watcher area writes the order of the pool hosts (W2), which the sidebar and every device follow.
describe('handleReorderTabs in the watcher area', () => {
  const T0 = 1_000_000
  const MLAB = '82b11c521df16c12'
  const MSERV = '0123456789abcdef'
  const GONE = 'fedcba9876543210'
  const hostTabs = () => useWorkspaceStore.getState().workspaces.find((w) => w.id === WATCH_WORKSPACE_ID)!.tabs
  const setPool = (ids: string[]) => usePoolStore.setState({ hosts: ids.map((id) => ({ host_id: id, alias: id, version: '1', proto: 1, platform: 'linux', arch: 'amd64', facts: null, worker: { enabled: false, slots: { heavy: 0, light: 0 }, owner_rule: '' }, labels: [], reachable: true, liveness: 'ok' })) as never, sourceHostId: 'cfg-a' } as never)

  beforeEach(() => {
    useWorkspaceStore.getState().reset()
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
    usePoolStore.setState({ hosts: [], sourceHostId: null } as never)
    usePoolHostsStore.setState({ order: [], looks: {} } as never)
    useWorkspaceStore.getState().addWorkspace('A')
    useWorkspaceStore.getState().ensureWatchWorkspace()
    setPool([MLAB, MSERV])
    usePoolHostsStore.setState({ order: [MLAB, MSERV] } as never)
    createWatchReconciler().reconcile({ hostIds: [MLAB, MSERV] }, T0)
    useWorkspaceStore.getState().setActiveWorkspace(WATCH_WORKSPACE_ID)
  })

  it('dragging mserv before mlab writes the order (the sidebar and the other devices read it)', () => {
    const { result } = renderHook(() => useTabWorkspaceActions([]))
    act(() => { result.current.handleReorderTabs([WATCH_GUARDIAN_TAB_ID, watchHostTabId(MSERV), watchHostTabId(MLAB)]) })
    expect(usePoolHostsStore.getState().order).toEqual([MSERV, MLAB])
    expect(hostTabs()).toEqual([WATCH_GUARDIAN_TAB_ID, watchHostTabId(MSERV), watchHostTabId(MLAB)])
    // the next pass has nothing to write
    const before = useWorkspaceStore.getState().workspaces
    createWatchReconciler().reconcile({ hostIds: [MSERV, MLAB] }, T0 + 1000)
    expect(useWorkspaceStore.getState().workspaces).toBe(before)
  })

  it('hosts that are not on screen keep their place in the order after the ones that were dragged', () => {
    usePoolHostsStore.setState({ order: [MLAB, GONE, MSERV] } as never)
    const { result } = renderHook(() => useTabWorkspaceActions([]))
    act(() => { result.current.handleReorderTabs([WATCH_GUARDIAN_TAB_ID, watchHostTabId(MSERV), watchHostTabId(MLAB)]) })
    expect(usePoolHostsStore.getState().order).toEqual([MSERV, MLAB, GONE])
  })

  it('dragging the guardian tab does not touch the order', () => {
    usePoolHostsStore.setState({ order: [] } as never)
    const { result } = renderHook(() => useTabWorkspaceActions([]))
    act(() => { result.current.handleReorderTabs([watchHostTabId(MLAB), WATCH_GUARDIAN_TAB_ID, watchHostTabId(MSERV)]) })
    expect(usePoolHostsStore.getState().order).toEqual([])
    expect(hostTabs()).toEqual([watchHostTabId(MLAB), WATCH_GUARDIAN_TAB_ID, watchHostTabId(MSERV)])
  })

  it('outside the watcher area a reorder does not write the order', () => {
    const [a, b] = [createTab({ kind: 'dashboard' }), createTab({ kind: 'hosts' })]
    useTabStore.getState().addTab(a); useTabStore.getState().addTab(b)
    const ws = useWorkspaceStore.getState().addWorkspace('WS')
    useWorkspaceStore.getState().insertTab(a.id, ws.id); useWorkspaceStore.getState().insertTab(b.id, ws.id)
    useWorkspaceStore.getState().setActiveWorkspace(ws.id)
    const { result } = renderHook(() => useTabWorkspaceActions([a, b]))
    act(() => { result.current.handleReorderTabs([b.id, a.id]) })
    expect(usePoolHostsStore.getState().order).toEqual([MLAB, MSERV])
  })

  it('two devices: B receives A\'s order and rearranges its tabs, then neither writes again', () => {
    const { result } = renderHook(() => useTabWorkspaceActions([]))
    act(() => { result.current.handleReorderTabs([WATCH_GUARDIAN_TAB_ID, watchHostTabId(MSERV), watchHostTabId(MLAB)]) })
    const orderOnA = usePoolHostsStore.getState().order
    // B: its tabs are still [mlab, mserv] and the synced order arrives
    useWorkspaceStore.getState().reorderWorkspaceTabs(WATCH_WORKSPACE_ID, [WATCH_GUARDIAN_TAB_ID, watchHostTabId(MLAB), watchHostTabId(MSERV)])
    usePoolHostsStore.setState({ order: orderOnA } as never)
    const rB = createWatchReconciler()
    rB.reconcile({ hostIds: [MSERV, MLAB] }, T0 + ABSENT_REMOVE_MS)
    expect(hostTabs()).toEqual([WATCH_GUARDIAN_TAB_ID, watchHostTabId(MSERV), watchHostTabId(MLAB)])
    const before = [useWorkspaceStore.getState().workspaces, usePoolHostsStore.getState().order]
    rB.reconcile({ hostIds: [MSERV, MLAB] }, T0 + ABSENT_REMOVE_MS + 1000)
    expect([useWorkspaceStore.getState().workspaces, usePoolHostsStore.getState().order]).toEqual(before)
  })
})

// The range of close-others / close-right is what App hands the hook: `getVisibleTabIds`. With workspaces but no
// pointer (boot, an unsettled world — the moment before adopt-standalone.ts re-points it) that used to be every
// tab, and "close others" on a tab of one workspace closed the tabs of all of them.
describe('closeOthers / closeRight while activeWorkspaceId is null', () => {
  beforeEach(() => {
    useWorkspaceStore.getState().reset()
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
  })

  function world() {
    const [a1, a2, a3, b1, b2] = [1, 2, 3, 4, 5].map(() => createTab({ kind: 'new-tab' }))
    const wsA = useWorkspaceStore.getState().addWorkspace('A')
    const wsB = useWorkspaceStore.getState().addWorkspace('B')
    // Interleaved in tabOrder, so "right of a1" in tabOrder reaches into B.
    for (const t of [a1, b1, a2, b2, a3]) useTabStore.getState().addTab(t)
    for (const t of [a1, a2, a3]) useWorkspaceStore.getState().addTabToWorkspace(wsA.id, t.id)
    for (const t of [b1, b2]) useWorkspaceStore.getState().addTabToWorkspace(wsB.id, t.id)
    useTabStore.getState().setActiveTab(a1.id)
    useWorkspaceStore.getState().setActiveWorkspace(null)
    return { a1, a2, a3, b1, b2 }
  }

  function act_(action: 'closeOthers' | 'closeRight', tabId: string) {
    const tabState = useTabStore.getState()
    const ws = useWorkspaceStore.getState()
    const displayTabs = getVisibleTabIds({
      tabs: tabState.tabs, tabOrder: tabState.tabOrder, activeTabId: tabState.activeTabId,
      workspaces: ws.workspaces, activeWorkspaceId: ws.activeWorkspaceId,
    }).map((id) => tabState.tabs[id])
    const { result } = renderHook(() => useTabWorkspaceActions(displayTabs))
    act(() => { result.current.handleContextMenu({ preventDefault() {}, clientX: 0, clientY: 0 } as unknown as React.MouseEvent, tabId) })
    act(() => { result.current.handleContextAction(action) })
  }

  it('closeOthers on a1 closes a2 and a3 — and nothing of workspace B', () => {
    const { a1, b1, b2 } = world()
    act_('closeOthers', a1.id)
    expect(Object.keys(useTabStore.getState().tabs).sort()).toEqual([a1.id, b1.id, b2.id].sort())
  })

  it('closeRight of a1 closes a2 and a3 — and nothing of workspace B', () => {
    const { a1, b1, b2 } = world()
    act_('closeRight', a1.id)
    expect(Object.keys(useTabStore.getState().tabs).sort()).toEqual([a1.id, b1.id, b2.id].sort())
  })
})

// #1816 — the Mac App loads the SPA from the dev server, so its Electron preload can be older than this code:
// `window.electronAPI` is there but `tearOffTab` is not. The handler closed the tab and then called the missing
// IPC, so the tab was lost and the menu action threw. The IPC is now checked before anything is touched: nothing
// changes, and a toast says why. With the IPC there, tear-off is what it always was.
describe('tearOff and a preload without tearOffTab (#1816)', () => {
  const UNSUPPORTED = "This version of the Purdex App can't move a tab to a new window yet. Update the App."

  beforeEach(() => {
    useWorkspaceStore.getState().reset()
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
    useHistoryStore.setState({ browseHistory: [], closedTabs: [] })
    useUndoToast.setState({ toast: null, notice: null })
    vi.mocked(closeTab).mockClear()
  })

  afterEach(() => {
    delete window.electronAPI
  })

  function setApi(api: Record<string, unknown>) {
    window.electronAPI = api as unknown as Window['electronAPI']
  }

  /** An active browser tab (a close would destroy its BrowserView) in the active workspace. */
  function world() {
    const tab = createTab({ kind: 'browser', url: 'https://example.com' })
    useTabStore.getState().addTab(tab)
    useTabStore.getState().setActiveTab(tab.id)
    const ws = useWorkspaceStore.getState().addWorkspace('A')
    useWorkspaceStore.getState().addTabToWorkspace(ws.id, tab.id)
    useWorkspaceStore.getState().setActiveWorkspace(ws.id)
    return { tab, wsId: ws.id }
  }

  function snapshot() {
    const { tabs, tabOrder, activeTabId } = useTabStore.getState()
    const { workspaces, activeWorkspaceId } = useWorkspaceStore.getState()
    return { tabs, tabOrder, activeTabId, workspaces, activeWorkspaceId, closedTabs: useHistoryStore.getState().closedTabs }
  }

  function tearOff(tabId: string) {
    const { result } = renderHook(() => useTabWorkspaceActions(Object.values(useTabStore.getState().tabs)))
    act(() => { result.current.handleContextMenu({ preventDefault() {}, clientX: 0, clientY: 0 } as unknown as React.MouseEvent, tabId) })
    act(() => { result.current.handleContextAction('tearOff') })
  }

  it('an electronAPI without tearOffTab: no close, no BrowserView destroyed, no store change, no throw — a toast says why', () => {
    const destroyBrowserView = vi.fn()
    setApi({ destroyBrowserView })
    const { tab, wsId } = world()
    const before = snapshot()

    expect(() => tearOff(tab.id)).not.toThrow()

    expect(vi.mocked(closeTab)).not.toHaveBeenCalled()
    expect(destroyBrowserView).not.toHaveBeenCalled()
    expect(snapshot()).toEqual(before)
    expect(useTabStore.getState().tabs[tab.id]).toBeDefined()
    expect(useWorkspaceStore.getState().workspaces.find((w) => w.id === wsId)?.tabs).toContain(tab.id)
    expect(useUndoToast.getState().toast?.message).toBe(UNSUPPORTED)
  })

  it('no electronAPI at all: the same — nothing changes, and the toast says why', () => {
    const { tab, wsId } = world()
    const before = snapshot()

    expect(() => tearOff(tab.id)).not.toThrow()

    expect(vi.mocked(closeTab)).not.toHaveBeenCalled()
    expect(snapshot()).toEqual(before)
    expect(useWorkspaceStore.getState().workspaces.find((w) => w.id === wsId)?.tabs).toContain(tab.id)
    expect(useUndoToast.getState().toast?.message).toBe(UNSUPPORTED)
  })

  it('an electronAPI with tearOffTab: unchanged — the tab leaves this window first, then is sent once; no toast', () => {
    let goneWhenSent: boolean | undefined
    const tearOffTab = vi.fn((json: string) => {
      goneWhenSent = useTabStore.getState().tabs[(JSON.parse(json) as Tab).id] === undefined
      return Promise.resolve()
    })
    const destroyBrowserView = vi.fn()
    setApi({ tearOffTab, destroyBrowserView })
    const { tab, wsId } = world()

    tearOff(tab.id)

    expect(vi.mocked(closeTab)).toHaveBeenCalledTimes(1)
    expect(tearOffTab).toHaveBeenCalledTimes(1)
    expect((JSON.parse(tearOffTab.mock.calls[0][0]) as Tab).id).toBe(tab.id)
    expect(goneWhenSent).toBe(true)
    expect(destroyBrowserView).toHaveBeenCalledTimes(1)
    expect(useTabStore.getState().tabs[tab.id]).toBeUndefined()
    expect(useWorkspaceStore.getState().workspaces.find((w) => w.id === wsId)?.tabs).not.toContain(tab.id)
    expect(useUndoToast.getState().toast).toBeNull()
    expect(isTabMoving(tab.id)).toBe(true) // #2140: a move, so the team lifecycle does not take it for a close
  })
})

describe('handleContextMenu — rows another surface left on the tab menu', () => {
  it('a tab menu opened from anywhere starts with none: the task-group view sets its own after this runs', () => {
    const tab = createTab({ kind: 'new-tab' })
    useTabStore.getState().addTab(tab)
    useTabMenuExtrasStore.getState().set(tab.id, [{ key: 'stale', label: 'Stale', onSelect: () => {} }])
    const { result } = renderHook(() => useTabWorkspaceActions([tab]))
    act(() => { result.current.handleContextMenu({ preventDefault() {}, clientX: 0, clientY: 0 } as unknown as React.MouseEvent, tab.id) })
    expect(useTabMenuExtrasStore.getState().items).toEqual([])
  })
})

// TG-4a — in the task-group view the range is the selected group's tabs (lib/task-groups/range.ts), across workspaces:
// close-others / close-right act on exactly that list.
describe('closeOthers / closeRight on a task-group range', () => {
  beforeEach(() => {
    useWorkspaceStore.getState().reset()
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
  })

  function world() {
    const [g1, g2, g3, x1] = [1, 2, 3, 4].map(() => createTab({ kind: 'new-tab' }))
    const wsA = useWorkspaceStore.getState().addWorkspace('A')
    const wsB = useWorkspaceStore.getState().addWorkspace('B')
    for (const t of [g1, x1, g2, g3]) useTabStore.getState().addTab(t)
    for (const t of [g1, x1]) useWorkspaceStore.getState().addTabToWorkspace(wsA.id, t.id)
    for (const t of [g2, g3]) useWorkspaceStore.getState().addTabToWorkspace(wsB.id, t.id)
    useTabStore.getState().setActiveTab(g1.id)
    useWorkspaceStore.getState().setActiveWorkspace(wsA.id)
    return { g1, g2, g3, x1 }
  }

  function act_(action: 'closeOthers' | 'closeRight', tabId: string, groupRange: string[]) {
    const tabState = useTabStore.getState()
    const ws = useWorkspaceStore.getState()
    const displayTabs = getVisibleTabIds({
      tabs: tabState.tabs, tabOrder: tabState.tabOrder, activeTabId: tabState.activeTabId,
      workspaces: ws.workspaces, activeWorkspaceId: ws.activeWorkspaceId, groupRange,
    }).map((id) => tabState.tabs[id])
    const { result } = renderHook(() => useTabWorkspaceActions(displayTabs))
    act(() => { result.current.handleContextMenu({ preventDefault() {}, clientX: 0, clientY: 0 } as unknown as React.MouseEvent, tabId) })
    act(() => { result.current.handleContextAction(action) })
  }

  it('closeOthers on g1 closes the group\'s other tabs (one in another workspace) and not the workspace tab outside the group', () => {
    const { g1, g2, g3, x1 } = world()
    act_('closeOthers', g1.id, [g1.id, g2.id, g3.id])
    expect(Object.keys(useTabStore.getState().tabs).sort()).toEqual([g1.id, x1.id].sort())
  })

  it('closeRight of g2 closes what is right of it in the group\'s order, not in tabOrder or the workspace', () => {
    const { g1, g2, g3, x1 } = world()
    act_('closeRight', g2.id, [g1.id, g2.id, g3.id])
    expect(Object.keys(useTabStore.getState().tabs).sort()).toEqual([g1.id, g2.id, x1.id].sort())
  })
})

describe('handleCloseTab in the task-group view (TG-4a)', () => {
  it('the tab shown next is the group\'s neighbour, in whichever workspace, not a tab of the closed tab\'s workspace outside the group', async () => {
    const { useTeamUiStore } = await import('../../stores/useTeamUiStore')
    const { useLayoutStore } = await import('../../stores/useLayoutStore')
    const { useManualGroupsStore } = await import('../../stores/useManualGroupsStore')
    const { useGroupViewStore } = await import('../../stores/useGroupViewStore')
    const { manualGroupKey } = await import('../../lib/task-groups/group-key')
    const G = manualGroupKey('aaaaaaaa')
    useWorkspaceStore.getState().reset()
    const [g1, g2, x] = [1, 2, 3].map(() => ({ ...createTab({ kind: 'new-tab' }) }))
    g1.group = G; g1.groupRank = 'A'
    g2.group = G; g2.groupRank = 'M'
    useTabStore.setState({ tabs: { [g1.id]: g1, [g2.id]: g2, [x.id]: x }, tabOrder: [g1.id, x.id, g2.id], activeTabId: g1.id, visitHistory: [] } as never)
    const wsA = useWorkspaceStore.getState().addWorkspace('A')
    const wsB = useWorkspaceStore.getState().addWorkspace('B')
    for (const t of [g1, x]) useWorkspaceStore.getState().addTabToWorkspace(wsA.id, t.id)
    useWorkspaceStore.getState().addTabToWorkspace(wsB.id, g2.id)
    useWorkspaceStore.getState().setActiveWorkspace(wsA.id)
    useManualGroupsStore.setState({ order: ['aaaaaaaa'], groups: { aaaaaaaa: { name: 'G', members: [], createdAt: 1 } } })
    useGroupViewStore.setState({ selectedGroup: G, lastActiveTab: {}, expanded: {}, lastByView: { workspace: null, group: null } })
    useTeamUiStore.setState({ sidebarView: 'groups' })
    useLayoutStore.setState({ activityBarWidth: 'wide', tabPosition: 'top' })
    const { result } = renderHook(() => useTabWorkspaceActions([g1, g2].map((t) => useTabStore.getState().tabs[t.id])))
    act(() => { result.current.handleCloseTab(g1.id) })
    expect(useTabStore.getState().activeTabId).toBe(g2.id)
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBe(wsB.id)
    useTeamUiStore.setState({ sidebarView: 'workspace' })
  })
})
