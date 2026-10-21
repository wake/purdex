// WWD-7b1-2a: the jobs tab (`watch-jobs`) through the real reconcile. It is made right after the guardian and events tabs once a connected daemon announces `jobs.v1`, and only then; it is never removed by the reconcile.
import { describe, it, expect, beforeEach } from 'vitest'
import { useWorkspaceStore, WATCH_WORKSPACE_ID } from '../store'
import { useTabStore } from '../../../stores/useTabStore'
import { useHostStore } from '../../../stores/useHostStore'
import { useNexHostStore } from '../../../stores/useNexHostStore'
import { useSessionStore } from '../../../stores/useSessionStore'
import { useWatchStatusStore } from '../../../lib/watch/status-store'
import type { WatchStatus } from '../../../lib/watch/status-types'
import { WATCH_EVENTS_TAB_ID, WATCH_GUARDIAN_TAB_ID, WATCH_JOBS_TAB_ID, watchHostTabId } from '../../../lib/watch/ids'
import { createWatchReconciler } from './watch-area'

const ws = () => useWorkspaceStore.getState()
const watchTabs = () => ws().workspaces.find((w) => w.id === WATCH_WORKSPACE_ID)?.tabs ?? []
const view = (ids: string[] = []) => ({ hostIds: ids, stale: false })
const T0 = 1_000_000
const status = (): WatchStatus => ({ self: { host_id: 'd1', state: 'none' }, enabled: true, auto_wake: true, mode: 'patrol', targets: [] })
const answerOk = (appHost: string) => useWatchStatusStore.setState((st) => ({ byHost: { ...st.byHost, [appHost]: { kind: 'ok', status: status(), at: 1 } } }))
const announce = (caps: string[], fetchedAt = 1) => useNexHostStore.setState({ byHost: { h1: { phase: 'ready', fetchedAt, daemonCapabilities: caps } } } as never)

beforeEach(() => {
  ws().reset()
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
  useWatchStatusStore.getState().reset()
  useNexHostStore.setState({ byHost: {} } as never)
  useSessionStore.setState({ sessions: {} })
  useHostStore.setState({
    hosts: { h1: { id: 'h1', name: 'mlab', ip: '1', port: 1, token: 't', order: 0, daemonId: 'd1' } },
    hostOrder: ['h1'], activeHostId: 'h1', runtime: { h1: { status: 'connected' } },
  } as never)
  ws().addWorkspace('A')
  ws().ensureWatchWorkspace()
})

describe('the jobs tab', () => {
  it('is not made while no connected daemon announces jobs.v1', () => {
    const r = createWatchReconciler()
    r.reconcile(view(['x']), T0)
    expect(watchTabs()).not.toContain(WATCH_JOBS_TAB_ID)
    announce(['pool.v1'])
    r.reconcile(view(['x']), T0)
    expect(WATCH_JOBS_TAB_ID in useTabStore.getState().tabs).toBe(false)
    // announced, but the connection is gone: the capability cache outlives it, so it does not count
    announce(['jobs.v1'])
    useHostStore.setState({ runtime: { h1: { status: 'disconnected' } } } as never)
    r.reconcile(view(['x']), T0)
    expect(WATCH_JOBS_TAB_ID in useTabStore.getState().tabs).toBe(false)
  })

  it('is not made on the strength of the last connection\'s capabilities: after a reconnect the cache is stale until it is read again, and a daemon that no longer has jobs.v1 makes no tab', () => {
    const r = createWatchReconciler()
    announce(['jobs.v1'], 0) // marked stale by the reconnect (data kept, fetchedAt 0)
    r.reconcile(view(['x']), T0)
    expect(WATCH_JOBS_TAB_ID in useTabStore.getState().tabs).toBe(false)
    announce([], 7) // read again: the new daemon does not have it
    r.reconcile(view(['x']), T0)
    expect(WATCH_JOBS_TAB_ID in useTabStore.getState().tabs).toBe(false)
    announce(['jobs.v1'], 8) // and one that does
    r.reconcile(view(['x']), T0)
    expect(watchTabs()).toContain(WATCH_JOBS_TAB_ID)
  })

  it('is made after the guardian and events tabs, before the host tabs, locked, with a fixed pane id', () => {
    answerOk('h1')
    announce(['jobs.v1'])
    createWatchReconciler().reconcile(view(['x', 'y']), T0)
    expect(watchTabs()).toEqual([WATCH_GUARDIAN_TAB_ID, WATCH_EVENTS_TAB_ID, WATCH_JOBS_TAB_ID, watchHostTabId('x'), watchHostTabId('y')])
    const tab = useTabStore.getState().tabs[WATCH_JOBS_TAB_ID]
    expect(tab.locked).toBe(true)
    expect(tab.layout).toEqual({ type: 'leaf', pane: { id: `${WATCH_JOBS_TAB_ID}.p`, content: { kind: 'watch-jobs' } } })
  })

  it('without an events tab it follows the guardian tab', () => {
    announce(['jobs.v1'])
    createWatchReconciler().reconcile(view(['x']), T0)
    expect(watchTabs()).toEqual([WATCH_GUARDIAN_TAB_ID, WATCH_JOBS_TAB_ID, watchHostTabId('x')])
  })

  it('is made when a daemon starts announcing jobs.v1 after the host tabs exist', () => {
    const r = createWatchReconciler()
    r.reconcile(view(['x']), T0)
    expect(watchTabs()).toEqual([WATCH_GUARDIAN_TAB_ID, watchHostTabId('x')])
    announce(['jobs.v1'])
    r.reconcile(view(['x']), T0)
    expect(watchTabs()).toEqual([WATCH_GUARDIAN_TAB_ID, WATCH_JOBS_TAB_ID, watchHostTabId('x')])
  })

  it('is made once, and the reader\'s own order stands afterwards', () => {
    announce(['jobs.v1'])
    const r = createWatchReconciler()
    r.reconcile(view(['x']), T0)
    ws().reorderWorkspaceTabs(WATCH_WORKSPACE_ID, [WATCH_GUARDIAN_TAB_ID, watchHostTabId('x'), WATCH_JOBS_TAB_ID])
    r.reconcile(view(['x']), T0)
    r.reconcile(view(['x']), T0)
    expect(watchTabs()).toEqual([WATCH_GUARDIAN_TAB_ID, watchHostTabId('x'), WATCH_JOBS_TAB_ID])
    expect(Object.keys(useTabStore.getState().tabs).filter((id) => id === WATCH_JOBS_TAB_ID)).toHaveLength(1)
  })

  it('stays when the daemon that made it goes away', () => {
    announce(['jobs.v1'])
    const r = createWatchReconciler()
    r.reconcile(view(['x']), T0)
    useHostStore.setState({ runtime: { h1: { status: 'disconnected' } } } as never)
    announce([])
    r.reconcile(view(['x']), T0)
    expect(watchTabs()).toContain(WATCH_JOBS_TAB_ID)
  })

  it('a tab that arrived by sync is kept (and put in the area) even where no daemon announces jobs.v1', () => {
    useTabStore.getState().addTab({ id: WATCH_JOBS_TAB_ID, pinned: false, locked: true, createdAt: 1, layout: { type: 'leaf', pane: { id: `${WATCH_JOBS_TAB_ID}.p`, content: { kind: 'watch-jobs' } } } })
    createWatchReconciler().reconcile(view(['x']), T0)
    expect(watchTabs()).toContain(WATCH_JOBS_TAB_ID)
    expect(Object.keys(useTabStore.getState().tabs).filter((id) => id === WATCH_JOBS_TAB_ID)).toHaveLength(1)
  })
})
