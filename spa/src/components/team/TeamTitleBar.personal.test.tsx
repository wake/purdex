// spa/src/components/team/TeamTitleBar.personal.test.tsx — task groups spec §7 (TG-5b): a tab of no team that shows an agent session gets the title-bar 「個人」 button and, in the `titlebar` state, the strip
// (the group's capsule and the session's cell).
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { TitleBar } from '../TitleBar'
import { TeamDisplayProvider } from './TeamDisplayProvider'
import { TeamPanelArea } from './TeamPanelArea'
import { HOST, resetTeamStores, seedScene } from '../../lib/team/__tests__/team-fixture'
import { clearModuleRegistry } from '../../lib/module-registry'
import { useTabStore } from '../../stores/useTabStore'
import { useTeamUiStore } from '../../stores/useTeamUiStore'
import { useShownHostsStore } from '../../stores/useShownHostsStore'
import { useManualGroupsStore } from '../../stores/useManualGroupsStore'
import { useRootCacheStore } from '../../lib/task-groups/root-cache'
import { useI18nStore } from '../../stores/useI18nStore'
import type { Tab } from '../../types/tab'

vi.mock('./TeamSeatIcon', () => ({
  TeamSeatIcon: () => <span data-testid="seat-icon" />,
  TeamSeatHostBadge: () => <span data-testid="seat-host" />,
}))

const ccTab = (id: string, agent: { type: string; sessionId?: string }): Tab => ({
  id, pinned: false, locked: false, createdAt: 0,
  layout: { type: 'leaf', pane: { id: `p-${id}`, content: {
    kind: 'tmux-session', hostId: HOST, sessionCode: `code-${id}`, mode: 'terminal', cachedName: id, tmuxInstance: 'i',
    rebuild: { sessionName: id, tmuxInstance: 'i', agent: { ...agent, updatedAt: 1 }, capturedAt: 1 },
  } } },
})
const addTab = (tab: Tab) => act(() => { useTabStore.setState((s) => ({ tabs: { ...s.tabs, [tab.id]: tab }, tabOrder: [...s.tabOrder, tab.id] })) })
const setActive = (id: string | null) => act(() => { useTabStore.setState({ activeTabId: id }) })
const shared = () => useTeamUiStore.getState().sharedPanelMode
const personalButton = () => screen.queryByTestId('personal-button')
const mountBar = () => render(<TeamDisplayProvider><TitleBar title="T" /><TeamPanelArea /></TeamDisplayProvider>)

beforeEach(() => {
  cleanup()
  localStorage.clear()
  resetTeamStores()
  useManualGroupsStore.setState({ order: [], groups: {} })
  useRootCacheStore.setState({ roots: {}, addresses: {} })
  useShownHostsStore.setState({ ids: [HOST] })
  clearModuleRegistry()
  useI18nStore.getState().setLocale('zh-TW')
  seedScene({ members: [['A', 'a-tm']], tabs: [['lead', 'lead-tm'], ['ma', 'a-tm'], ['x', null]], workspaces: [{ id: 'w1', tabs: ['lead', 'ma', 'x'] }], activeTabId: 'x' })
  addTab(ccTab('cc1', { type: 'cc', sessionId: 'S1' }))
})
afterEach(() => { cleanup(); vi.restoreAllMocks(); act(() => useI18nStore.getState().setLocale('en')) })

describe('the 「個人」 button', () => {
  it('shows for a tab that shows an agent session — a codex one too — and not for a tab without one', () => {
    addTab(ccTab('cx', { type: 'codex', sessionId: 'S2' }))
    addTab(ccTab('cn', { type: 'cc' }))
    mountBar()
    expect(personalButton()).toBeNull() // tab x: no session
    setActive('cc1'); expect(personalButton()).not.toBeNull()
    setActive('cx'); expect(personalButton()).not.toBeNull()
    setActive('cn'); expect(personalButton()).toBeNull() // the pane recorded no session id
  })
  it('lit in the title bar, dim in the pane; a press moves the shared value between the title bar and the pane state it left', () => {
    setActive('cc1')
    mountBar()
    expect(shared()).toBe('titlebar')
    expect(personalButton()!.getAttribute('aria-pressed')).toBe('true')
    fireEvent.click(personalButton()!)
    expect(shared()).toBe('full')
    expect(personalButton()!.getAttribute('aria-pressed')).toBe('false')
    act(() => useTeamUiStore.getState().setSharedPanelMode('line'))
    fireEvent.click(personalButton()!)
    expect(shared()).toBe('titlebar')
    fireEvent.click(personalButton()!)
    expect(shared()).toBe('line') // back to where it left
  })
  it('a team tab keeps the team button (it moves the team value, not the shared one)', () => {
    setActive('lead')
    mountBar()
    expect(personalButton()).toBeNull()
    fireEvent.click(screen.getByTestId('team-notebook-button'))
    expect(shared()).toBe('titlebar')
    expect(useTeamUiStore.getState().panelMode).not.toEqual({})
  })
})

describe('the strip', () => {
  it('in the title bar: the group\'s capsule and the session\'s cell; the window title stays', () => {
    setActive('cc1')
    mountBar()
    expect(screen.getByTestId('personal-title-strip')).toBeTruthy()
    expect(screen.getByTestId('personal-strip-capsule').textContent).toBe('未分組')
    expect(screen.getByTestId('personal-strip-cell').querySelector('[data-testid="personal-cell"]')).toBeTruthy()
    expect(screen.getByText('T')).toBeTruthy()
    expect(screen.queryByTestId('team-title-strip')).toBeNull()
  })
  it('the capsule follows the group the session is filed in', () => {
    setActive('cc1')
    mountBar()
    act(() => {
      useManualGroupsStore.setState({ order: ['g1'], groups: { g1: { name: 'iOS 發版', members: [{ host: HOST, root: 'R' }], createdAt: 0 } } })
      useRootCacheStore.getState().ingest(HOST, [{ session_id: 'S1', root_session_id: 'R' }])
    })
    expect(screen.getByTestId('personal-strip-capsule').textContent).toBe('iOS 發版')
  })
  it('a click on the capsule restores the mode the area left', () => {
    setActive('cc1')
    mountBar()
    fireEvent.click(screen.getByTestId('personal-strip-capsule'))
    expect(shared()).toBe('full')
    expect(screen.queryByTestId('personal-title-strip')).toBeNull()
    expect(screen.getByTestId('personal-panel')).toBeTruthy()
  })
  it.each(['line', 'full'] as const)('from %s: the strip and the button both give back that mode, again on a second round trip', (mode) => {
    setActive('cc1')
    mountBar()
    act(() => useTeamUiStore.getState().setSharedPanelMode(mode))
    for (const press of [() => fireEvent.click(personalButton()!), () => fireEvent.click(screen.getByTestId('personal-strip-capsule'))]) {
      fireEvent.click(personalButton()!) // into the title bar
      expect(shared()).toBe('titlebar')
      press()
      expect(shared()).toBe(mode)
      expect(useTeamUiStore.getState().sharedPanelLast).toBe(mode)
    }
  })
  it('is absent while the area is in the pane, and for a tab with no session', () => {
    act(() => useTeamUiStore.getState().setSharedPanelMode('line'))
    setActive('cc1')
    mountBar()
    expect(screen.queryByTestId('personal-title-strip')).toBeNull()
    act(() => useTeamUiStore.getState().setSharedPanelMode('titlebar'))
    setActive('x')
    expect(screen.queryByTestId('personal-title-strip')).toBeNull()
  })
})
