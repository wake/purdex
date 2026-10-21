// T-a3.4 / T-a3.6: the guardian tab's content, through the real reconcile. The pane holds the real `tmux-session` content of the
// holder's `pdx-watch` while it can be shown, else the `watch-guardian` panel; it is written when this device's derivation
// changes, never enforced on every run.
import { describe, it, expect, beforeEach } from 'vitest'
import { useWorkspaceStore, WATCH_WORKSPACE_ID } from '../store'
import { useTabStore } from '../../../stores/useTabStore'
import { useHostStore } from '../../../stores/useHostStore'
import { useSessionStore } from '../../../stores/useSessionStore'
import { useWatchStatusStore } from '../../../lib/watch/status-store'
import type { WatchStatus } from '../../../lib/watch/status-types'
import { WATCH_GUARDIAN_TAB_ID, watchHostTabId } from '../../../lib/watch/ids'
import { buildTabsSection } from '../../../lib/profile/sections'
import { applyTabs } from '../../../lib/profile/applier'
import type { PaneContent } from '../../../types/tab'
import type { Session } from '../../../lib/host-api'
import { createWatchReconciler } from './watch-area'

const ws = () => useWorkspaceStore.getState()
const watchTabs = () => ws().workspaces.find((w) => w.id === WATCH_WORKSPACE_ID)?.tabs ?? []
const view = (ids: string[] = []) => ({ hostIds: ids, stale: false })
const T0 = 1_000_000
const content = (): PaneContent => (useTabStore.getState().tabs[WATCH_GUARDIAN_TAB_ID].layout as { pane: { content: PaneContent } }).pane.content

const status = (hostId: string, state: 'awake' | 'resting' | 'none', since = '2026-10-16T01:00:00Z', extra: Partial<WatchStatus> = {}): WatchStatus =>
  ({ self: { host_id: hostId, state, since, session_id: 's1' }, enabled: true, auto_wake: true, mode: 'patrol', targets: [], ...extra })
const answer = (appHost: string, s: WatchStatus) =>
  useWatchStatusStore.setState((st) => ({ byHost: { ...st.byHost, [appHost]: { kind: 'ok', status: s, at: 1 } } }))
const session = (name: string, code: string, instance = 'i1'): Session => ({ code, name, cwd: '/w', mode: 'terminal', tmux_instance: instance })
const connectH2 = () => useHostStore.setState((st) => ({ runtime: { ...st.runtime, h2: { status: 'connected' } } }) as never)
const failed = (appHost: string) => useWatchStatusStore.setState((st) => ({ byHost: { ...st.byHost, [appHost]: { kind: 'error', at: 1 } } }))
const holdSession = (code = 'wc1') => useTabStore.getState().setPaneContent(WATCH_GUARDIAN_TAB_ID, `${WATCH_GUARDIAN_TAB_ID}.p`, tmuxOn('h1', code))
const tmuxOn = (hostId: string, code: string, instance = 'i1'): PaneContent =>
  ({ kind: 'tmux-session', hostId, sessionCode: code, mode: 'terminal', cachedName: 'pdx-watch', tmuxInstance: instance })

beforeEach(() => {
  ws().reset()
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
  useWatchStatusStore.getState().reset()
  useSessionStore.setState({ sessions: {} })
  useHostStore.setState({
    hosts: {
      h1: { id: 'h1', name: 'mlab', ip: '1', port: 1, token: 't', order: 0, daemonId: 'd1' },
      h2: { id: 'h2', name: 'air26', ip: '2', port: 1, token: 't', order: 1, daemonId: 'd2' },
    },
    hostOrder: ['h1', 'h2'], activeHostId: 'h1', runtime: { h1: { status: 'connected' }, h2: { status: 'disconnected' } },
  } as never)
  ws().addWorkspace('A')
  ws().ensureWatchWorkspace()
})

describe('the guardian tab is created first', () => {
  it('is the first tab of the area, locked, with a fixed pane id and the panel content', () => {
    createWatchReconciler().reconcile(view(['x', 'y']), T0)
    expect(watchTabs()).toEqual([WATCH_GUARDIAN_TAB_ID, watchHostTabId('x'), watchHostTabId('y')])
    const tab = useTabStore.getState().tabs[WATCH_GUARDIAN_TAB_ID]
    expect(tab.locked).toBe(true)
    expect(tab.layout).toEqual({ type: 'leaf', pane: { id: `${WATCH_GUARDIAN_TAB_ID}.p`, content: { kind: 'watch-guardian' } } })
  })

  it('is made even without a fleet view, and put before host tabs that already exist', () => {
    const r = createWatchReconciler()
    r.reconcile(view(['x']), T0)
    // as if an older App had made only the host tab
    ws().removeTabFromWorkspace(WATCH_WORKSPACE_ID, WATCH_GUARDIAN_TAB_ID)
    useTabStore.setState((s) => { const { [WATCH_GUARDIAN_TAB_ID]: _gone, ...tabs } = s.tabs; return { tabs } })
    r.reconcile(null, T0 + 1)
    expect(watchTabs()[0]).toBe(WATCH_GUARDIAN_TAB_ID)
  })

  it('keeps the order the user dragged it to', () => {
    const r = createWatchReconciler()
    r.reconcile(view(['x']), T0)
    ws().reorderWorkspaceTabs(WATCH_WORKSPACE_ID, [watchHostTabId('x'), WATCH_GUARDIAN_TAB_ID])
    r.reconcile(view(['x']), T0 + 1)
    expect(watchTabs()).toEqual([watchHostTabId('x'), WATCH_GUARDIAN_TAB_ID])
  })
})

describe('the content of the guardian pane', () => {
  it('awake, holder connected, pdx-watch found → its tmux-session', () => {
    answer('h1', status('d1', 'awake'))
    useSessionStore.setState({ sessions: { h1: [session('other', 'o1'), session('pdx-watch', 'wc1')] } })
    createWatchReconciler().reconcile(view(), T0)
    expect(content()).toEqual(tmuxOn('h1', 'wc1'))
  })

  it('resting with a pdx-watch session → its tmux-session as well', () => {
    answer('h1', status('d1', 'resting'))
    useSessionStore.setState({ sessions: { h1: [session('pdx-watch', 'wc1', 'i9')] } })
    createWatchReconciler().reconcile(view(), T0)
    expect(content()).toEqual(tmuxOn('h1', 'wc1', 'i9'))
  })

  it('uses the status tmux_session when the status carries it', () => {
    answer('h1', status('d1', 'awake', undefined, { watcher: { host_id: 'd1', alias: 'mlab', session_id: 's1', state: 'awake', tmux_session: 'pdx-watch-b' } }))
    useSessionStore.setState({ sessions: { h1: [session('pdx-watch', 'wc1'), session('pdx-watch-b', 'wc2')] } })
    createWatchReconciler().reconcile(view(), T0)
    expect(content()).toMatchObject({ kind: 'tmux-session', sessionCode: 'wc2' })
  })

  it.each([
    ['none', () => answer('h1', status('d1', 'none'))],
    ['unknown', () => undefined],
    ['the holder not connected to this App', () => answer('h1', status('d9', 'none', undefined, { current: { alias: 'far', host_id: 'dfar', session_id: 'x', state: 'awake' } }))],
  ])('%s → the panel', (_name, setup) => {
    setup()
    createWatchReconciler().reconcile(view(), T0)
    expect(content()).toEqual({ kind: 'watch-guardian' })
  })

  it('the holder connected but its session list has no pdx-watch → the panel', () => {
    answer('h1', status('d1', 'awake'))
    useSessionStore.setState({ sessions: { h1: [session('other', 'o1')] } })
    createWatchReconciler().reconcile(view(), T0)
    expect(content()).toEqual({ kind: 'watch-guardian' })
  })

  it('a session list that is still loading leaves the content as it is', () => {
    answer('h1', status('d1', 'awake'))
    useSessionStore.setState({ sessions: { h1: [session('pdx-watch', 'wc1')] } })
    const r = createWatchReconciler()
    r.reconcile(view(), T0)
    expect(content()).toEqual(tmuxOn('h1', 'wc1'))
    useSessionStore.setState({ sessions: {} }) // the host reconnects and its list is not there yet
    r.reconcile(view(), T0 + 1)
    expect(content()).toEqual(tmuxOn('h1', 'wc1'))
  })

  it('the session going away turns the content back into the panel', () => {
    answer('h1', status('d1', 'awake'))
    useSessionStore.setState({ sessions: { h1: [session('pdx-watch', 'wc1')] } })
    const r = createWatchReconciler()
    r.reconcile(view(), T0)
    useSessionStore.setState({ sessions: { h1: [] } })
    r.reconcile(view(), T0 + 1)
    expect(content()).toEqual({ kind: 'watch-guardian' })
  })

  it('a failover: host A awake, then host B awake → the content becomes B\'s session', () => {
    connectH2()
    useSessionStore.setState({ sessions: { h1: [session('pdx-watch', 'a1')], h2: [session('pdx-watch', 'b1', 'j1')] } })
    answer('h1', status('d1', 'awake'))
    answer('h2', status('d2', 'none'))
    const r = createWatchReconciler()
    r.reconcile(view(), T0)
    expect(content()).toEqual(tmuxOn('h1', 'a1'))
    answer('h1', status('d1', 'resting'))
    answer('h2', status('d2', 'awake', '2026-10-16T03:00:00Z'))
    r.reconcile(view(), T0 + 1)
    expect(content()).toEqual(tmuxOn('h2', 'b1', 'j1'))
  })
})

describe('edge-triggered, not enforced', () => {
  it('a content that arrived by sync stays while this device\'s derivation is unchanged', () => {
    answer('h1', status('d1', 'awake'))
    useSessionStore.setState({ sessions: { h1: [session('pdx-watch', 'wc1')] } })
    const r = createWatchReconciler()
    r.reconcile(view(), T0)
    const synced = tmuxOn('h2', 'zz9')
    useTabStore.getState().setPaneContent(WATCH_GUARDIAN_TAB_ID, `${WATCH_GUARDIAN_TAB_ID}.p`, synced)
    r.reconcile(view(), T0 + 1)
    expect(content()).toEqual(synced)
    // …and the next change of the derivation writes again
    useSessionStore.setState({ sessions: { h1: [session('pdx-watch', 'wc2')] } })
    r.reconcile(view(), T0 + 2)
    expect(content()).toEqual(tmuxOn('h1', 'wc2'))
  })

  it('an unchanged derivation writes nothing', () => {
    answer('h1', status('d1', 'awake'))
    useSessionStore.setState({ sessions: { h1: [session('pdx-watch', 'wc1')] } })
    const r = createWatchReconciler()
    r.reconcile(view(), T0)
    const before = useTabStore.getState().tabs
    r.reconcile(view(), T0 + 1)
    expect(useTabStore.getState().tabs).toBe(before)
  })

  it('two devices with the same derivation converge through one applyTabs each way', () => {
    answer('h1', status('d1', 'awake'))
    useSessionStore.setState({ sessions: { h1: [session('pdx-watch', 'wc1')] } })
    const snap = () => ({ workspaces: ws().workspaces, tabs: useTabStore.getState().tabs })
    const load = (s: ReturnType<typeof snap>) => {
      useWorkspaceStore.setState({ workspaces: s.workspaces })
      useTabStore.setState({ tabs: s.tabs, tabOrder: Object.keys(s.tabs) })
    }
    const rA = createWatchReconciler()
    const rB = createWatchReconciler()
    const base = snap()
    rA.reconcile(view(), T0)
    let A = snap()
    load(base)
    rB.reconcile(view(), T0)
    let B = snap()
    const send = (from: ReturnType<typeof snap>, to: ReturnType<typeof snap>, r: ReturnType<typeof createWatchReconciler>) => {
      const payload = buildTabsSection(from.workspaces.find((w) => w.id === WATCH_WORKSPACE_ID)!, from.tabs)
      load(to)
      const out = applyTabs({ tabs: useTabStore.getState().tabs, workspaces: ws().workspaces }, WATCH_WORKSPACE_ID, payload)
      load({ workspaces: out.next.workspaces, tabs: out.next.tabs })
      r.reconcile(view(), T0 + 1000)
      return snap()
    }
    const seen: string[] = []
    for (let i = 0; i < 4; i++) {
      B = send(A, B, rB)
      A = send(B, A, rA)
      seen.push(JSON.stringify([A.tabs[WATCH_GUARDIAN_TAB_ID], B.tabs[WATCH_GUARDIAN_TAB_ID]]))
    }
    expect(seen[2]).toBe(seen[3])
    const ca = (A.tabs[WATCH_GUARDIAN_TAB_ID].layout as { pane: { content: PaneContent } }).pane.content
    const cb = (B.tabs[WATCH_GUARDIAN_TAB_ID].layout as { pane: { content: PaneContent } }).pane.content
    expect(ca).toEqual(tmuxOn('h1', 'wc1'))
    expect(cb).toEqual(ca)
  })
})

describe('the content rule: wait for a settled derivation, downgrade only on device-independent evidence', () => {
  const start = (): ReturnType<typeof createWatchReconciler> => {
    const r = createWatchReconciler()
    r.reconcile(view(), T0) // makes the tab with the panel
    return r
  }

  it('a restart: the synced session stays while the status store is empty, and when statuses then arrive', () => {
    const r = start()
    holdSession()
    r.reconcile(view(), T0 + 1)
    expect(content()).toEqual(tmuxOn('h1', 'wc1'))
    const fresh = createWatchReconciler() // the restarted device: no memory, empty store
    fresh.reconcile(view(), T0 + 2)
    expect(content()).toEqual(tmuxOn('h1', 'wc1'))
    answer('h1', status('d1', 'awake'))
    useSessionStore.setState({ sessions: { h1: [session('pdx-watch', 'wc1')] } })
    fresh.reconcile(view(), T0 + 3)
    expect(content()).toEqual(tmuxOn('h1', 'wc1'))
  })

  it('a connected host that has not answered yet leaves the content alone', () => {
    const r = start()
    holdSession()
    connectH2()
    answer('h1', status('d1', 'none')) // h2 silent
    r.reconcile(view(), T0 + 1)
    expect(content()).toEqual(tmuxOn('h1', 'wc1'))
  })

  it('this device not reaching the holder (reachable:false) while the pane holds the session → no write', () => {
    const r = start()
    holdSession()
    answer('h1', status('d9', 'none', undefined, { current: { alias: 'far', host_id: 'dfar', session_id: 'x', state: 'awake' } }))
    r.reconcile(view(), T0 + 1)
    expect(content()).toEqual(tmuxOn('h1', 'wc1'))
  })

  it('one failed read, or a forgotten host → no write', () => {
    const r = start()
    holdSession()
    failed('h1')
    r.reconcile(view(), T0 + 1)
    expect(content()).toEqual(tmuxOn('h1', 'wc1'))
    answer('h1', status('d1', 'awake'))
    useSessionStore.setState({ sessions: { h1: [session('pdx-watch', 'wc1')] } })
    r.reconcile(view(), T0 + 2)
    useWatchStatusStore.getState().forget('h1')
    r.reconcile(view(), T0 + 3)
    expect(content()).toEqual(tmuxOn('h1', 'wc1'))
  })

  it('none from an ok host → the panel is written', () => {
    answer('h1', status('d1', 'awake'))
    useSessionStore.setState({ sessions: { h1: [session('pdx-watch', 'wc1')] } })
    const r = start()
    expect(content()).toEqual(tmuxOn('h1', 'wc1'))
    answer('h1', status('d1', 'none'))
    r.reconcile(view(), T0 + 1)
    expect(content()).toEqual({ kind: 'watch-guardian' })
  })

  it('the holder reachable, list loaded, no pdx-watch → the panel is written', () => {
    answer('h1', status('d1', 'awake'))
    useSessionStore.setState({ sessions: { h1: [session('pdx-watch', 'wc1')] } })
    const r = start()
    expect(content()).toEqual(tmuxOn('h1', 'wc1'))
    answer('h1', status('d1', 'awake'))
    useSessionStore.setState({ sessions: { h1: [session('other', 'o1')] } })
    r.reconcile(view(), T0 + 1)
    expect(content()).toEqual({ kind: 'watch-guardian' })
  })

  it('a failover to B, whose session is found → B\'s session is written over A\'s', () => {
    connectH2()
    useSessionStore.setState({ sessions: { h1: [session('pdx-watch', 'a1')], h2: [session('pdx-watch', 'b1', 'j1')] } })
    answer('h1', status('d1', 'awake'))
    answer('h2', status('d2', 'none'))
    const r = start()
    expect(content()).toEqual(tmuxOn('h1', 'a1'))
    answer('h1', status('d1', 'resting'))
    answer('h2', status('d2', 'awake', '2026-10-16T03:00:00Z'))
    r.reconcile(view(), T0 + 1)
    expect(content()).toEqual(tmuxOn('h2', 'b1', 'j1'))
  })

  it('two devices with different connectivity both end on the session, never the panel', () => {
    const snap = () => ({ workspaces: ws().workspaces, tabs: useTabStore.getState().tabs })
    const load = (s: ReturnType<typeof snap>) => {
      useWorkspaceStore.setState({ workspaces: s.workspaces })
      useTabStore.setState({ tabs: s.tabs, tabOrder: Object.keys(s.tabs) })
    }
    const own = (reach: boolean) => {
      useWatchStatusStore.getState().reset()
      useSessionStore.setState({ sessions: reach ? { h1: [session('pdx-watch', 'wc1')] } : {} })
      answer('h1', reach ? status('d1', 'awake') : status('d9', 'none', undefined, { current: { alias: 'far', host_id: 'dfar', session_id: 'x', state: 'awake' } }))
    }
    const rA = createWatchReconciler()
    const rB = createWatchReconciler()
    own(true)
    rA.reconcile(view(), T0)
    const A = snap()
    expect(content()).toEqual(tmuxOn('h1', 'wc1'))
    load(A) // device 2 first opens with what device 1 synced
    own(false)
    rB.reconcile(view(), T0)
    let B = snap()
    const send = (from: ReturnType<typeof snap>, to: ReturnType<typeof snap>, r: ReturnType<typeof createWatchReconciler>, reach: boolean) => {
      const payload = buildTabsSection(from.workspaces.find((w) => w.id === WATCH_WORKSPACE_ID)!, from.tabs)
      load(to)
      own(reach)
      const out = applyTabs({ tabs: useTabStore.getState().tabs, workspaces: ws().workspaces }, WATCH_WORKSPACE_ID, payload)
      load({ workspaces: out.next.workspaces, tabs: out.next.tabs })
      r.reconcile(view(), T0 + 1000)
      return snap()
    }
    let a = A
    for (let i = 0; i < 3; i++) {
      a = send(B, a, rA, true)
      B = send(a, B, rB, false)
    }
    const c = (x: ReturnType<typeof snap>) => (x.tabs[WATCH_GUARDIAN_TAB_ID].layout as { pane: { content: PaneContent } }).pane.content
    expect(c(a)).toEqual(tmuxOn('h1', 'wc1'))
    expect(c(B)).toEqual(tmuxOn('h1', 'wc1'))
  })

  it('a non-leaf layout skips the write and the next run retries', () => {
    const r = start()
    const tab = useTabStore.getState().tabs[WATCH_GUARDIAN_TAB_ID]
    useTabStore.setState((s) => ({ tabs: { ...s.tabs, [WATCH_GUARDIAN_TAB_ID]: { ...tab, layout: { type: 'split', direction: 'h', children: [] } as never } } }))
    answer('h1', status('d1', 'awake'))
    useSessionStore.setState({ sessions: { h1: [session('pdx-watch', 'wc1')] } })
    r.reconcile(view(), T0 + 1)
    useTabStore.setState((s) => ({ tabs: { ...s.tabs, [WATCH_GUARDIAN_TAB_ID]: tab } }))
    r.reconcile(view(), T0 + 2)
    expect(content()).toEqual(tmuxOn('h1', 'wc1'))
  })
})
