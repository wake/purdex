// The workbook view's scroll box follows the panel area's height (no fixed cap): full and max both. jsdom measures no layout, so this
// asserts the CSS chain instead — the scroll box is flex-1 / min-h-0 / overflow-y-auto with no max-h-*, and every container between it
// and the area is a flex column that may shrink (min-h-0), under an area whose own height is bounded (full: max-h of the pane, max: inset).
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { act, cleanup, render, screen } from '@testing-library/react'
import { TeamDisplayProvider } from './TeamDisplayProvider'
import { TeamPanelArea } from './TeamPanelArea'
import { HOST, KEY, resetTeamStores, seedScene } from '../../lib/team/__tests__/team-fixture'
import { entry, seedWorkbook } from '../../lib/team/__tests__/workbook-fixture'
import { useTabStore } from '../../stores/useTabStore'
import { useTeamUiStore } from '../../stores/useTeamUiStore'
import { useWorkbookStore } from '../../stores/useWorkbookStore'
import { useI18nStore } from '../../stores/useI18nStore'
import type { Tab } from '../../types/tab'

const fetchConversation = vi.hoisted(() => vi.fn())
vi.mock('../../lib/workbook/api', async (orig) => ({ ...(await orig<typeof import('../../lib/workbook/api')>()), fetchConversation }))
vi.mock('./TeamSeatIcon', () => ({
  TeamSeatIcon: () => <span />,
  TeamSeatHostBadge: () => <span />,
}))

const ccTab = (id: string, sessionId: string): Tab => ({
  id, pinned: false, locked: false, createdAt: 0,
  layout: { type: 'leaf', pane: { id: `p-${id}`, content: {
    kind: 'tmux-session', hostId: HOST, sessionCode: `code-${id}`, mode: 'terminal', cachedName: id, tmuxInstance: 'i',
    rebuild: { sessionName: id, tmuxInstance: 'i', agent: { type: 'cc', sessionId, updatedAt: 1 }, capturedAt: 1 },
  } } },
})
const cls = (el: Element) => el.className.split(/\s+/)
const mount = () => render(<TeamDisplayProvider><TeamPanelArea /></TeamDisplayProvider>)

/** The elements from the scroll box's parent up to (not including) the area. */
function chain(): Element[] {
  const area = screen.getByTestId('team-panel-area')
  const out: Element[] = []
  for (let el = screen.getByTestId('workbook-body').parentElement; el && el !== area; el = el.parentElement) out.push(el)
  return out
}
function expectFollowsArea(mode: 'full' | 'max') {
  const body = screen.getByTestId('workbook-body')
  for (const c of ['flex-1', 'min-h-0', 'overflow-y-auto']) expect(cls(body)).toContain(c)
  expect(body.className).not.toMatch(/(^|\s)max-h-/)
  const links = chain()
  expect(links.length).toBeGreaterThan(1)
  for (const el of links) {
    expect(cls(el), el.getAttribute('data-testid') ?? el.className).toEqual(expect.arrayContaining(['flex', 'flex-col', 'min-h-0']))
  }
  const area = screen.getByTestId('team-panel-area')
  if (mode === 'max') expect(cls(area)).toContain('inset-3') // a definite height: the pane's
  else expect(area.className).toMatch(/max-h-\[calc\(100%-12px\)\]/) // bounded by the pane
}

beforeEach(() => {
  cleanup()
  localStorage.clear()
  resetTeamStores()
  useWorkbookStore.getState().reset()
  fetchConversation.mockReset()
  fetchConversation.mockReturnValue(new Promise(() => {}))
  useI18nStore.getState().setLocale('zh-TW')
})
afterEach(() => cleanup())

describe('a tab\'s own workbook', () => {
  const own = (mode: 'full' | 'max') => {
    seedScene({ members: [['A', 'a-tm']], tabs: [['lead', 'lead-tm'], ['ma', 'a-tm'], ['x', null]], workspaces: [{ id: 'w1', tabs: ['lead', 'ma', 'x'] }], activeTabId: 'x' })
    act(() => {
      const tab = ccTab('cc1', 'S1')
      useTabStore.setState((s) => ({ tabs: { ...s.tabs, [tab.id]: tab }, tabOrder: [...s.tabOrder, tab.id], activeTabId: 'cc1' }))
      useTeamUiStore.getState().setSharedPanelMode(mode)
    })
    seedWorkbook(HOST, 'S1', { status: 's', entries: [entry(2)] })
    mount()
  }
  it('full: the scroll box and its containers follow the area', () => { own('full'); expectFollowsArea('full') })
  it('max: fills the pane', () => { own('max'); expectFollowsArea('max') })
})

describe('a team\'s drilled-in seat workbook', () => {
  const drilled = (mode: 'full' | 'max') => {
    seedScene({ members: [['A', 'a-tm']], tabs: [['lead', 'lead-tm'], ['ma', 'a-tm']], workspaces: [{ id: 'w1', tabs: ['lead', 'ma'] }], activeTabId: 'lead' })
    seedWorkbook(HOST, 'A', { status: 's', entries: [entry(2)] })
    useTeamUiStore.getState().setPanelMode(KEY, mode)
    useTeamUiStore.getState().setTeamDrill(KEY, { hostId: HOST, sessionId: 'A' })
    mount()
  }
  it('full', () => { drilled('full'); expectFollowsArea('full') })
  it('max', () => { drilled('max'); expectFollowsArea('max') })
})
