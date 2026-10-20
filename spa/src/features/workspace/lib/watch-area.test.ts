import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { useWorkspaceStore, WATCH_WORKSPACE_ID } from '../store'
import { useTabStore } from '../../../stores/useTabStore'
import { usePoolStore } from '../../../lib/pool/store'
import { usePoolHostsStore } from '../../../stores/usePoolHostsStore'
import { createTab } from '../../../types/tab'
import { WATCH_GUARDIAN_TAB_ID, watchHostTabId } from '../../../lib/watch/ids'
import { buildTabsSection } from '../../../lib/profile/sections'
import { applyTabs } from '../../../lib/profile/applier'
import { openWatchArea } from './open-watch-area'
import { ABSENT_REMOVE_MS, createWatchReconciler, startWatchArea, leaveAreaForForeignActiveTab, readPoolView } from './watch-area'

const ws = () => useWorkspaceStore.getState()
// The host tabs of the area: the guardian tab is made by every pass and is covered by watch-area.guardian.test.ts.
const watchTabs = () => (ws().workspaces.find((w) => w.id === WATCH_WORKSPACE_ID)?.tabs ?? []).filter((id) => id !== WATCH_GUARDIAN_TAB_ID)
const view = (ids: string[]) => ({ hostIds: ids })
const T0 = 1_000_000

function setPool(ids: string[]): void {
  usePoolStore.setState({ hosts: ids.map((id) => ({ host_id: id, alias: id })) as never, sourceHostId: 'cfg-a' } as never)
}

beforeEach(() => {
  ws().reset()
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
  usePoolStore.setState({ hosts: [], sourceHostId: null } as never)
  usePoolHostsStore.setState({ order: [], looks: {} } as never)
  ws().addWorkspace('A')
  ws().ensureWatchWorkspace()
})

describe('reconcileWatchTabs', () => {
  it('creates a locked tab per host, appended, with a fixed id and pane id', () => {
    createWatchReconciler().reconcile(view(['h1', 'h2']), T0)
    expect(watchTabs()).toEqual([watchHostTabId('h1'), watchHostTabId('h2')])
    const tab = useTabStore.getState().tabs[watchHostTabId('h1')]
    expect(tab.locked).toBe(true)
    expect(tab.layout).toEqual({ type: 'leaf', pane: { id: `${watchHostTabId('h1')}.p`, content: { kind: 'watch-host', poolHostId: 'h1' } } })
  })

  it('is idempotent: a second pass writes nothing', () => {
    const r = createWatchReconciler()
    r.reconcile(view(['h1']), T0)
    const before = [ws().workspaces, useTabStore.getState().tabs]
    r.reconcile(view(['h1']), T0 + 1000)
    expect([ws().workspaces, useTabStore.getState().tabs]).toEqual(before)
    expect(ws().workspaces).toBe(before[0])
  })

  it('the order is the pool hosts store\'s: a row order that was not written there gives way to it (a drag writes it, MON-6b)', () => {
    const r = createWatchReconciler()
    r.reconcile(view(['a', 'b', 'c']), T0)
    ws().reorderWorkspaceTabs(WATCH_WORKSPACE_ID, [watchHostTabId('c'), watchHostTabId('a'), watchHostTabId('b')])
    r.reconcile(view(['a', 'b', 'c', 'd']), T0 + 1000)
    expect(watchTabs()).toEqual(['a', 'b', 'c', 'd'].map(watchHostTabId))
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

  it('a pool that cannot be read (no view) removes nothing, however long', () => {
    const r = createWatchReconciler()
    r.reconcile(view(['a', 'b']), T0)
    r.reconcile(null, T0 + 10 * ABSENT_REMOVE_MS)
    expect(watchTabs()).toContain(watchHostTabId('b'))
  })

  describe('tabs from before the pool (content without poolHostId)', () => {
    const oldTab = (id: string) => useTabStore.getState().addTab({ id: watchHostTabId(id), pinned: false, locked: true, createdAt: 1, layout: { type: 'leaf', pane: { id: `${watchHostTabId(id)}.p`, content: { kind: 'watch-host', fleetHostId: id } } } })
    const putInArea = (id: string) => ws().addTabToWorkspace(WATCH_WORKSPACE_ID, watchHostTabId(id))

    it('goes after ABSENT_REMOVE_MS even though no device ever listed its fleet id, and not before', () => {
      oldTab('fleet-x'); putInArea('fleet-x')
      const r = createWatchReconciler()
      expect(r.reconcile(view(['a']), T0)).toBe(T0 + ABSENT_REMOVE_MS)
      expect(watchTabs()).toContain(watchHostTabId('fleet-x'))
      r.reconcile(view(['a']), T0 + ABSENT_REMOVE_MS - 1)
      expect(watchTabs()).toContain(watchHostTabId('fleet-x'))
      r.reconcile(view(['a']), T0 + ABSENT_REMOVE_MS)
      expect(watchTabs()).toEqual([watchHostTabId('a')])
    })
    it('one that syncs back in later is collected again, one minute after it is seen', () => {
      const r = createWatchReconciler()
      oldTab('fleet-x'); putInArea('fleet-x')
      r.reconcile(view(['a']), T0)
      r.reconcile(view(['a']), T0 + ABSENT_REMOVE_MS)
      expect(watchTabs()).toEqual([watchHostTabId('a')])
      oldTab('fleet-x'); putInArea('fleet-x') // a device that has not upgraded sends it back
      r.reconcile(view(['a']), T0 + ABSENT_REMOVE_MS + 5000)
      expect(watchTabs()).toContain(watchHostTabId('fleet-x'))
      r.reconcile(view(['a']), T0 + 2 * ABSENT_REMOVE_MS + 5000)
      expect(watchTabs()).toEqual([watchHostTabId('a')])
    })
    it('with no view (pool unreadable) it is not touched', () => {
      oldTab('fleet-x'); putInArea('fleet-x')
      const r = createWatchReconciler()
      r.reconcile(null, T0)
      r.reconcile(null, T0 + 10 * ABSENT_REMOVE_MS)
      expect(watchTabs()).toContain(watchHostTabId('fleet-x'))
    })
    it('an old tab whose id is a listed pool host is made a pool tab, not collected', () => {
      oldTab('a'); putInArea('a')
      const r = createWatchReconciler()
      r.reconcile(view(['a']), T0)
      r.reconcile(view(['a']), T0 + 10 * ABSENT_REMOVE_MS)
      const tab = useTabStore.getState().tabs[watchHostTabId('a')]
      expect(tab.layout.type === 'leaf' && tab.layout.pane.content).toEqual({ kind: 'watch-host', poolHostId: 'a' })
    })
  })

  it('a switch of the source that answers is not a host leaving: what the old source listed and the new one does not stays', () => {
    const r = createWatchReconciler()
    r.reconcile({ hostIds: ['a', 'b'], source: 'cfg-x' }, T0)
    r.reconcile({ hostIds: ['a'], source: 'cfg-y' }, T0 + 1000)
    r.reconcile({ hostIds: ['a'], source: 'cfg-y' }, T0 + 10 * ABSENT_REMOVE_MS)
    expect(watchTabs()).toContain(watchHostTabId('b'))
  })

  it('the same source dropping a host still removes it after the minute', () => {
    const r = createWatchReconciler()
    r.reconcile({ hostIds: ['a', 'b'], source: 'cfg-x' }, T0)
    r.reconcile({ hostIds: ['a'], source: 'cfg-x' }, T0 + 1000)
    r.reconcile({ hostIds: ['a'], source: 'cfg-x' }, T0 + ABSENT_REMOVE_MS)
    expect(watchTabs()).toEqual([watchHostTabId('a')])
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

describe('host tabs follow the order of the view (MON-6b, M13)', () => {
  const areaTabs = () => ws().workspaces.find((w) => w.id === WATCH_WORKSPACE_ID)!.tabs
  const host = (...ids: string[]) => ids.map(watchHostTabId)

  it('the host tabs take the places host tabs held, in the order of the view', () => {
    const r = createWatchReconciler()
    r.reconcile(view(['mserv', 'mlab']), T0)
    expect(areaTabs()).toEqual([WATCH_GUARDIAN_TAB_ID, ...host('mserv', 'mlab')])
    r.reconcile(view(['mlab', 'mserv']), T0 + 1000)
    expect(areaTabs()).toEqual([WATCH_GUARDIAN_TAB_ID, ...host('mlab', 'mserv')])
  })

  it('a tab that is not a host tab in the view keeps its place: the reader\'s guardian between two hosts stays there', () => {
    const r = createWatchReconciler()
    r.reconcile(view(['mserv', 'mlab']), T0)
    ws().reorderWorkspaceTabs(WATCH_WORKSPACE_ID, [...host('mserv'), WATCH_GUARDIAN_TAB_ID, ...host('mlab')])
    r.reconcile(view(['mserv', 'mlab']), T0 + 1000)
    expect(areaTabs()).toEqual([...host('mserv'), WATCH_GUARDIAN_TAB_ID, ...host('mlab')])
    r.reconcile(view(['mlab', 'mserv']), T0 + 2000)
    expect(areaTabs()).toEqual([...host('mlab'), WATCH_GUARDIAN_TAB_ID, ...host('mserv')])
  })

  it('a host tab the view does not list (waiting to be collected, or synced in) keeps its place', () => {
    const r = createWatchReconciler()
    r.reconcile(view(['a', 'b', 'c']), T0)
    r.reconcile(view(['c', 'a']), T0 + 1000) // b left the answer: its tab waits a minute
    expect(areaTabs()).toEqual([WATCH_GUARDIAN_TAB_ID, ...host('c', 'b', 'a')])
  })

  it('already in order: nothing is written', () => {
    const r = createWatchReconciler()
    r.reconcile(view(['a', 'b']), T0)
    const before = ws().workspaces
    r.reconcile(view(['a', 'b']), T0 + 1000)
    expect(ws().workspaces).toBe(before)
  })

  it('a new host is placed where the view has it', () => {
    const r = createWatchReconciler()
    r.reconcile(view(['a', 'b']), T0)
    r.reconcile(view(['a', 'c', 'b']), T0 + 1000)
    expect(areaTabs()).toEqual([WATCH_GUARDIAN_TAB_ID, ...host('a', 'c', 'b')])
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
    setPool(['h1'])
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
    setPool(['h1'])
    const stop = startWatchArea()
    openWatchArea({ poolHost: { hostId: 'h1' } })
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
    setPool(['a', 'b'])
    const stop = startWatchArea()
    expect(watchTabs()).toEqual(['a', 'b'].map(watchHostTabId))
    vi.setSystemTime(T0 + 1000)
    setPool(['a'])
    expect(watchTabs()).toHaveLength(2)
    vi.advanceTimersByTime(ABSENT_REMOVE_MS)
    expect(watchTabs()).toEqual([watchHostTabId('a')])
    stop()
  })

  it('does nothing when the area is not there (an apply removed it): no workspace is recreated', () => {
    useWorkspaceStore.setState({ workspaces: ws().workspaces.filter((w) => w.id !== WATCH_WORKSPACE_ID) })
    setPool(['a'])
    const stop = startWatchArea()
    expect(ws().workspaces.some((w) => w.id === WATCH_WORKSPACE_ID)).toBe(false)
    expect(Object.keys(useTabStore.getState().tabs)).toEqual([])
    stop()
  })
})

describe('readPoolView', () => {
  it('is null while no source has answered (the pool cannot be read): host tabs are then not touched', () => {
    usePoolStore.setState({ hosts: [{ host_id: 'a', alias: 'a' }] as never, sourceHostId: null } as never)
    expect(readPoolView()).toBeNull()
  })
  it('lists the pool hosts in the order of the pool hosts store, the unlisted after them', () => {
    setPool(['a', 'b', 'c'])
    usePoolHostsStore.setState({ order: ['c', 'a'] } as never)
    expect(readPoolView()).toEqual({ hostIds: ['c', 'a', 'b'], source: 'cfg-a' })
  })
})
