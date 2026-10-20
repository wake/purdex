import { describe, it, expect, beforeEach } from 'vitest'
import { useWorkspaceStore, WATCH_WORKSPACE_ID, __resetReturnWorkspaceForTest, getReturnWorkspaceId } from '../store'
import { useTabStore } from '../../../stores/useTabStore'
import { usePoolStore } from '../../../lib/pool/store'
import { usePoolHostsStore } from '../../../stores/usePoolHostsStore'
import { readHostPage, clearViewMemory } from '../../../lib/watch/view-memory'
import { createTab } from '../../../types/tab'
import { WATCH_GUARDIAN_TAB_ID as G, watchHostTabId } from '../../../lib/watch/ids'
import { getVisibleTabIds } from './getVisibleTabIds'
import { openWatchArea } from './open-watch-area'

const ws = () => useWorkspaceStore.getState()
const area = () => ws().workspaces.find((w) => w.id === WATCH_WORKSPACE_ID)

function setPool(ids: string[]): void {
  usePoolStore.setState({ hosts: ids.map((id) => ({ host_id: id, alias: id })) as never, sourceHostId: 'cfg-a' } as never)
}

let a: string
beforeEach(() => {
  ws().reset()
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
  usePoolStore.setState({ hosts: [], sourceHostId: null } as never)
  usePoolHostsStore.setState({ order: [], looks: {} } as never)
  clearViewMemory()
  __resetReturnWorkspaceForTest()
  a = ws().addWorkspace('A').id
  const session = createTab({ kind: 'new-tab' })
  useTabStore.getState().addTab(session)
  ws().insertTab(session.id, a)
  useTabStore.getState().setActiveTab(session.id)
  setPool(['h1', 'h2'])
})

describe('openWatchArea in a world that is not settled', () => {
  it('writes nothing', () => {
    useWorkspaceStore.setState({ worldEpoch: 7 }) // the tab and workspace stores now disagree: unsettled
    openWatchArea({ poolHost: { hostId: 'h1' } })
    expect(ws().workspaces.some((w) => w.id === WATCH_WORKSPACE_ID)).toBe(false)
    expect(ws().activeWorkspaceId).toBe(a)
    expect(Object.keys(useTabStore.getState().tabs).some((id) => id.startsWith('watch-'))).toBe(false)
    useWorkspaceStore.setState({ worldEpoch: 0 })
  })
})

describe('openWatchArea', () => {
  it('a pool host (the sidebar) selects its tab by the wwd host_id; the alias is not read', () => {
    openWatchArea({ poolHost: { hostId: 'h2', alias: 'whatever' } })
    expect(useTabStore.getState().activeTabId).toBe(watchHostTabId('h2'))
    expect(ws().activeWorkspaceId).toBe(WATCH_WORKSPACE_ID)
  })

  it('a pool host the pool does not list selects the area\'s first tab, like any unknown host', () => {
    openWatchArea({ poolHost: { hostId: 'ghost' } })
    expect(useTabStore.getState().activeTabId).toBe(G)
  })

  it('with sub, the host page to show is remembered under the host_id before the tab is selected', () => {
    openWatchArea({ poolHost: { hostId: 'h1' }, sub: 'settings' })
    expect(readHostPage('h1')).toBe('settings')
    expect(useTabStore.getState().activeTabId).toBe(watchHostTabId('h1'))
  })

  it('entering from a session workspace: creates the area and its tabs, selects the host tab, remembers where from', () => {
    openWatchArea({ poolHost: { hostId: 'h2' } })
    expect(area()!.tabs).toEqual([G, watchHostTabId('h1'), watchHostTabId('h2')])
    expect(useTabStore.getState().activeTabId).toBe(watchHostTabId('h2'))
    expect(ws().activeWorkspaceId).toBe(WATCH_WORKSPACE_ID)
    expect(area()!.activeTabId).toBe(watchHostTabId('h2'))
    expect(getReturnWorkspaceId()).toBe(a)
  })

  it('from inside the area: switches tab, return workspace unchanged', () => {
    openWatchArea({ poolHost: { hostId: 'h1' } })
    openWatchArea({ poolHost: { hostId: 'h2' } })
    expect(useTabStore.getState().activeTabId).toBe(watchHostTabId('h2'))
    expect(getReturnWorkspaceId()).toBe(a)
    expect(area()!.tabs).toHaveLength(3)
  })

  it('a host the pool lists but that has no tab yet gets one first', () => {
    openWatchArea({ poolHost: { hostId: 'h1' } })
    setPool(['h1', 'h2', 'h3'])
    openWatchArea({ poolHost: { hostId: 'h3' } })
    expect(area()!.tabs).toContain(watchHostTabId('h3'))
    expect(useTabStore.getState().activeTabId).toBe(watchHostTabId('h3'))
  })

  it('an unknown host id selects the first tab (the guardian)', () => {
    openWatchArea({ poolHost: { hostId: 'nope' } })
    expect(useTabStore.getState().activeTabId).toBe(G)
  })

  it('the guardian target selects the guardian tab', () => {
    openWatchArea({ tab: 'guardian' })
    expect(useTabStore.getState().activeTabId).toBe(G)
    expect(area()!.activeTabId).toBe(G)
  })

  it('a restart with the area active reopens it: the bar lists the area tabs', () => {
    openWatchArea({ poolHost: { hostId: 'h2' } })
    const { workspaces, activeWorkspaceId } = ws()
    const { tabs, tabOrder, activeTabId } = useTabStore.getState()
    expect(getVisibleTabIds({ tabs, tabOrder, activeTabId, workspaces, activeWorkspaceId }))
      .toEqual([G, watchHostTabId('h1'), watchHostTabId('h2')])
  })
})
