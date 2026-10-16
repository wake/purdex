import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { useWorkspaceStore, WATCH_WORKSPACE_ID } from '../store'
import { useTabStore } from '../../../stores/useTabStore'
import { useFleetStore, type FleetSnapshot } from '../../../lib/fleet/store'
import { createTab } from '../../../types/tab'
import { WATCH_GUARDIAN_TAB_ID, watchHostTabId } from '../../../lib/watch/ids'
import { buildTabsSection } from '../../../lib/profile/sections'
import { applyTabs } from '../../../lib/profile/applier'
import { openWatchArea } from './open-watch-area'
import { ABSENT_REMOVE_MS, createWatchReconciler, startWatchArea, leaveAreaForForeignActiveTab } from './watch-area'

const ws = () => useWorkspaceStore.getState()
// The host tabs of the area: the guardian tab is made by every pass and is covered by watch-area.guardian.test.ts.
const watchTabs = () => (ws().workspaces.find((w) => w.id === WATCH_WORKSPACE_ID)?.tabs ?? []).filter((id) => id !== WATCH_GUARDIAN_TAB_ID)
const view = (ids: string[], stale = false) => ({ hostIds: ids, stale })
const T0 = 1_000_000

function setFleet(ids: string[], stale = false): void {
  const snapshot: FleetSnapshot = {
    hostId: 'p', term: 1, order: ids,
    hosts: Object.fromEntries(ids.map((id) => [id, { id, alias: id } as never])),
  }
  useFleetStore.setState({ snapshot, primaryHostId: 'p', stale })
}

beforeEach(() => {
  ws().reset()
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
  useFleetStore.getState().reset()
  ws().addWorkspace('A')
  ws().ensureWatchWorkspace()
})

describe('reconcileWatchTabs', () => {
  it('creates a locked tab per host, appended, with a fixed id and pane id', () => {
    createWatchReconciler().reconcile(view(['h1', 'h2']), T0)
    expect(watchTabs()).toEqual([watchHostTabId('h1'), watchHostTabId('h2')])
    const tab = useTabStore.getState().tabs[watchHostTabId('h1')]
    expect(tab.locked).toBe(true)
    expect(tab.layout).toEqual({ type: 'leaf', pane: { id: `${watchHostTabId('h1')}.p`, content: { kind: 'watch-host', fleetHostId: 'h1' } } })
  })

  it('is idempotent: a second pass writes nothing', () => {
    const r = createWatchReconciler()
    r.reconcile(view(['h1']), T0)
    const before = [ws().workspaces, useTabStore.getState().tabs]
    r.reconcile(view(['h1']), T0 + 1000)
    expect([ws().workspaces, useTabStore.getState().tabs]).toEqual(before)
    expect(ws().workspaces).toBe(before[0])
  })

  it('keeps the user order; a new host goes at the end', () => {
    const r = createWatchReconciler()
    r.reconcile(view(['a', 'b', 'c']), T0)
    ws().reorderWorkspaceTabs(WATCH_WORKSPACE_ID, [watchHostTabId('c'), watchHostTabId('a'), watchHostTabId('b')])
    r.reconcile(view(['a', 'b', 'c', 'd']), T0 + 1000)
    expect(watchTabs()).toEqual(['c', 'a', 'b', 'd'].map(watchHostTabId))
  })

  it('removes a host this device saw leave, only after ABSENT_REMOVE_MS', () => {
    const r = createWatchReconciler()
    r.reconcile(view(['a', 'b']), T0)
    expect(r.reconcile(view(['a']), T0 + 1000)).toBe(T0 + ABSENT_REMOVE_MS)
    expect(watchTabs()).toContain(watchHostTabId('b'))
    expect(r.reconcile(view(['a']), T0 + ABSENT_REMOVE_MS - 1)).toBe(T0 + ABSENT_REMOVE_MS)
    expect(watchTabs()).toContain(watchHostTabId('b'))
    r.reconcile(view(['a']), T0 + ABSENT_REMOVE_MS)
    expect(watchTabs()).toEqual([watchHostTabId('a')])
    expect(watchHostTabId('b') in useTabStore.getState().tabs).toBe(false)
  })

  it('removes nothing while the snapshot is stale', () => {
    const r = createWatchReconciler()
    r.reconcile(view(['a', 'b']), T0)
    r.reconcile(view(['a'], true), T0 + 10 * ABSENT_REMOVE_MS)
    expect(watchTabs()).toContain(watchHostTabId('b'))
  })

  it('a synced tab for a host this device never listed stays', () => {
    const r = createWatchReconciler()
    r.reconcile(view(['a', 'b']), T0)
    const other = createWatchReconciler() // a device that never saw b
    other.reconcile(view(['a']), T0 + 10 * ABSENT_REMOVE_MS)
    expect(watchTabs()).toContain(watchHostTabId('b'))
  })

  it('without a fleet view the host tabs are left as they are', () => {
    const r = createWatchReconciler()
    r.reconcile(view(['a', 'b']), T0)
    r.reconcile(null, T0 + 10 * ABSENT_REMOVE_MS)
    expect(watchTabs()).toHaveLength(2)
  })

  it('moves a watch tab found elsewhere back, and a non-watch tab found in the area out', () => {
    const r = createWatchReconciler()
    r.reconcile(view(['a']), T0)
    const aId = ws().workspaces.find((w) => w.id !== WATCH_WORKSPACE_ID)!.id
    // a rehydrate / repair put the watch tab in A, and an ordinary tab in watch
    ws().removeTabFromWorkspace(WATCH_WORKSPACE_ID, watchHostTabId('a'))
    ws().addTabToWorkspace(aId, watchHostTabId('a'))
    const stray = createTab({ kind: 'new-tab' })
    useTabStore.getState().addTab(stray)
    ws().addTabToWorkspace(WATCH_WORKSPACE_ID, stray.id)
    r.reconcile(view(['a']), T0 + 1000)
    expect(watchTabs()).toEqual([watchHostTabId('a')])
    expect(ws().workspaces.find((w) => w.id === aId)!.tabs).toEqual([stray.id])
  })

  it('with the area removed, stray watch tabs are dropped and nothing is recreated', () => {
    const r = createWatchReconciler()
    r.reconcile(view(['a']), T0)
    const aId = ws().workspaces.find((w) => w.id !== WATCH_WORKSPACE_ID)!.id
    ws().removeTabFromWorkspace(WATCH_WORKSPACE_ID, watchHostTabId('a'))
    ws().addTabToWorkspace(aId, watchHostTabId('a'))
    useWorkspaceStore.setState({ workspaces: ws().workspaces.filter((w) => w.id !== WATCH_WORKSPACE_ID) })
    r.reconcile(view(['a']), T0 + 1000)
    expect(ws().workspaces.some((w) => w.id === WATCH_WORKSPACE_ID)).toBe(false)
    expect(watchHostTabId('a') in useTabStore.getState().tabs).toBe(false)
  })
})

describe('Profile Sync round trip', () => {
  it('a tabs.watch payload keeps order and locked through applyTabs', () => {
    createWatchReconciler().reconcile(view(['a', 'b', 'c']), T0)
    ws().reorderWorkspaceTabs(WATCH_WORKSPACE_ID, [WATCH_GUARDIAN_TAB_ID, ...['c', 'a', 'b'].map(watchHostTabId)])
    const watch = ws().workspaces.find((w) => w.id === WATCH_WORKSPACE_ID)!
    const payload = buildTabsSection(watch, useTabStore.getState().tabs)
    expect(payload.order).toEqual([WATCH_GUARDIAN_TAB_ID, ...['c', 'a', 'b'].map(watchHostTabId)])
    const local = { tabs: {}, workspaces: [{ ...watch, tabs: [], activeTabId: null }] }
    const out = applyTabs(local, WATCH_WORKSPACE_ID, payload)
    expect(out.next.workspaces[0].tabs).toEqual([WATCH_GUARDIAN_TAB_ID, ...['c', 'a', 'b'].map(watchHostTabId)])
    for (const id of out.next.workspaces[0].tabs) expect(out.next.tabs[id].locked).toBe(true)
  })

  it('two devices whose snapshots differ by one host converge with no ping-pong', () => {
    const snap = () => ({ workspaces: ws().workspaces, tabs: useTabStore.getState().tabs })
    const load = (s: ReturnType<typeof snap>) => {
      useWorkspaceStore.setState({ workspaces: s.workspaces })
      useTabStore.setState({ tabs: s.tabs, tabOrder: Object.keys(s.tabs) })
    }
    const rA = createWatchReconciler()
    const rB = createWatchReconciler()
    const base = snap()
    rA.reconcile(view(['a', 'b']), T0)
    let A = snap()
    load(base)
    rB.reconcile(view(['a', 'c']), T0)
    let B = snap()

    const send = (from: ReturnType<typeof snap>, to: ReturnType<typeof snap>, r: ReturnType<typeof createWatchReconciler>, ids: string[]) => {
      const watch = from.workspaces.find((w) => w.id === WATCH_WORKSPACE_ID)!
      const payload = buildTabsSection(watch, from.tabs)
      load(to)
      const out = applyTabs({ tabs: useTabStore.getState().tabs, workspaces: ws().workspaces }, WATCH_WORKSPACE_ID, payload)
      load({ workspaces: out.next.workspaces, tabs: out.next.tabs })
      r.reconcile(view(ids), T0 + 1000)
      return snap()
    }

    const orderOf = (s: ReturnType<typeof snap>) => s.workspaces.find((w) => w.id === WATCH_WORKSPACE_ID)!.tabs
    const seen: string[] = []
    for (let round = 0; round < 4; round++) {
      B = send(A, B, rB, ['a', 'c'])
      A = send(B, A, rA, ['a', 'b'])
      seen.push(JSON.stringify([orderOf(A), orderOf(B)]))
    }
    expect(seen[2]).toBe(seen[3]) // stable
    expect(orderOf(A)).toEqual(orderOf(B))
    expect(new Set(orderOf(A))).toEqual(new Set([WATCH_GUARDIAN_TAB_ID, ...['a', 'b', 'c'].map(watchHostTabId)]))
  })
})

describe('dropping the active tab of the area', () => {
  it('activates a neighbour in the area, not nothing', () => {
    const r = createWatchReconciler()
    r.reconcile(view(['a', 'b', 'c']), T0)
    useTabStore.getState().setActiveTab(watchHostTabId('b'))
    ws().setWorkspaceActiveTab(WATCH_WORKSPACE_ID, watchHostTabId('b'))
    r.reconcile(view(['a', 'c']), T0 + 1000)
    r.reconcile(view(['a', 'c']), T0 + ABSENT_REMOVE_MS)
    expect(watchTabs()).toEqual([watchHostTabId('a'), watchHostTabId('c')])
    expect(useTabStore.getState().activeTabId).toBe(watchHostTabId('c'))
    expect(ws().workspaces.find((w) => w.id === WATCH_WORKSPACE_ID)!.activeTabId).toBe(watchHostTabId('c'))
  })
})

describe('through startWatchArea', () => {
  afterEach(() => vi.useRealTimers())

  it('sweeps the watch tabs adoption left in Unsorted after a sync apply removed the area', () => {
    createWatchReconciler().reconcile(view(['a']), T0)
    const aId = ws().workspaces.find((w) => w.id !== WATCH_WORKSPACE_ID)!.id
    ws().removeTabFromWorkspace(WATCH_WORKSPACE_ID, watchHostTabId('a'))
    ws().addTabToWorkspace(aId, watchHostTabId('a')) // where adoption puts it
    useWorkspaceStore.setState({ workspaces: ws().workspaces.filter((w) => w.id !== WATCH_WORKSPACE_ID) })
    const stop = startWatchArea()
    expect(watchHostTabId('a') in useTabStore.getState().tabs).toBe(false)
    expect(ws().workspaces.find((w) => w.id === aId)!.tabs).toEqual([])
    expect(ws().workspaces.some((w) => w.id === WATCH_WORKSPACE_ID)).toBe(false)
    stop()
  })

  it('opening a session while the area is shown leaves it for the return workspace', () => {
    const a = ws().workspaces.find((w) => w.id !== WATCH_WORKSPACE_ID)!.id
    setFleet(['h1'])
    const stop = startWatchArea()
    ws().setActiveWorkspace(a)
    ws().setActiveWorkspace(WATCH_WORKSPACE_ID)
    const session = createTab({ kind: 'new-tab' })
    useTabStore.getState().addTab(session)
    useTabStore.getState().setActiveTab(session.id)
    ws().insertTab(session.id) // the store sends it to the return workspace
    expect(ws().activeWorkspaceId).toBe(a)
    stop()
  })

  it('openWatchArea from a session workspace stays in the area: no loop, no bounce', () => {
    const a = ws().workspaces.find((w) => w.id !== WATCH_WORKSPACE_ID)!.id
    const session = createTab({ kind: 'new-tab' })
    useTabStore.getState().addTab(session)
    ws().insertTab(session.id, a)
    useTabStore.getState().setActiveTab(session.id)
    ws().setActiveWorkspace(a)
    setFleet(['h1'])
    const stop = startWatchArea()
    openWatchArea({ host: 'h1' })
    expect(ws().activeWorkspaceId).toBe(WATCH_WORKSPACE_ID)
    expect(useTabStore.getState().activeTabId).toBe(watchHostTabId('h1'))
    stop()
  })
})

describe('leaving the area', () => {
  it('a session tab that becomes active while the area is shown takes the user back to its workspace', () => {
    const a = ws().workspaces.find((w) => w.id !== WATCH_WORKSPACE_ID)!
    const tab = createTab({ kind: 'new-tab' })
    useTabStore.getState().addTab(tab)
    ws().setActiveWorkspace(WATCH_WORKSPACE_ID)
    ws().insertTab(tab.id) // redirected to the return workspace
    useTabStore.getState().setActiveTab(tab.id)
    leaveAreaForForeignActiveTab()
    expect(ws().activeWorkspaceId).toBe(a.id)
  })

  it('an area tab keeps the area', () => {
    createWatchReconciler().reconcile(view(['a']), T0)
    ws().setActiveWorkspace(WATCH_WORKSPACE_ID)
    useTabStore.getState().setActiveTab(watchHostTabId('a'))
    leaveAreaForForeignActiveTab()
    expect(ws().activeWorkspaceId).toBe(WATCH_WORKSPACE_ID)
  })
})

describe('startWatchArea', () => {
  afterEach(() => vi.useRealTimers())

  it('reconciles on fleet change while the area exists, and removes after the timer', () => {
    vi.useFakeTimers()
    vi.setSystemTime(T0)
    setFleet(['a', 'b'])
    const stop = startWatchArea()
    expect(watchTabs()).toEqual(['a', 'b'].map(watchHostTabId))
    vi.setSystemTime(T0 + 1000)
    setFleet(['a'])
    expect(watchTabs()).toHaveLength(2)
    vi.advanceTimersByTime(ABSENT_REMOVE_MS)
    expect(watchTabs()).toEqual([watchHostTabId('a')])
    stop()
  })

  it('does nothing when the area is not there (an apply removed it): no workspace is recreated', () => {
    useWorkspaceStore.setState({ workspaces: ws().workspaces.filter((w) => w.id !== WATCH_WORKSPACE_ID) })
    setFleet(['a'])
    const stop = startWatchArea()
    expect(ws().workspaces.some((w) => w.id === WATCH_WORKSPACE_ID)).toBe(false)
    expect(Object.keys(useTabStore.getState().tabs)).toEqual([])
    stop()
  })
})
