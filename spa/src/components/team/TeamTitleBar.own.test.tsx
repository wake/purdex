// spa/src/components/team/TeamTitleBar.own.test.tsx — WA-2b-1b (round 3): a tab of no team whose `cc` conversation has a workbook
// gets the title-bar Notebook button and, in the `titlebar` state, a one-line strip (first sentence of the status).
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { TitleBar } from '../TitleBar'
import { TeamDisplayProvider } from './TeamDisplayProvider'
import { TeamPanelArea } from './TeamPanelArea'
import { HOST, resetTeamStores, seedScene } from '../../lib/team/__tests__/team-fixture'
import { seedWorkbook } from '../../lib/team/__tests__/workbook-fixture'
import { clearModuleRegistry } from '../../lib/module-registry'
import { useTabStore } from '../../stores/useTabStore'
import { useTeamUiStore } from '../../stores/useTeamUiStore'
import { useWorkbookStore } from '../../stores/useWorkbookStore'
import { useShownHostsStore } from '../../stores/useShownHostsStore'
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
const button = () => screen.queryByTestId('team-notebook-button')
const mountBar = () => render(<TeamDisplayProvider><TitleBar title="T" /><TeamPanelArea /></TeamDisplayProvider>)

beforeEach(() => {
  cleanup()
  localStorage.clear()
  resetTeamStores()
  useWorkbookStore.getState().reset()
  useShownHostsStore.setState({ ids: [HOST] })
  clearModuleRegistry()
  seedScene({ members: [['A', 'a-tm']], tabs: [['lead', 'lead-tm'], ['ma', 'a-tm'], ['x', null]], workspaces: [{ id: 'w1', tabs: ['lead', 'ma', 'x'] }], activeTabId: 'x' })
  addTab(ccTab('cc1', { type: 'cc', sessionId: 'S1' }))
  seedWorkbook(HOST, 'S1', { status: 'Wiring the login form. Next the tests.' })
})
afterEach(() => { cleanup(); vi.restoreAllMocks(); act(() => useI18nStore.getState().setLocale('en')) })

describe('the Notebook button for a tab of no team', () => {
  it('shows for a cc tab with a workbook, and not for a tab without one', () => {
    mountBar()
    expect(button()).toBeNull() // tab x: no conversation
    setActive('cc1')
    expect(button()).not.toBeNull()
  })
  it('is absent when the host lacks workbook.v1, the agent is not cc, or the conversation has no workbook', () => {
    addTab(ccTab('cx', { type: 'codex', sessionId: 'S1' }))
    addTab(ccTab('cy', { type: 'cc', sessionId: 'S2' }))
    mountBar()
    setActive('cx'); expect(button()).toBeNull()
    setActive('cy'); expect(button()).toBeNull()
    act(() => { seedWorkbook(HOST, 'S1', { status: 'x' }, { v1: false }) })
    setActive('cc1'); expect(button()).toBeNull()
  })
  it('lit in the title bar, dim in the pane; a press moves the shared value between the title bar and the pane state it left', () => {
    setActive('cc1')
    mountBar()
    expect(shared()).toBe('titlebar')
    expect(button()!.getAttribute('aria-pressed')).toBe('true')
    fireEvent.click(button()!)
    expect(shared()).toBe('full')
    expect(button()!.getAttribute('aria-pressed')).toBe('false')
    act(() => useTeamUiStore.getState().setSharedPanelMode('line'))
    fireEvent.click(button()!)
    expect(shared()).toBe('titlebar')
    fireEvent.click(button()!)
    expect(shared()).toBe('line') // back to where it left
  })
  it('a team tab keeps the team button (it moves the team value, not the shared one)', () => {
    setActive('lead')
    mountBar()
    fireEvent.click(button()!)
    expect(shared()).toBe('titlebar')
    expect(useTeamUiStore.getState().panelMode).not.toEqual({})
  })
})

describe('the strip for a tab of no team', () => {
  it('in the title bar: one line with the first sentence of the status; the window title stays', () => {
    setActive('cc1')
    mountBar()
    expect(screen.getByTestId('own-title-strip')).toBeTruthy()
    expect(screen.getByTestId('own-strip-line').textContent).toBe('Wiring the login form')
    expect(screen.getByText('T')).toBeTruthy()
    expect(screen.queryByTestId('team-title-strip')).toBeNull()
  })
  it('a click brings the area into the pane at full', () => {
    setActive('cc1')
    mountBar()
    fireEvent.click(screen.getByTestId('own-strip-line'))
    expect(shared()).toBe('full')
    expect(screen.queryByTestId('own-title-strip')).toBeNull()
    expect(screen.getByTestId('team-seat-workbook')).toBeTruthy()
  })
  it('is absent while the area is in the pane, and for a tab with no workbook', () => {
    act(() => useTeamUiStore.getState().setSharedPanelMode('line'))
    setActive('cc1')
    mountBar()
    expect(screen.queryByTestId('own-title-strip')).toBeNull()
    act(() => useTeamUiStore.getState().setSharedPanelMode('titlebar'))
    setActive('x')
    expect(screen.queryByTestId('own-title-strip')).toBeNull()
  })
  it('the strip follows the status', () => {
    setActive('cc1')
    mountBar()
    act(() => { seedWorkbook(HOST, 'S1', { status: 'Reviewing the diff! More later.' }) })
    expect(screen.getByTestId('own-strip-line').textContent).toBe('Reviewing the diff')
  })
})
