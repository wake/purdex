import { describe, it, expect, beforeEach } from 'vitest'
import { useWorkspaceStore, WATCH_WORKSPACE_ID, __resetReturnWorkspaceForTest, getReturnWorkspaceId } from '../store'
import { useTabStore } from '../../../stores/useTabStore'
import { useFleetStore, type FleetSnapshot } from '../../../lib/fleet/store'
import { createTab } from '../../../types/tab'
import { WATCH_GUARDIAN_TAB_ID as G, watchHostTabId } from '../../../lib/watch/ids'
import { getVisibleTabIds } from './getVisibleTabIds'
import { openWatchArea } from './open-watch-area'

const ws = () => useWorkspaceStore.getState()
const area = () => ws().workspaces.find((w) => w.id === WATCH_WORKSPACE_ID)

function setFleet(ids: string[]): void {
  const snapshot: FleetSnapshot = {
    hostId: 'p', term: 1, order: ids,
    hosts: Object.fromEntries(ids.map((id) => [id, { id, alias: id } as never])),
  }
  useFleetStore.setState({ snapshot, primaryHostId: 'p', stale: false })
}

let a: string
beforeEach(() => {
  ws().reset()
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
  useFleetStore.getState().reset()
  __resetReturnWorkspaceForTest()
  a = ws().addWorkspace('A').id
  const session = createTab({ kind: 'new-tab' })
  useTabStore.getState().addTab(session)
  ws().insertTab(session.id, a)
  useTabStore.getState().setActiveTab(session.id)
  setFleet(['h1', 'h2'])
})

describe('openWatchArea in a world that is not settled', () => {
  it('writes nothing', () => {
    useWorkspaceStore.setState({ worldEpoch: 7 }) // the tab and workspace stores now disagree: unsettled
    openWatchArea({ host: 'h1' })
    expect(ws().workspaces.some((w) => w.id === WATCH_WORKSPACE_ID)).toBe(false)
    expect(ws().activeWorkspaceId).toBe(a)
    expect(Object.keys(useTabStore.getState().tabs).some((id) => id.startsWith('watch-'))).toBe(false)
    useWorkspaceStore.setState({ worldEpoch: 0 })
  })
})

describe('openWatchArea', () => {
  it('a pool host (the sidebar) is found among the fleet\'s hosts by alias and its tab selected (MON-1c bridge)', () => {
    useFleetStore.setState({ snapshot: { hostId: 'p', term: 1, order: ['h1', 'h2'], hosts: { h1: { id: 'h1', alias: 'air26' } as never, h2: { id: 'h2', alias: 'mlab' } as never } } })
    openWatchArea({ poolHost: { hostId: '82b11c521df16c12', alias: 'mlab' } })
    expect(useTabStore.getState().activeTabId).toBe(watchHostTabId('h2'))
    expect(ws().activeWorkspaceId).toBe(WATCH_WORKSPACE_ID)
  })

  it('a pool host no fleet host has the alias of selects the area\'s first tab, like an unknown host', () => {
    openWatchArea({ poolHost: { hostId: '82b11c521df16c12', alias: 'ghost' } })
    expect(useTabStore.getState().activeTabId).toBe(G)
    openWatchArea({ poolHost: { hostId: '82b11c521df16c12', alias: '' } })
    expect(useTabStore.getState().activeTabId).toBe(G)
  })

  it('entering from a session workspace: creates the area and its tabs, selects the host tab, remembers where from', () => {
    openWatchArea({ host: 'h2' })
    expect(area()!.tabs).toEqual([G, watchHostTabId('h1'), watchHostTabId('h2')])
    expect(useTabStore.getState().activeTabId).toBe(watchHostTabId('h2'))
    expect(ws().activeWorkspaceId).toBe(WATCH_WORKSPACE_ID)
    expect(area()!.activeTabId).toBe(watchHostTabId('h2'))
    expect(getReturnWorkspaceId()).toBe(a)
  })

  it('from inside the area: switches tab, return workspace unchanged', () => {
    openWatchArea({ host: 'h1' })
    openWatchArea({ host: 'h2' })
    expect(useTabStore.getState().activeTabId).toBe(watchHostTabId('h2'))
    expect(getReturnWorkspaceId()).toBe(a)
    expect(area()!.tabs).toHaveLength(3)
  })

  it('a host the fleet lists but that has no tab yet gets one first', () => {
    openWatchArea({ host: 'h1' })
    setFleet(['h1', 'h2', 'h3'])
    openWatchArea({ host: 'h3' })
    expect(area()!.tabs).toContain(watchHostTabId('h3'))
    expect(useTabStore.getState().activeTabId).toBe(watchHostTabId('h3'))
  })

  it('an unknown host id selects the first tab (the guardian)', () => {
    openWatchArea({ host: 'nope' })
    expect(useTabStore.getState().activeTabId).toBe(G)
  })

  it('the guardian target selects the guardian tab', () => {
    openWatchArea({ tab: 'guardian' })
    expect(useTabStore.getState().activeTabId).toBe(G)
    expect(area()!.activeTabId).toBe(G)
  })

  it('a restart with the area active reopens it: the bar lists the area tabs', () => {
    openWatchArea({ host: 'h2' })
    const { workspaces, activeWorkspaceId } = ws()
    const { tabs, tabOrder, activeTabId } = useTabStore.getState()
    expect(getVisibleTabIds({ tabs, tabOrder, activeTabId, workspaces, activeWorkspaceId }))
      .toEqual([G, watchHostTabId('h1'), watchHostTabId('h2')])
  })
})
