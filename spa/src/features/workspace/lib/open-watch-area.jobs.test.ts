// WWD-7b1-2a: openWatchArea on the jobs tab (the link the host tab's jobs section and the history page take).
import { describe, it, expect, beforeEach } from 'vitest'
import { useWorkspaceStore, WATCH_WORKSPACE_ID, __resetReturnWorkspaceForTest } from '../store'
import { useTabStore } from '../../../stores/useTabStore'
import { useHostStore } from '../../../stores/useHostStore'
import { useNexHostStore } from '../../../stores/useNexHostStore'
import { useFleetStore, type FleetSnapshot } from '../../../lib/fleet/store'
import { useWatchStatusStore } from '../../../lib/watch/status-store'
import { createTab } from '../../../types/tab'
import { WATCH_GUARDIAN_TAB_ID as G, WATCH_JOBS_TAB_ID as J, watchHostTabId } from '../../../lib/watch/ids'
import { openWatchArea } from './open-watch-area'

const ws = () => useWorkspaceStore.getState()
const area = () => ws().workspaces.find((w) => w.id === WATCH_WORKSPACE_ID)

beforeEach(() => {
  ws().reset()
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
  useFleetStore.getState().reset()
  useWatchStatusStore.getState().reset()
  useNexHostStore.setState({ byHost: {} } as never)
  useHostStore.setState({ hosts: { h1: { id: 'h1', name: 'mlab', ip: '1', port: 1, token: 't', order: 0 } }, hostOrder: ['h1'], activeHostId: 'h1', runtime: { h1: { status: 'connected' } } } as never)
  __resetReturnWorkspaceForTest()
  const a = ws().addWorkspace('A').id
  const session = createTab({ kind: 'new-tab' })
  useTabStore.getState().addTab(session)
  ws().insertTab(session.id, a)
  useTabStore.getState().setActiveTab(session.id)
  const snapshot: FleetSnapshot = { hostId: 'p', term: 1, order: ['h1'], hosts: { h1: { id: 'h1', alias: 'h1' } as never } }
  useFleetStore.setState({ snapshot, primaryHostId: 'p', stale: false })
})

describe('openWatchArea on the jobs tab', () => {
  it('selects the jobs tab once a connected daemon announces jobs.v1', () => {
    useNexHostStore.setState({ byHost: { h1: { phase: 'ready', daemonCapabilities: ['jobs.v1'] } } } as never)
    openWatchArea({ tab: 'jobs' })
    expect(area()!.tabs).toEqual([G, J, watchHostTabId('h1')])
    expect(useTabStore.getState().activeTabId).toBe(J)
    expect(ws().activeWorkspaceId).toBe(WATCH_WORKSPACE_ID)
    expect(area()!.activeTabId).toBe(J)
  })

  it('falls back to the first tab where no jobs tab exists yet', () => {
    openWatchArea({ tab: 'jobs' })
    expect(area()!.tabs).toEqual([G, watchHostTabId('h1')])
    expect(useTabStore.getState().activeTabId).toBe(G)
  })
})
