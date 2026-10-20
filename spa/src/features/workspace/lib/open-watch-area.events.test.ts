// W5e: openWatchArea on the events tab.
import { describe, it, expect, beforeEach } from 'vitest'
import { useWorkspaceStore, WATCH_WORKSPACE_ID, __resetReturnWorkspaceForTest } from '../store'
import { useTabStore } from '../../../stores/useTabStore'
import { usePoolStore } from '../../../lib/pool/store'
import { usePoolHostsStore } from '../../../stores/usePoolHostsStore'
import { useWatchStatusStore } from '../../../lib/watch/status-store'
import { createTab } from '../../../types/tab'
import { WATCH_EVENTS_TAB_ID as E, WATCH_GUARDIAN_TAB_ID as G, watchHostTabId } from '../../../lib/watch/ids'
import { openWatchArea } from './open-watch-area'

const ws = () => useWorkspaceStore.getState()
const area = () => ws().workspaces.find((w) => w.id === WATCH_WORKSPACE_ID)

function setPool(ids: string[]): void {
  usePoolStore.setState({ hosts: ids.map((id) => ({ host_id: id, alias: id })) as never, sourceHostId: 'cfg-a' } as never)
}

beforeEach(() => {
  ws().reset()
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
  usePoolStore.setState({ hosts: [], sourceHostId: null } as never)
  usePoolHostsStore.setState({ order: [], looks: {} } as never)
  useWatchStatusStore.getState().reset()
  __resetReturnWorkspaceForTest()
  const a = ws().addWorkspace('A').id
  const session = createTab({ kind: 'new-tab' })
  useTabStore.getState().addTab(session)
  ws().insertTab(session.id, a)
  useTabStore.getState().setActiveTab(session.id)
  setPool(['h1'])
})

describe('openWatchArea on the events tab', () => {
  it('selects the events tab once a host carries the watch module', () => {
    useWatchStatusStore.setState({ byHost: { x: { kind: 'ok', at: 1, status: { self: { host_id: 'd', state: 'none' }, enabled: true, auto_wake: true, mode: '', targets: [] } } } })
    openWatchArea({ tab: 'events' })
    expect(area()!.tabs).toEqual([G, E, watchHostTabId('h1')])
    expect(useTabStore.getState().activeTabId).toBe(E)
    expect(ws().activeWorkspaceId).toBe(WATCH_WORKSPACE_ID)
    expect(area()!.activeTabId).toBe(E)
  })
  it('falls back to the first tab where no events tab exists yet', () => {
    openWatchArea({ tab: 'events' })
    expect(area()!.tabs).toEqual([G, watchHostTabId('h1')])
    expect(useTabStore.getState().activeTabId).toBe(G)
  })
})
