import { describe, it, expect, beforeEach } from 'vitest'
import { useWorkspaceStore, WATCH_WORKSPACE_ID, __resetReturnWorkspaceForTest, getReturnWorkspaceId } from '../store'
import { useTabStore } from '../../../stores/useTabStore'
import { useFleetStore, type FleetSnapshot } from '../../../lib/fleet/store'
import { createTab } from '../../../types/tab'
import { watchHostTabId } from '../../../lib/watch/ids'
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

describe('openWatchArea', () => {
  it('entering from a session workspace: creates the area and its tabs, selects the host tab, remembers where from', () => {
    openWatchArea({ host: 'h2' })
    expect(area()!.tabs).toEqual([watchHostTabId('h1'), watchHostTabId('h2')])
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
    expect(area()!.tabs).toHaveLength(2)
  })

  it('a host the fleet lists but that has no tab yet gets one first', () => {
    openWatchArea({ host: 'h1' })
    setFleet(['h1', 'h2', 'h3'])
    openWatchArea({ host: 'h3' })
    expect(area()!.tabs).toContain(watchHostTabId('h3'))
    expect(useTabStore.getState().activeTabId).toBe(watchHostTabId('h3'))
  })

  it('an unknown host id selects the first tab', () => {
    openWatchArea({ host: 'nope' })
    expect(useTabStore.getState().activeTabId).toBe(watchHostTabId('h1'))
  })

  it('the guardian target falls back to the first tab while no guardian tab exists (W5a3 adds it)', () => {
    openWatchArea({ tab: 'guardian' })
    expect(useTabStore.getState().activeTabId).toBe(watchHostTabId('h1'))
  })

  it('a restart with the area active reopens it: the bar lists the area tabs', () => {
    openWatchArea({ host: 'h2' })
    const { workspaces, activeWorkspaceId } = ws()
    const { tabs, tabOrder, activeTabId } = useTabStore.getState()
    expect(getVisibleTabIds({ tabs, tabOrder, activeTabId, workspaces, activeWorkspaceId }))
      .toEqual([watchHostTabId('h1'), watchHostTabId('h2')])
  })
})
