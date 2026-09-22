// #1326: route-sync × GlobalSettingsPage self-heal ping-pong.
//
// When the ACTIVE tab is the global Settings tab and the URL moves to a
// non-/settings route (direct URL / pushState), there is one commit where the
// location has already changed but useRouteSync's URL→Tab effect has not yet
// switched the active tab. The still-mounted GlobalSettingsPage used to read
// that foreign URL as "/settings with no section" and rewrite it back to
// /settings/<section>, which route-sync then answered by re-activating the
// global tab — "Maximum update depth exceeded". A kept-alive (inactive)
// instance did the same on every switch to another tab.
//
// The shell below is the production wiring App uses — useRouteSync + the real
// TabContent (useTabAlivePool + PaneLayoutRenderer + module registry) — with
// the real SettingsPage registered as the settings pane renderer, so the
// keep-alive cases depend on the production alive pool, not on the test.
import { vi } from 'vitest'

vi.mock('../features/workspace/lib/icon-path-cache', () => ({
  getIconPath: () => null,
  isWeightLoaded: () => true,
  prefetchWeight: () => Promise.resolve(),
}))

vi.mock('../features/workspace/components/WorkspaceSettingsPage', () => ({
  WorkspaceSettingsPage: ({ workspaceId }: { workspaceId: string }) => (
    <div data-testid="workspace-settings-mock">ws:{workspaceId}</div>
  ),
}))

import { describe, it, expect, beforeEach, afterEach } from 'vitest'
import { act, render, screen } from '@testing-library/react'
import { Router } from 'wouter'
import { memoryLocation } from 'wouter/memory-location'
import { useRouteSync } from './useRouteSync'
import { useTabStore } from '../stores/useTabStore'
import { useHistoryStore } from '../stores/useHistoryStore'
import { useUISettingsStore } from '../stores/useUISettingsStore'
import { useWorkspaceStore } from '../features/workspace'
import { getPrimaryPane } from '../lib/pane-tree'
import { clearModuleRegistry, registerModule } from '../lib/module-registry'
import { TabContent } from '../components/TabContent'
import { SettingsPage, resetLastSection } from '../components/SettingsPage'
import { registerSettingsSection, clearSettingsSectionRegistry } from '../lib/settings-section-registry'
import { clearContributions } from '../lib/settings-contribution-registry'
import { dispatchSettingsContributions } from '../lib/dispatch-settings-contributions'
import type { Tab, PaneContent } from '../types/tab'

const UNSORTED = 'unsort'
const WS_X = 'wsxxxx'
const GLOBAL_TAB = 'gset01'
const SESSION_TAB = 'sess01'

function makeTab(id: string, content: PaneContent): Tab {
  return {
    id,
    pinned: false,
    locked: false,
    createdAt: 0,
    layout: { type: 'leaf', pane: { id: `pane-${id}`, content } },
  }
}

const Appearance = () => <div data-testid="global-settings-body">AppearanceBody</div>
const Terminal = () => <div>TerminalBody</div>

/** App.tsx's routing shell: useRouteSync + TabContent over every tab. */
function Shell() {
  useRouteSync()
  const tabs = useTabStore((s) => s.tabs)
  const tabOrder = useTabStore((s) => s.tabOrder)
  const activeTabId = useTabStore((s) => s.activeTabId)
  return (
    <TabContent
      activeTab={activeTabId ? tabs[activeTabId] ?? null : null}
      allTabs={tabOrder.map((id) => tabs[id]).filter(Boolean)}
    />
  )
}

function seed() {
  const globalTab = makeTab(GLOBAL_TAB, { kind: 'settings', scope: 'global' })
  // A heavy (tmux-session) tab: bound by keepAliveCount like settings.
  const sessionTab = makeTab(SESSION_TAB, {
    kind: 'tmux-session', hostId: 'h', sessionCode: 's', mode: 'terminal', cachedName: '', tmuxInstance: '',
  })
  const all = [globalTab, sessionTab]
  useTabStore.setState({
    tabs: Object.fromEntries(all.map((t) => [t.id, t])),
    tabOrder: all.map((t) => t.id),
    activeTabId: GLOBAL_TAB,
    visitHistory: [],
  })
  useWorkspaceStore.setState({
    workspaces: [
      { id: UNSORTED, name: 'Unsorted', tabs: all.map((t) => t.id), activeTabId: GLOBAL_TAB },
      { id: WS_X, name: 'X', tabs: [], activeTabId: null },
    ],
    activeWorkspaceId: UNSORTED,
  })
}

/** Mounts the shell; every setLocation the app issues is recorded in `navs`. */
function mount(path: string) {
  const mem = memoryLocation({ path, record: true })
  const navs: { to: string; replace: boolean }[] = []
  const hook = () => {
    const [loc, nav] = mem.hook()
    const recording = (to: string, opts?: { replace?: boolean }) => {
      navs.push({ to, replace: !!opts?.replace })
      nav(to, opts)
    }
    return [loc, recording] as ReturnType<typeof mem.hook>
  }
  const view = render(
    <Router hook={hook}>
      <Shell />
    </Router>,
  )
  const current = () => mem.history[mem.history.length - 1]
  return { ...view, mem, navs, current }
}

function activeContent(): PaneContent | null {
  const s = useTabStore.getState()
  const tab = s.activeTabId ? s.tabs[s.activeTabId] : null
  return tab ? getPrimaryPane(tab.layout).content : null
}

describe('useRouteSync × GlobalSettingsPage in the real TabContent shell (#1326)', () => {
  beforeEach(() => {
    resetLastSection()
    clearSettingsSectionRegistry()
    clearContributions()
    clearModuleRegistry()
    registerModule({ id: 'settings', name: 'Settings', panes: [{ kind: 'settings', component: SettingsPage }] })
    registerModule({
      id: 'session',
      name: 'Session',
      panes: [{ kind: 'tmux-session', component: () => <div data-testid="terminal-stub" /> }],
    })
    registerSettingsSection({ id: 'appearance', label: 'Appearance', order: 0, component: Appearance })
    registerSettingsSection({ id: 'terminal', label: 'Terminal', order: 1, component: Terminal })
    dispatchSettingsContributions([])
    useHistoryStore.setState({ browseHistory: [], closedTabs: [] })
    useUISettingsStore.setState({ keepAliveCount: 0, keepAlivePinned: false })
    seed()
  })

  afterEach(() => {
    clearModuleRegistry()
    useUISettingsStore.setState({ keepAliveCount: 0 })
  })

  it('keepAliveCount=0: navigating to /w/<ws>/settings from global Settings settles with no replace', () => {
    const { mem, navs, current } = mount('/settings/appearance')
    expect(current()).toBe('/settings/appearance')
    navs.length = 0

    act(() => {
      mem.navigate(`/w/${WS_X}/settings`)
    })

    expect(current()).toBe(`/w/${WS_X}/settings`)
    expect(activeContent()).toEqual({ kind: 'settings', scope: { workspaceId: WS_X } })
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBe(WS_X)
    expect(screen.getByTestId('workspace-settings-mock').textContent).toBe(`ws:${WS_X}`)
    expect(navs).toEqual([])
  })

  it('keepAliveCount>0: navigating to /w/<ws>/settings settles while the global Settings pane stays mounted', () => {
    useUISettingsStore.setState({ keepAliveCount: 1 })
    const { mem, navs, current } = mount('/settings/appearance')
    navs.length = 0

    act(() => {
      mem.navigate(`/w/${WS_X}/settings`)
    })

    expect(current()).toBe(`/w/${WS_X}/settings`)
    expect(activeContent()).toEqual({ kind: 'settings', scope: { workspaceId: WS_X } })
    // The previously-active global pane is kept alive (inactive) by the real pool.
    expect(screen.getByTestId('global-settings-body')).toBeTruthy()
    expect(navs).toEqual([])
  })

  it('keepAliveCount>0: switching from global Settings to a heavy tab settles on that tab with a single replace', () => {
    useUISettingsStore.setState({ keepAliveCount: 1 })
    const { navs, current } = mount('/settings/appearance')
    expect(current()).toBe('/settings/appearance')
    navs.length = 0

    act(() => {
      useTabStore.getState().setActiveTab(SESSION_TAB)
    })

    expect(screen.getByTestId('terminal-stub')).toBeTruthy()
    // Inactive global Settings pane is still mounted by the production alive pool.
    expect(screen.getByTestId('global-settings-body')).toBeTruthy()
    expect(useTabStore.getState().activeTabId).toBe(SESSION_TAB)
    expect(current()).toBe(`/t/${SESSION_TAB}/terminal`)
    expect(navs).toEqual([{ to: `/t/${SESSION_TAB}/terminal`, replace: true }])
  })

  it('keepAliveCount>0: re-activating the kept-alive global Settings pane restores its section URL', () => {
    useUISettingsStore.setState({ keepAliveCount: 1 })
    const { current } = mount('/settings/appearance')
    act(() => {
      useTabStore.getState().setActiveTab(SESSION_TAB)
    })
    expect(current()).toBe(`/t/${SESSION_TAB}/terminal`)

    act(() => {
      useTabStore.getState().setActiveTab(GLOBAL_TAB)
    })

    expect(useTabStore.getState().activeTabId).toBe(GLOBAL_TAB)
    expect(current()).toBe('/settings/appearance')
  })
})

// #1326 (cold start): a full page load whose initial URL is a deep link that
// differs from the PERSISTED active tab. On the first hydrated commit both
// useRouteSync effects run: Tab→URL used to replace the URL with the stale
// persisted tab's URL while URL→Tab (same commit, old location in its
// closure) switched the store to the deep link — next commit each undid the
// other, forever ("Maximum update depth exceeded" at the Tab→URL replace).
// The URL is the source of truth on a cold start: the deep link wins, and
// Tab→URL only corrects a URL that URL→Tab had nothing to apply for.
describe('useRouteSync cold start: deep link vs persisted active tab (#1326)', () => {
  beforeEach(() => {
    resetLastSection()
    clearSettingsSectionRegistry()
    clearContributions()
    clearModuleRegistry()
    registerModule({ id: 'settings', name: 'Settings', panes: [{ kind: 'settings', component: SettingsPage }] })
    registerModule({
      id: 'session',
      name: 'Session',
      panes: [{ kind: 'tmux-session', component: () => <div data-testid="terminal-stub" /> }],
    })
    registerSettingsSection({ id: 'appearance', label: 'Appearance', order: 0, component: Appearance })
    dispatchSettingsContributions([])
    useHistoryStore.setState({ browseHistory: [], closedTabs: [] })
    useUISettingsStore.setState({ keepAliveCount: 0, keepAlivePinned: false })
    seed() // persisted: global Settings is the active tab
  })

  afterEach(() => {
    vi.restoreAllMocks()
    clearModuleRegistry()
  })

  /** Mounts with persist NOT yet hydrated, then fires onFinishHydration — the real gate. */
  function coldMount(path: string) {
    let finish: (() => void) | null = null
    vi.spyOn(useTabStore.persist, 'hasHydrated').mockReturnValue(false)
    vi.spyOn(useTabStore.persist, 'onFinishHydration').mockImplementation((cb) => {
      finish = () => cb(useTabStore.getState())
      return () => { finish = null }
    })
    const m = mount(path)
    expect(m.navs).toEqual([]) // nothing happens before hydration
    act(() => finish!())
    return m
  }

  it('/w/<ws>/settings through the hydration gate: the deep link wins, no replace', () => {
    const { navs, current } = coldMount(`/w/${WS_X}/settings`)
    expect(current()).toBe(`/w/${WS_X}/settings`)
    expect(activeContent()).toEqual({ kind: 'settings', scope: { workspaceId: WS_X } })
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBe(WS_X)
    expect(navs).toEqual([])
  })

  it('/w/<ws>/settings already hydrated at mount: the deep link wins, no replace', () => {
    const { navs, current } = mount(`/w/${WS_X}/settings`)
    expect(current()).toBe(`/w/${WS_X}/settings`)
    expect(activeContent()).toEqual({ kind: 'settings', scope: { workspaceId: WS_X } })
    expect(navs).toEqual([])
  })

  it('/t/<other tab>/terminal: activates that tab, no replace', () => {
    const { navs, current } = coldMount(`/t/${SESSION_TAB}/terminal`)
    expect(current()).toBe(`/t/${SESSION_TAB}/terminal`)
    expect(useTabStore.getState().activeTabId).toBe(SESSION_TAB)
    expect(navs).toEqual([])
  })

  it('/history: opens the history tab, no replace', () => {
    const { navs, current } = coldMount('/history')
    expect(current()).toBe('/history')
    expect(activeContent()).toEqual({ kind: 'history' })
    expect(navs).toEqual([])
  })

  it('/hosts: opens the hosts tab, no replace', () => {
    const { navs, current } = coldMount('/hosts')
    expect(current()).toBe('/hosts')
    expect(activeContent()?.kind).toBe('hosts')
    expect(navs).toEqual([])
  })

  it('/execution/<host>/<id>: opens the execution tab, no replace', () => {
    const { navs, current } = coldMount('/execution/h1/ex1')
    expect(current()).toBe('/execution/h1/ex1')
    expect(activeContent()).toMatchObject({ kind: 'execution', executionId: 'ex1', host: 'h1' })
    expect(navs).toEqual([])
  })

  it('unparseable URL: corrected to the persisted active tab with one replace', () => {
    const { navs, current } = coldMount('/no/such/route')
    expect(useTabStore.getState().activeTabId).toBe(GLOBAL_TAB)
    expect(current()).toBe('/settings/appearance')
    expect(navs[0]).toEqual({ to: '/settings', replace: true })
  })

  it('workspace URL (/w/<ws>): corrected to the persisted active tab', () => {
    const { navs, current } = coldMount(`/w/${WS_X}`)
    expect(useTabStore.getState().activeTabId).toBe(GLOBAL_TAB)
    expect(current()).toBe('/settings/appearance')
    expect(navs[0]).toEqual({ to: '/settings', replace: true })
  })

  it('URL already matches the persisted active tab: nothing happens', () => {
    const { navs, current } = coldMount('/settings/appearance')
    expect(useTabStore.getState().activeTabId).toBe(GLOBAL_TAB)
    expect(current()).toBe('/settings/appearance')
    expect(navs).toEqual([])
  })
})
