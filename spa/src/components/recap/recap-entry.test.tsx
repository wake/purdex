// spa/src/components/recap/recap-entry.test.tsx — where the recap shows up in the shell (plan T14b): a team row's second line and clock, the title bar's button, the personal card,
// the 「已結束」 group, and the history view that takes the panel area's place. Real stores and components (the icon pieces are mocked, as in the other panel tests); the daemon's
// dialogue routes are mocked.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import type { Recap, RecapsResponse, TurnSummary } from '../../lib/dialogue/types'

const fetchRecaps = vi.fn<(h: string, s: string, limit?: number) => Promise<RecapsResponse>>()
const fetchTurnSummaries = vi.fn<(h: string, s: string, r?: { from?: number; to?: number; limit?: number }) => Promise<TurnSummary[]>>()
const requestRecap = vi.fn()
vi.mock('../../lib/dialogue/api', () => ({
  fetchRecaps: (...a: [string, string, number?]) => fetchRecaps(...a),
  fetchTurnSummaries: (...a: [string, string, { from?: number; to?: number; limit?: number }?]) => fetchTurnSummaries(...a),
  requestRecap: (...a: unknown[]) => requestRecap(...a),
}))
vi.mock('../team/TeamSeatIcon', () => ({
  TeamSeatIcon: () => <span data-testid="seat-icon" />,
  TeamSeatHostBadge: () => <span data-testid="seat-host" />,
}))
vi.mock('../../lib/task-groups/session-roots-lifecycle', () => ({ setRootsInterest: () => {} }))

import { TitleBar } from '../TitleBar'
import { TeamDisplayProvider } from '../team/TeamDisplayProvider'
import { TeamPanelArea } from '../team/TeamPanelArea'
import { HOST, KEY, resetTeamStores, seedScene } from '../../lib/team/__tests__/team-fixture'
import { clearModuleRegistry } from '../../lib/module-registry'
import { useDialogueStore } from '../../stores/useDialogueStore'
import { useRecapViewStore } from '../../stores/useRecapViewStore'
import { useTabStore } from '../../stores/useTabStore'
import { useTeamUiStore } from '../../stores/useTeamUiStore'
import { useShownHostsStore } from '../../stores/useShownHostsStore'
import { useManualGroupsStore } from '../../stores/useManualGroupsStore'
import { useRootCacheStore } from '../../lib/task-groups/root-cache'
import { usePersonaStore } from '../../stores/usePersonaStore'
import { useI18nStore } from '../../stores/useI18nStore'
import type { Tab } from '../../types/tab'

const at = (h: number, m: number) => new Date(2026, 10, 1, h, m).getTime()
const recap = (id: number, over: Partial<Recap> = {}): Recap =>
  ({ id, at: at(16, 5), from_no: 1, to_no: 30, trigger: 'auto', headline: `headline ${id}`, done: ['d'], now: 'n', decided: [], waiting: [], next: 'x', ...over })
const resp = (recaps: Recap[], over: Partial<RecapsResponse> = {}): RecapsResponse =>
  ({ recaps, can_generate: true, newest_turn_no: recaps[0]?.to_no ?? 0, last_recap_to_no: recaps[0]?.to_no ?? 0, ...over })
const turn = (n: number): TurnSummary => ({ turn_no: n, at: at(15, n), title: `turn title ${n}`, summary: `s${n}`, push: '' })
const settle = async () => { await act(async () => { await new Promise((r) => setTimeout(r, 0)) }) }

/** Per-session answers: the lead 'L' has a recap, the member 'A' has only turn summaries, the personal 'S1' has a recap. */
function answers(map: Record<string, RecapsResponse>, turns: Record<string, TurnSummary[]> = {}) {
  fetchRecaps.mockImplementation(async (_h, s) => map[s] ?? resp([], { newest_turn_no: 0 }))
  fetchTurnSummaries.mockImplementation(async (_h, s) => turns[s] ?? [])
}

const ccTab = (id: string, agent: { type: string; sessionId?: string }, terminated?: string): Tab => ({
  id, pinned: false, locked: false, createdAt: 0,
  layout: { type: 'leaf', pane: { id: `p-${id}`, content: {
    kind: 'tmux-session', hostId: HOST, sessionCode: `code-${id}`, mode: 'terminal', cachedName: id, tmuxInstance: 'i', ...(terminated ? { terminated } : {}),
    rebuild: { sessionName: id, tmuxInstance: 'i', agent: { ...agent, updatedAt: 1 }, capturedAt: 1 },
  } } },
}) as Tab
const addTab = (tab: Tab) => act(() => { useTabStore.setState((s) => ({ tabs: { ...s.tabs, [tab.id]: tab }, tabOrder: [...s.tabOrder, tab.id] })) })
const setActive = (id: string | null) => act(() => { useTabStore.setState({ activeTabId: id }) })
const mount = () => render(<TeamDisplayProvider><TitleBar title="T" /><TeamPanelArea /></TeamDisplayProvider>)
const row = (sessionId: string) => screen.getAllByTestId('team-panel-row').find((r) => r.getAttribute('data-session-id') === sessionId)!

beforeEach(() => {
  cleanup()
  localStorage.clear()
  fetchRecaps.mockReset(); fetchTurnSummaries.mockReset(); requestRecap.mockReset()
  requestRecap.mockResolvedValue('up_to_date')
  resetTeamStores()
  useTeamUiStore.setState({ endedSeats: {} }) // the fixture's reset leaves the ended seats
  useRecapViewStore.getState().close()
  useDialogueStore.getState().reset()
  useDialogueStore.getState().setSupport(HOST, true)
  useManualGroupsStore.setState({ order: [], groups: {} })
  useRootCacheStore.setState({ roots: {}, addresses: {}, titles: {} })
  usePersonaStore.setState({ byHost: {} })
  useShownHostsStore.setState({ ids: [HOST] })
  clearModuleRegistry()
  useI18nStore.getState().setLocale('zh-TW')
  seedScene({ members: [['A', 'a-tm']], tabs: [['lead', 'lead-tm'], ['ma', 'a-tm'], ['x', null]], workspaces: [{ id: 'w1', tabs: ['lead', 'ma', 'x'] }], activeTabId: 'lead' })
})
afterEach(() => { cleanup() })

describe('a team row', () => {
  it('second line: the headline with 截至 HH:MM, and 「回合 N」 plus the latest turn title for a seat with no recap yet', async () => {
    answers({ L: resp([recap(1, { headline: 'lead headline' })]), A: resp([], { newest_turn_no: 7 }) }, { A: [turn(7)] })
    mount()
    await settle()
    expect(within(row('L')).getByTestId('recap-line')).toHaveTextContent('lead headline')
    expect(within(row('L')).getByTestId('recap-line-asof')).toHaveTextContent('截至 16:05')
    expect(within(row('A')).getByTestId('recap-line-turn')).toHaveTextContent('回合 7')
    expect(within(row('A')).getByTestId('recap-line')).toHaveTextContent('turn title 7')
  })

  it('the clock at the row\'s end opens that seat\'s history in the area\'s place; back returns to the team', async () => {
    answers({ L: resp([recap(1)]), A: resp([recap(2, { headline: 'member headline' })]) })
    mount()
    await settle()
    expect(screen.getByTestId('team-panel')).toBeTruthy()
    fireEvent.click(within(row('A')).getByTestId('recap-row-button'))
    await settle()
    expect(screen.queryByTestId('team-panel')).toBeNull()
    expect(screen.getByTestId('recap-title')).toHaveTextContent('title A 的 recap')
    expect(screen.getAllByTestId('recap-item')[0]).toHaveTextContent('member headline')
    expect(useTabStore.getState().activeTabId).toBe('lead') // the clock did not switch tabs
    fireEvent.click(screen.getByTestId('recap-back'))
    expect(screen.getByTestId('team-panel')).toBeTruthy()
    expect(screen.queryByTestId('recap-view')).toBeNull()
  })

  it('a click on the second line opens the history and does not switch tabs', async () => {
    answers({ L: resp([recap(1)]), A: resp([recap(2)]) })
    mount()
    await settle()
    fireEvent.click(within(row('A')).getByTestId('recap-line'))
    await settle()
    expect(screen.getByTestId('recap-view')).toBeTruthy()
    expect(useTabStore.getState().activeTabId).toBe('lead')
  })

  it('a host without dialogue.v1: no line, no clock, no request', async () => {
    useDialogueStore.getState().setSupport(HOST, false)
    answers({ L: resp([recap(1)]) })
    mount()
    await settle()
    expect(screen.queryByTestId('recap-line')).toBeNull()
    expect(screen.queryByTestId('recap-row-button')).toBeNull()
    expect(screen.queryByTestId('recap-button')).toBeNull()
    expect(fetchRecaps).not.toHaveBeenCalled()
  })
})

describe('the title bar button', () => {
  it('opens the recap of the seat the active tab shows, and is lit while it is shown; a second press closes', async () => {
    answers({ L: resp([recap(1, { headline: 'lead headline' })]) })
    mount()
    await settle()
    const btn = screen.getByTestId('recap-button')
    expect(btn).toHaveAttribute('aria-pressed', 'false')
    fireEvent.click(btn)
    await settle()
    expect(screen.getByTestId('recap-title')).toHaveTextContent('title L 的 recap')
    expect(screen.getByTestId('recap-button')).toHaveAttribute('aria-pressed', 'true')
    fireEvent.click(screen.getByTestId('recap-button'))
    expect(screen.queryByTestId('recap-view')).toBeNull()
    expect(screen.getByTestId('recap-button')).toHaveAttribute('aria-pressed', 'false')
  })

  it('works while the panel sits in the title bar: it brings the panel out with the history in it', async () => {
    answers({ L: resp([recap(1)]) })
    act(() => { useTeamUiStore.getState().setPanelMode(KEY, 'titlebar') })
    mount()
    await settle()
    expect(screen.queryByTestId('team-panel-area')).toBeNull()
    fireEvent.click(screen.getByTestId('recap-button'))
    await settle()
    expect(screen.getByTestId('recap-view')).toBeTruthy()
    expect(useTeamUiStore.getState().panelMode[KEY]).toBeUndefined() // full
  })

  it('a dot marks a recap being written', async () => {
    answers({ L: resp([recap(1)]) })
    mount()
    await settle()
    expect(screen.queryByTestId('recap-button-dot')).toBeNull()
    act(() => { useDialogueStore.setState((s) => ({ byHost: { [HOST]: { ...s.byHost[HOST], L: { ...s.byHost[HOST].L, generating: true } } } })) })
    expect(screen.getByTestId('recap-button-dot')).toBeTruthy()
  })

  it('is not drawn for a tab that shows no session', async () => {
    answers({})
    setActive('x')
    mount()
    await settle()
    expect(screen.queryByTestId('recap-button')).toBeNull()
  })
})

describe('the 「已結束」 group', () => {
  it('lists the seats that left; a click opens its history, where nothing new can be made', async () => {
    answers({ L: resp([recap(1)]), GONE: resp([recap(3, { headline: 'last words' })], { can_generate: false, newest_turn_no: 36 }) }, { GONE: [turn(31), turn(32), turn(33), turn(34), turn(35), turn(36)] })
    act(() => { useTeamUiStore.getState().recordEndedSeats(KEY, [{ hostId: HOST, sessionId: 'GONE', title: 'gone-title', endedAt: 5 }]) })
    mount()
    await settle()
    const group = screen.getByTestId('team-panel-ended')
    expect(group).toHaveTextContent('已結束 (1)')
    expect(within(group).queryByTestId('team-panel-ended-row')).toBeNull() // folded
    fireEvent.click(within(group).getByRole('button', { name: /已結束/ }))
    fireEvent.click(within(group).getByTestId('team-panel-ended-row'))
    await settle()
    expect(screen.getByTestId('recap-title')).toHaveTextContent('gone-title 的 recap')
    expect(screen.getAllByTestId('recap-item')[0]).toHaveTextContent('last words')
    expect((screen.getByTestId('recap-regen') as HTMLButtonElement).disabled).toBe(true)
    expect(screen.getByTestId('recap-status')).toHaveTextContent('session 已結束，無法產生新的 recap')
    expect(requestRecap).not.toHaveBeenCalled()
  })

  it('is absent when the host does not list dialogue.v1, or nobody left', async () => {
    answers({ L: resp([recap(1)]) })
    mount()
    await settle()
    expect(screen.queryByTestId('team-panel-ended')).toBeNull() // nobody left
    cleanup()
    act(() => { useTeamUiStore.getState().recordEndedSeats(KEY, [{ hostId: HOST, sessionId: 'GONE', title: 'gone-title', endedAt: 5 }]) })
    useDialogueStore.getState().setSupport(HOST, false)
    mount()
    await settle()
    expect(screen.queryByTestId('team-panel-ended')).toBeNull()
  })
})

describe('a personal session', () => {
  const scene = () => { addTab(ccTab('cc1', { type: 'cc', sessionId: 'S1' })); setActive('cc1'); act(() => { useTeamUiStore.getState().setSharedPanelMode('full') }) }

  it('the card\'s second line and the avatar cell\'s tooltip carry the headline', async () => {
    answers({ S1: resp([recap(1, { headline: 'solo headline' })]) })
    scene()
    mount()
    await settle()
    expect(within(screen.getByTestId('personal-card')).getByTestId('recap-line')).toHaveTextContent('solo headline')
    expect(within(screen.getByTestId('personal-card')).getByTestId('recap-line-asof')).toHaveTextContent('截至 16:05')
    act(() => { useTeamUiStore.getState().setSharedPanelMode('line') })
    expect(screen.getByTestId('personal-cell').getAttribute('title')).toContain('solo headline')
  })

  it('the title bar button opens the history inside the personal panel; back returns to the card', async () => {
    answers({ S1: resp([recap(1)]) })
    scene()
    mount()
    await settle()
    fireEvent.click(screen.getByTestId('recap-button'))
    await settle()
    expect(screen.getByTestId('recap-title')).toHaveTextContent('cc1 的 recap')
    expect(screen.queryByTestId('personal-card')).toBeNull()
    expect(screen.getByTestId('recap-button')).toHaveAttribute('aria-pressed', 'true')
    fireEvent.click(screen.getByTestId('recap-back'))
    expect(screen.getByTestId('personal-card')).toBeTruthy()
  })

  it('the title bar button also works while the personal panel sits in the title bar: it brings it out', async () => {
    answers({ S1: resp([recap(1)]) })
    scene()
    act(() => { useTeamUiStore.getState().setSharedPanelMode('titlebar') })
    mount()
    await settle()
    fireEvent.click(screen.getByTestId('recap-button'))
    await settle()
    expect(useTeamUiStore.getState().sharedPanelMode).toBe('full')
    expect(screen.getByTestId('recap-view')).toBeTruthy()
  })

  it('an ended session: the line is the last one it left, and a new recap cannot be made', async () => {
    answers({ S1: resp([recap(1, { headline: 'final words' })], { can_generate: false, newest_turn_no: 34, last_recap_to_no: 30 }) }, { S1: [turn(31), turn(32), turn(33), turn(34)] })
    addTab(ccTab('cc1', { type: 'cc', sessionId: 'S1' }, 'tmux-restarted')); setActive('cc1'); act(() => { useTeamUiStore.getState().setSharedPanelMode('full') })
    mount()
    await settle()
    expect(within(screen.getByTestId('personal-card')).getByTestId('recap-line')).toHaveTextContent('最後：final words')
    fireEvent.click(screen.getByTestId('recap-button'))
    await settle()
    expect((screen.getByTestId('recap-regen') as HTMLButtonElement).disabled).toBe(true)
    expect(screen.getByTestId('recap-after')).toHaveTextContent('結束前最後 4 回合沒有寫進 recap')
  })

  it('a host without dialogue.v1: nothing of it', async () => {
    useDialogueStore.getState().setSupport(HOST, false)
    answers({ S1: resp([recap(1)]) })
    scene()
    mount()
    await settle()
    expect(screen.queryByTestId('recap-line')).toBeNull()
    expect(screen.queryByTestId('recap-button')).toBeNull()
  })
})
