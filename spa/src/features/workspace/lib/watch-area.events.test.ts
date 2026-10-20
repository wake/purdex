// W5e: the events tab (`watch-events`) through the real reconcile. It is made right after the guardian tab once any connected
// host carries the watch module (its status answered ok), and only then; it is never removed by the reconcile.
import { describe, it, expect, beforeEach } from 'vitest'
import { useWorkspaceStore, WATCH_WORKSPACE_ID } from '../store'
import { useTabStore } from '../../../stores/useTabStore'
import { useHostStore } from '../../../stores/useHostStore'
import { useSessionStore } from '../../../stores/useSessionStore'
import { useFleetStore } from '../../../lib/fleet/store'
import { useWatchStatusStore } from '../../../lib/watch/status-store'
import { useNexHostStore } from '../../../stores/useNexHostStore'
import type { WatchStatus } from '../../../lib/watch/status-types'
import { WATCH_EVENTS_TAB_ID, WATCH_GUARDIAN_TAB_ID, watchHostTabId } from '../../../lib/watch/ids'
import { createWatchReconciler } from './watch-area'

const ws = () => useWorkspaceStore.getState()
const watchTabs = () => ws().workspaces.find((w) => w.id === WATCH_WORKSPACE_ID)?.tabs ?? []
const view = (ids: string[] = []) => ({ hostIds: ids, stale: false })
const T0 = 1_000_000
const status = (): WatchStatus => ({ self: { host_id: 'd1', state: 'none' }, enabled: true, auto_wake: true, mode: 'patrol', targets: [] })
const answerOk = (appHost: string) => useWatchStatusStore.setState((st) => ({ byHost: { ...st.byHost, [appHost]: { kind: 'ok', status: status(), at: 1 } } }))

beforeEach(() => {
  ws().reset()
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
  useFleetStore.getState().reset()
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

describe('the events tab', () => {
  it('is not made while no host carries the watch module', () => {
    const r = createWatchReconciler()
    r.reconcile(view(['x']), T0)
    expect(watchTabs()).not.toContain(WATCH_EVENTS_TAB_ID)
    useWatchStatusStore.setState({ byHost: { h1: { kind: 'unsupported' } } })
    r.reconcile(view(['x']), T0)
    expect(WATCH_EVENTS_TAB_ID in useTabStore.getState().tabs).toBe(false)
  })

  it('is made right after the guardian tab, before the host tabs, locked, with a fixed pane id', () => {
    answerOk('h1')
    createWatchReconciler().reconcile(view(['x', 'y']), T0)
    expect(watchTabs()).toEqual([WATCH_GUARDIAN_TAB_ID, WATCH_EVENTS_TAB_ID, watchHostTabId('x'), watchHostTabId('y')])
    const tab = useTabStore.getState().tabs[WATCH_EVENTS_TAB_ID]
    expect(tab.locked).toBe(true)
    expect(tab.layout).toEqual({ type: 'leaf', pane: { id: `${WATCH_EVENTS_TAB_ID}.p`, content: { kind: 'watch-events' } } })
  })

  it('is made when a host starts declaring watch after the host tabs exist', () => {
    const r = createWatchReconciler()
    r.reconcile(view(['x']), T0)
    expect(watchTabs()).toEqual([WATCH_GUARDIAN_TAB_ID, watchHostTabId('x')])
    answerOk('h1')
    r.reconcile(view(['x']), T0)
    expect(watchTabs()).toEqual([WATCH_GUARDIAN_TAB_ID, WATCH_EVENTS_TAB_ID, watchHostTabId('x')])
  })

  it('is made once, and the reader\'s own order stands afterwards', () => {
    answerOk('h1')
    const r = createWatchReconciler()
    r.reconcile(view(['x']), T0)
    ws().reorderWorkspaceTabs(WATCH_WORKSPACE_ID, [WATCH_GUARDIAN_TAB_ID, watchHostTabId('x'), WATCH_EVENTS_TAB_ID])
    r.reconcile(view(['x']), T0)
    r.reconcile(view(['x']), T0)
    expect(watchTabs()).toEqual([WATCH_GUARDIAN_TAB_ID, watchHostTabId('x'), WATCH_EVENTS_TAB_ID])
    expect(Object.keys(useTabStore.getState().tabs).filter((id) => id === WATCH_EVENTS_TAB_ID)).toHaveLength(1)
  })

  it('stays when the host that made it stops answering', () => {
    answerOk('h1')
    const r = createWatchReconciler()
    r.reconcile(view(['x']), T0)
    useWatchStatusStore.getState().reset()
    r.reconcile(view(['x']), T0)
    expect(watchTabs()).toContain(WATCH_EVENTS_TAB_ID)
  })

  it('a tab that arrived by sync is kept (and put in the area) even where no host declares watch', () => {
    useTabStore.getState().addTab({ id: WATCH_EVENTS_TAB_ID, pinned: false, locked: true, createdAt: 1, layout: { type: 'leaf', pane: { id: `${WATCH_EVENTS_TAB_ID}.p`, content: { kind: 'watch-events' } } } })
    createWatchReconciler().reconcile(view(['x']), T0)
    expect(watchTabs()).toContain(WATCH_EVENTS_TAB_ID)
    expect(Object.keys(useTabStore.getState().tabs).filter((id) => id === WATCH_EVENTS_TAB_ID)).toHaveLength(1)
  })
})

describe('the events tab and pool.alerts.v1 (MON-10c)', () => {
  const announce = (caps: string[], fetchedAt = 1) => useNexHostStore.setState({ byHost: { h1: { phase: 'ready', fetchedAt, daemonCapabilities: caps } } } as never)

  it('is made when a connected daemon announces pool.alerts.v1, though none carries watch.v1', () => {
    announce(['pool.alerts.v1'])
    createWatchReconciler().reconcile(view(['x']), T0)
    expect(watchTabs()).toContain(WATCH_EVENTS_TAB_ID)
  })

  it('is not made when no daemon announces it', () => {
    announce(['jobs.v1'])
    createWatchReconciler().reconcile(view(['x']), T0)
    expect(watchTabs()).not.toContain(WATCH_EVENTS_TAB_ID)
  })

  it('is not made for a daemon that is not connected', () => {
    useHostStore.setState({ runtime: { h1: { status: 'disconnected' } } } as never)
    announce(['pool.alerts.v1'])
    createWatchReconciler().reconcile(view(['x']), T0)
    expect(watchTabs()).not.toContain(WATCH_EVENTS_TAB_ID)
  })

  it('stays once made when the capability goes away', () => {
    announce(['pool.alerts.v1'])
    const r = createWatchReconciler()
    r.reconcile(view(['x']), T0)
    announce([])
    r.reconcile(view(['x']), T0)
    expect(watchTabs()).toContain(WATCH_EVENTS_TAB_ID)
  })
})
