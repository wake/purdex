import { describe, it, expect, beforeEach, vi } from 'vitest'

vi.mock('../features/workspace/lib/icon-path-cache', () => ({
  getIconPath: () => null,
  isWeightLoaded: () => true,
  prefetchWeight: () => Promise.resolve(),
}))

import { renderHook, act } from '@testing-library/react'
import type { ReactNode } from 'react'
import { createElement } from 'react'
import { Router } from 'wouter'
import { memoryLocation } from 'wouter/memory-location'
import { resetLastHostSelection } from '../components/HostPage'
import { useRouteSync } from './useRouteSync'
import { useTabStore } from '../stores/useTabStore'
import { useHostStore } from '../stores/useHostStore'
import { useShownHostsStore } from '../stores/useShownHostsStore'
import { syncIdOfSync } from '../lib/profile/host-identity'
import { getPrimaryPane } from '../lib/pane-tree'
import type { Tab } from '../types/tab'

function makeTab(id: string, contentKind: 'tmux-session' | 'dashboard' | 'history' | 'settings', mode?: 'terminal'): Tab {
  const content = contentKind === 'tmux-session'
    ? { kind: 'tmux-session' as const, hostId: 'test-host', sessionCode: 'test', mode: mode ?? 'terminal' as const, cachedName: '', tmuxInstance: '' }
    : contentKind === 'settings'
      ? { kind: 'settings' as const, scope: 'global' as const }
      : { kind: contentKind as 'dashboard' | 'history' }
  return {
    id,
    pinned: false,
    locked: false,
    createdAt: Date.now(),
    layout: { type: 'leaf', pane: { id: `pane-${id}`, content } },
  }
}

function resetStore(data?: { tabs?: Record<string, Tab>; tabOrder?: string[]; activeTabId?: string | null }) {
  // Use merge mode (no second arg) so zustand action methods are preserved
  useTabStore.setState({
    tabs: data?.tabs ?? {},
    tabOrder: data?.tabOrder ?? [],
    activeTabId: data?.activeTabId ?? null,
    visitHistory: [],
  })
}

function createWrapper(mem: ReturnType<typeof memoryLocation>) {
  return function Wrapper({ children }: { children: ReactNode }) {
    return createElement(Router, { hook: mem.hook, children })
  }
}

describe('useRouteSync', () => {
  beforeEach(() => {
    resetStore()
    resetLastHostSelection()
    useShownHostsStore.setState({ ids: ['h1'] }) // predates shown hosts (H2d-3): h1 shown
  })

  it('singleton route /history opens a history tab', () => {
    const mem = memoryLocation({ path: '/history', record: true })

    renderHook(() => useRouteSync(), { wrapper: createWrapper(mem) })

    // openSingletonTab should have created a tab with history content
    const state = useTabStore.getState()
    expect(state.tabOrder.length).toBeGreaterThanOrEqual(1)
    const tabId = state.activeTabId!
    expect(tabId).toBeTruthy()
    const tab = state.tabs[tabId]
    const primary = getPrimaryPane(tab.layout)
    expect(primary.content.kind).toBe('history')
  })

  it('session route /t/abc123/terminal with existing tab activates it', () => {
    const tab = makeTab('abc123', 'tmux-session', 'terminal')
    resetStore({
      tabs: { abc123: tab },
      tabOrder: ['abc123'],
      activeTabId: null,
    })

    const mem = memoryLocation({ path: '/t/abc123/terminal', record: true })
    renderHook(() => useRouteSync(), { wrapper: createWrapper(mem) })

    expect(useTabStore.getState().activeTabId).toBe('abc123')
  })

  it('session route with missing tab sets activeTabId to null', () => {
    // No active tab — navigating to a nonexistent session tab should leave activeTabId null
    resetStore({
      tabs: {},
      tabOrder: [],
      activeTabId: null,
    })

    const mem = memoryLocation({ path: '/t/abc123/terminal', record: true })
    renderHook(() => useRouteSync(), { wrapper: createWrapper(mem) })

    expect(useTabStore.getState().activeTabId).toBeNull()
  })

  it('tab activation updates URL', () => {
    const tab = makeTab('abc123', 'tmux-session', 'terminal')
    resetStore({
      tabs: { abc123: tab },
      tabOrder: ['abc123'],
      activeTabId: null,
    })

    const mem = memoryLocation({ path: '/', record: true })
    renderHook(() => useRouteSync(), { wrapper: createWrapper(mem) })

    // Activate tab
    act(() => {
      useTabStore.getState().setActiveTab('abc123')
    })

    expect(mem.history).toContain('/t/abc123/terminal')
  })

  it('/ route does not open any tab (no-op)', () => {
    const mem = memoryLocation({ path: '/', record: true })

    renderHook(() => useRouteSync(), { wrapper: createWrapper(mem) })

    const state = useTabStore.getState()
    expect(state.tabOrder).toHaveLength(0)
    expect(state.activeTabId).toBeNull()
  })

  it('invalid ID format in URL does not activate any tab', () => {
    const mem = memoryLocation({ path: '/t/INVALID/terminal', record: true })

    renderHook(() => useRouteSync(), { wrapper: createWrapper(mem) })

    // parseRoute returns null for invalid IDs — no tab should be activated
    expect(useTabStore.getState().activeTabId).toBeNull()
  })

  it('does not overwrite /settings/terminal back to /settings', () => {
    const settingsTab: Tab = {
      id: 'set001',
      pinned: false,
      locked: false,
      createdAt: Date.now(),
      layout: { type: 'leaf', pane: { id: 'pane-set001', content: { kind: 'settings', scope: 'global' } } },
    }
    resetStore({
      tabs: { set001: settingsTab },
      tabOrder: ['set001'],
      activeTabId: 'set001',
    })

    const mem = memoryLocation({ path: '/settings/terminal', record: true })
    renderHook(() => useRouteSync(), { wrapper: createWrapper(mem) })

    // Tab→URL should NOT replace /settings/terminal with /settings
    const lastPath = mem.history[mem.history.length - 1]
    expect(lastPath).toBe('/settings/terminal')
  })

  it('opens the singleton hosts tab for /hosts/test-host/logs without rewriting the URL', () => {
    const mem = memoryLocation({ path: '/hosts/test-host/logs', record: true })

    renderHook(() => useRouteSync(), { wrapper: createWrapper(mem) })

    const state = useTabStore.getState()
    const tab = state.activeTabId ? state.tabs[state.activeTabId] : null
    const primary = tab ? getPrimaryPane(tab.layout) : null

    expect(primary?.content.kind).toBe('hosts')
    expect(mem.history[mem.history.length - 1]).toBe('/hosts/test-host/logs')
  })

  it('opens the singleton hosts tab for invalid host deep links without rewriting them to bare /hosts', () => {
    const mem = memoryLocation({ path: '/hosts/test-host/not-a-page', record: true })

    renderHook(() => useRouteSync(), { wrapper: createWrapper(mem) })

    const state = useTabStore.getState()
    const tab = state.activeTabId ? state.tabs[state.activeTabId] : null
    const primary = tab ? getPrimaryPane(tab.layout) : null

    expect(primary?.content.kind).toBe('hosts')
    expect(mem.history[mem.history.length - 1]).toBe('/hosts/test-host/not-a-page')
  })

  it('opens /execution/<host>/<id> as an execution pane with the resolved host', () => {
    useHostStore.setState({
      hosts: { h1: { id: 'h1', name: 'H1', ip: '1', port: 1, order: 0 } } as never,
      hostOrder: ['h1'], activeHostId: 'h1', runtime: {},
    })
    const mem = memoryLocation({ path: '/execution/h1/exc_1', record: true })
    renderHook(() => useRouteSync(), { wrapper: createWrapper(mem) })
    const tab = useTabStore.getState().tabs[useTabStore.getState().activeTabId!]
    expect(getPrimaryPane(tab.layout).content).toEqual({ kind: 'execution', executionId: 'exc_1', host: 'h1' })
  })

  it('opens /execution/<unknownHost>/<id> with the unknown host verbatim, never falling back to another daemon (spec §4.3.2 step 5)', () => {
    useHostStore.setState({
      hosts: { h1: { id: 'h1', name: 'H1', ip: '1', port: 1, order: 0 } } as never,
      hostOrder: ['h1'], activeHostId: 'h1', runtime: {},
    })
    // H2d-3: a ref that is not a local host is openable only when listed.
    useShownHostsStore.setState({ ids: ['h1', 'unknown-host'] })
    const mem = memoryLocation({ path: '/execution/unknown-host/exc_1', record: true })
    renderHook(() => useRouteSync(), { wrapper: createWrapper(mem) })
    const tab = useTabStore.getState().tabs[useTabStore.getState().activeTabId!]
    expect(getPrimaryPane(tab.layout).content).toEqual({ kind: 'execution', executionId: 'exc_1', host: 'unknown-host' })
  })
})

// Host ownership H2d-3 T4 — the `/execution/<host>/<execution id>` route naming a host that is not shown in this
// workbench (hidden, or a ref that is neither a local host nor listed) lands on the Hosts page: no execution tab.
describe('useRouteSync — /execution on a host not shown (H2d-3)', () => {
  const DAEMON = 'air-lab:26aaaa'
  const X = syncIdOfSync('nowhere:000000') // d1_X: not a local host
  const kinds = () => Object.values(useTabStore.getState().tabs).map((t) => getPrimaryPane(t.layout).content.kind)
  const run = (path: string) => renderHook(() => useRouteSync(), { wrapper: createWrapper(memoryLocation({ path, record: true })) })

  beforeEach(() => {
    resetStore()
    resetLastHostSelection()
    useHostStore.setState({
      hosts: {
        h1: { id: 'h1', name: 'H1', ip: '1', port: 1, order: 0, daemonId: DAEMON },
        h2: { id: 'h2', name: 'H2', ip: '2', port: 1, order: 1 },
      } as never,
      hostOrder: ['h1', 'h2'], activeHostId: 'h2', runtime: {},
    })
    useShownHostsStore.setState({ ids: ['h2'] }) // h1 hidden
  })

  it('a hidden host → the Hosts page on that host, no execution tab', () => {
    run('/execution/h1/exc_1')
    expect(kinds()).toEqual(['hosts'])
    expect(useHostStore.getState().activeHostId).toBe('h1')
  })

  it('a hostless route whose fallback hostOrder[0] is hidden → the Hosts page, no execution tab', () => {
    run('/execution/exc_1')
    expect(kinds()).toEqual(['hosts'])
    expect(useHostStore.getState().activeHostId).toBe('h1')
  })

  it('/execution/d1_X/<id> with d1_X neither a local host nor listed → the Hosts page, no tab', () => {
    run(`/execution/${X}/exc_1`)
    expect(kinds()).toEqual(['hosts'])
    expect(useHostStore.getState().activeHostId).toBe('h2')
  })

  it('/execution/d1_X/<id> with d1_X listed → the execution tab as today', () => {
    useShownHostsStore.setState({ ids: ['h2', X] })
    run(`/execution/${X}/exc_1`)
    const tab = useTabStore.getState().tabs[useTabStore.getState().activeTabId!]
    expect(getPrimaryPane(tab.layout).content).toEqual({ kind: 'execution', executionId: 'exc_1', host: X })
  })

  it('a shown host (listed by its d1_ id) → the execution tab as today', () => {
    useShownHostsStore.setState({ ids: [syncIdOfSync(DAEMON)] })
    run('/execution/h1/exc_1')
    const tab = useTabStore.getState().tabs[useTabStore.getState().activeTabId!]
    expect(getPrimaryPane(tab.layout).content).toEqual({ kind: 'execution', executionId: 'exc_1', host: 'h1' })
  })
})
