// #1326: route-sync × GlobalSettingsPage self-heal ping-pong.
//
// When the ACTIVE tab is the global Settings tab and the URL moves to a
// non-/settings route (direct URL / pushState), there is one commit where the
// location has already changed but useRouteSync's URL→Tab effect has not yet
// switched the active tab. The still-mounted GlobalSettingsPage used to read
// that foreign URL as "/settings with no section" and rewrite it back to
// /settings/<section>, which route-sync then answered by re-activating the
// global tab — "Maximum update depth exceeded".
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

import { describe, it, expect, beforeEach } from 'vitest'
import { act, render } from '@testing-library/react'
import { Router } from 'wouter'
import { memoryLocation } from 'wouter/memory-location'
import { useRouteSync } from './useRouteSync'
import { useTabStore } from '../stores/useTabStore'
import { useHistoryStore } from '../stores/useHistoryStore'
import { useWorkspaceStore } from '../features/workspace'
import { getPrimaryPane } from '../lib/pane-tree'
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

const Appearance = () => <div>AppearanceBody</div>
const Terminal = () => <div>TerminalBody</div>

/**
 * Mirrors what TabContent + PaneLayoutRenderer mount: the active tab's
 * settings pane, plus (when `keepAlive`) every other settings tab as a hidden
 * inactive instance — the keepAliveCount > 0 pool.
 */
function Harness({ keepAlive }: { keepAlive: boolean }) {
  useRouteSync()
  const tabs = useTabStore((s) => s.tabs)
  const activeTabId = useTabStore((s) => s.activeTabId)
  const ids = keepAlive ? Object.keys(tabs) : activeTabId ? [activeTabId] : []
  return (
    <>
      {ids.map((id) => {
        const tab = tabs[id]
        if (!tab) return null
        const pane = getPrimaryPane(tab.layout)
        if (pane.content.kind !== 'settings') return null
        return <SettingsPage key={id} pane={pane} isActive={id === activeTabId} />
      })}
    </>
  )
}

function seed(extraTabs: Tab[] = []) {
  const globalTab = makeTab(GLOBAL_TAB, { kind: 'settings', scope: 'global' })
  const all = [globalTab, ...extraTabs]
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

function mount(path: string, keepAlive = false) {
  const mem = memoryLocation({ path, record: true })
  const view = render(
    <Router hook={mem.hook}>
      <Harness keepAlive={keepAlive} />
    </Router>,
  )
  return { ...view, mem, current: () => mem.history[mem.history.length - 1] }
}

function activeContent(): PaneContent | null {
  const s = useTabStore.getState()
  const tab = s.activeTabId ? s.tabs[s.activeTabId] : null
  return tab ? getPrimaryPane(tab.layout).content : null
}

describe('useRouteSync × GlobalSettingsPage (#1326)', () => {
  beforeEach(() => {
    resetLastSection()
    clearSettingsSectionRegistry()
    clearContributions()
    registerSettingsSection({ id: 'appearance', label: 'Appearance', order: 0, component: Appearance })
    registerSettingsSection({ id: 'terminal', label: 'Terminal', order: 1, component: Terminal })
    dispatchSettingsContributions([])
    useHistoryStore.setState({ browseHistory: [], closedTabs: [] })
  })

  it('navigating to /w/<ws>/settings while global Settings is active settles on the workspace settings tab', () => {
    seed()
    const { mem, current } = mount('/settings/appearance')
    expect(current()).toBe('/settings/appearance')
    const before = mem.history.length

    act(() => {
      mem.navigate(`/w/${WS_X}/settings`)
    })

    expect(current()).toBe(`/w/${WS_X}/settings`)
    expect(activeContent()).toEqual({ kind: 'settings', scope: { workspaceId: WS_X } })
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBe(WS_X)
    // No ping-pong: only the push itself landed in history.
    expect(mem.history.length - before).toBeLessThanOrEqual(1)
  })

  it('a kept-alive (inactive) global Settings pane does not pull the URL back when another tab is activated', () => {
    seed([
      makeTab(SESSION_TAB, {
        kind: 'tmux-session', hostId: 'h', sessionCode: 's', mode: 'terminal', cachedName: '', tmuxInstance: '',
      }),
    ])
    const { current } = mount('/settings/appearance', true)
    expect(current()).toBe('/settings/appearance')

    act(() => {
      useTabStore.getState().setActiveTab(SESSION_TAB)
    })

    expect(useTabStore.getState().activeTabId).toBe(SESSION_TAB)
    expect(current()).toBe(`/t/${SESSION_TAB}/terminal`)
  })

  it('re-activating a kept-alive global Settings pane restores its section URL', () => {
    seed([
      makeTab(SESSION_TAB, {
        kind: 'tmux-session', hostId: 'h', sessionCode: 's', mode: 'terminal', cachedName: '', tmuxInstance: '',
      }),
    ])
    const { current } = mount('/settings/appearance', true)
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
