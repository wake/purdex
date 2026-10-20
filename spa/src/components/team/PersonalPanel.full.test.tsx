// spa/src/components/team/PersonalPanel.full.test.tsx — the personal panel's full form (task groups spec §7, TG-5c; design R12–R24): the card, the settings (move, take out, new group, name, colour), an unread root,
// an ended session, the workbook button. Real stores; only the seat icon (a store-driven light) is stubbed.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { TeamDisplayProvider } from './TeamDisplayProvider'
import { TeamPanelArea } from './TeamPanelArea'
import { TEAM_COLORS } from './team-display'
import { HOST, resetTeamStores, seedScene } from '../../lib/team/__tests__/team-fixture'
import { seedUnit, setJournalHost } from '../../lib/journal/__tests__/journal-fixture'
import { readViewMemo } from '../../lib/journal/view-memory'
import { seedWorkbook } from '../../lib/team/__tests__/workbook-fixture'
import { useTabStore } from '../../stores/useTabStore'
import { useTeamUiStore } from '../../stores/useTeamUiStore'
import { useManualGroupsStore } from '../../stores/useManualGroupsStore'
import { useWorkbookStore } from '../../stores/useWorkbookStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { useRootCacheStore } from '../../lib/task-groups/root-cache'
import { manualGroupKey } from '../../lib/task-groups/group-key'
import type { Tab } from '../../types/tab'

vi.mock('./TeamSeatIcon', () => ({
  TeamSeatIcon: (p: { light?: boolean }) => <span data-testid="seat-icon" data-light={String(p.light !== false)} />,
  TeamSeatHostBadge: () => <span data-testid="seat-host" />,
}))

const A = 'aaaaaaaa'
const B = 'bbbbbbbb'
const ccTab = (id: string, agent: string, extra: Record<string, unknown> = {}): Tab => ({
  id, pinned: false, locked: false, createdAt: 0,
  layout: { type: 'leaf', pane: { id: `p-${id}`, content: {
    kind: 'tmux-session', hostId: HOST, sessionCode: `code-${id}`, mode: 'terminal', cachedName: id, tmuxInstance: 'i',
    rebuild: { sessionName: id, tmuxInstance: 'i', agent: { type: 'cc', sessionId: agent, updatedAt: 1 }, capturedAt: 1 }, ...extra,
  } } },
}) as Tab
const addTab = (tab: Tab) => act(() => { useTabStore.setState((s) => ({ tabs: { ...s.tabs, [tab.id]: tab }, tabOrder: [...s.tabOrder, tab.id] })) })
const setActive = (id: string) => act(() => { useTabStore.setState({ activeTabId: id }) })
const readRoot = (session: string, root: string) => act(() => { useRootCacheStore.getState().ingest(HOST, [{ session_id: session, root_session_id: root }]) })
const groups = () => useManualGroupsStore.getState().groups
const q = (id: string) => screen.getByTestId(id)

function scene() {
  seedScene({ members: [['A', 'a-tm']], tabs: [['lead', 'lead-tm'], ['x', null]], workspaces: [{ id: 'w1', tabs: ['lead', 'x'] }], activeTabId: 'x' })
  addTab(ccTab('cc1', 'S1'))
  setActive('cc1')
  act(() => { useTeamUiStore.getState().setSharedPanelMode('full') })
  render(<TeamDisplayProvider><TeamPanelArea /></TeamDisplayProvider>)
}

beforeEach(() => {
  cleanup()
  localStorage.clear()
  resetTeamStores()
  useWorkbookStore.getState().reset()
  useRootCacheStore.setState({ roots: {}, addresses: {}, titles: {} })
  useManualGroupsStore.setState({
    order: [A, B],
    groups: { [A]: { name: 'iOS 發版', color: TEAM_COLORS[3], members: [], createdAt: 1 }, [B]: { name: '文件整理', color: TEAM_COLORS[1], members: [], createdAt: 2 } },
  })
  useI18nStore.getState().setLocale('zh-TW')
})
afterEach(() => cleanup())

describe('the card (R12–R15)', () => {
  it('one card: name, title, context left, the last status line as its second line; no click action', () => {
    useRootCacheStore.getState().ingest(HOST, [{ session_id: 'S1', root_session_id: 'R1', address: 'mlab/x-q9z', title: 'purdex-iface-tg5' }])
    seedWorkbook(HOST, 'S1', { status: 'Wiring the login form. Next the tests.' })
    scene()
    expect(q('personal-card-name').textContent).toBe('purdex-iface-tg5') // no persona in this fixture: the pdx title takes the name's place (R15)
    expect(q('personal-card-ctx').textContent).toBe('—')
    expect(q('personal-card-line').textContent).toBe('Wiring the login form')
  })
  it('no status at all: the card is one line (R13)', () => {
    scene()
    expect(screen.queryByTestId('personal-card-line')).toBeNull()
  })
  it('an ended session keeps the panel, dim, with no light and 「最後：」 before the line (R2)', () => {
    seedWorkbook(HOST, 'S1', { status: 'Wiring the login form.' })
    seedScene({ members: [], tabs: [['x', null]], workspaces: [{ id: 'w1', tabs: ['x'] }], activeTabId: 'x' })
    addTab(ccTab('cc1', 'S1', { terminated: 'tmux-restarted' }))
    setActive('cc1')
    act(() => { useTeamUiStore.getState().setSharedPanelMode('full') })
    render(<TeamDisplayProvider><TeamPanelArea /></TeamDisplayProvider>)
    expect(q('personal-card').getAttribute('data-ended')).toBe('true')
    expect(q('personal-card-ended').textContent).toBe('已結束')
    expect(q('personal-card-line').textContent).toBe('最後：Wiring the login form')
    expect(screen.queryByTestId('team-panel-light-dot')).toBeNull()
  })
})

describe('the settings (R16–R21, R23)', () => {
  it('「未分組」 has the group row and a hint, no name or colour rows (R16)', () => {
    readRoot('S1', 'R1')
    scene()
    expect(q('personal-group-button').textContent).toContain('未分組')
    expect(q('personal-ungrouped-hint')).toBeTruthy()
    expect(screen.queryByTestId('personal-name-edit')).toBeNull()
    expect(screen.queryByTestId('personal-colors')).toBeNull()
    expect((q('personal-take-out') as HTMLButtonElement).disabled).toBe(true)
  })

  it('until the root is read, moving and taking out stay off and say why — and the capsule stays 「未分組」 (R23)', () => {
    scene()
    const btn = q('personal-group-button') as HTMLButtonElement
    expect(btn.disabled).toBe(true)
    expect(btn.title).toBe('這個 session 的來源還沒讀到，暫時不能編組')
    expect(q('team-panel-name').textContent).toBe('未分組')
    readRoot('S1', 'R1')
    expect(btn.disabled).toBe(false)
  })

  it('the list holds every manual group; one click moves the session there, last in that group (R17, R18)', async () => {
    readRoot('S1', 'R1')
    // two tabs already in 文件整理 (their roots are its members); the moved tab has an old rank that would put it first
    act(() => {
      useManualGroupsStore.setState({ groups: { ...groups(), [B]: { ...groups()[B], members: [{ host: HOST, root: 'RD1' }, { host: HOST, root: 'RD2' }] } } })
      useRootCacheStore.getState().ingest(HOST, [{ session_id: 'D1', root_session_id: 'RD1' }, { session_id: 'D2', root_session_id: 'RD2' }])
    })
    scene()
    addTab({ ...ccTab('d1', 'D1'), createdAt: 1 }); addTab({ ...ccTab('d2', 'D2'), createdAt: 2 })
    act(() => { useTabStore.getState().setTabGroupRanks([{ tabId: 'cc1', rank: 'a' }]) })
    fireEvent.click(q('personal-group-button'))
    expect(screen.getAllByTestId('personal-group-option').map((o) => o.textContent)).toEqual(['iOS 發版', '文件整理'])
    fireEvent.click(screen.getAllByTestId('personal-group-option')[1])
    await waitFor(() => expect(groups()[B].members).toContainEqual({ host: HOST, root: 'R1' }))
    expect(q('team-panel-name').textContent).toBe('文件整理')
    const { groupTabs } = await import('../../lib/task-groups/group-order')
    const { currentGroupOrderCtx } = await import('../../lib/task-groups/current')
    expect(groupTabs(manualGroupKey(B), currentGroupOrderCtx()).at(-1)).toBe('cc1')
  })

  it('「移出」 takes the session out to 「未分組」', async () => {
    readRoot('S1', 'R1')
    act(() => { useManualGroupsStore.setState({ groups: { ...groups(), [A]: { ...groups()[A], members: [{ host: HOST, root: 'R1' }] } } }) })
    scene()
    expect(q('team-panel-name').textContent).toBe('iOS 發版')
    fireEvent.click(q('personal-take-out'))
    await waitFor(() => expect(groups()[A].members).toEqual([]))
    expect(q('team-panel-name').textContent).toBe('未分組')
  })

  it('「新增任務編組」 starts 「任務編組 1」 in the first unused swatch colour with the name field open; Enter renames the whole group (R19, R20)', async () => {
    readRoot('S1', 'R1')
    scene()
    fireEvent.click(q('personal-group-button'))
    fireEvent.click(q('personal-group-new'))
    const input = (await screen.findByTestId('personal-name-input')) as HTMLInputElement
    expect(input.value).toBe('任務編組 1')
    const id = Object.keys(groups()).find((g) => g !== A && g !== B)!
    expect(groups()[id]).toMatchObject({ name: '任務編組 1', color: TEAM_COLORS[0], members: [{ host: HOST, root: 'R1' }] })
    fireEvent.change(input, { target: { value: 'TestFlight' } })
    fireEvent.keyDown(input, { key: 'Enter' })
    expect(groups()[id].name).toBe('TestFlight')
    expect(q('team-panel-name').textContent).toBe('TestFlight')
  })

  it('Esc and a click elsewhere drop the draft; a bad name is refused with a notice (R20)', () => {
    readRoot('S1', 'R1')
    act(() => { useManualGroupsStore.setState({ groups: { ...groups(), [A]: { ...groups()[A], members: [{ host: HOST, root: 'R1' }] } } }) })
    scene()
    fireEvent.click(q('personal-name-edit'))
    fireEvent.change(q('personal-name-input'), { target: { value: 'changed' } })
    fireEvent.keyDown(q('personal-name-input'), { key: 'Escape' })
    expect(groups()[A].name).toBe('iOS 發版')
    fireEvent.click(q('personal-name-edit'))
    fireEvent.change(q('personal-name-input'), { target: { value: 'changed' } })
    fireEvent.blur(q('personal-name-input'))
    expect(groups()[A].name).toBe('iOS 發版')
    fireEvent.click(q('personal-name-edit'))
    fireEvent.change(q('personal-name-input'), { target: { value: 'x'.repeat(70) } })
    fireEvent.keyDown(q('personal-name-input'), { key: 'Enter' })
    expect(groups()[A].name).toBe('iOS 發版')
    expect(q('personal-notice').textContent).toContain('1 到 64')
  })

  it('a colour is one click, for the whole group, and there is no grey choice (R21)', () => {
    readRoot('S1', 'R1')
    act(() => { useManualGroupsStore.setState({ groups: { ...groups(), [A]: { ...groups()[A], members: [{ host: HOST, root: 'R1' }] } } }) })
    scene()
    const swatches = screen.getAllByTestId('personal-color')
    expect(swatches.map((s) => s.getAttribute('data-color'))).toEqual([...TEAM_COLORS])
    fireEvent.click(swatches[5])
    expect(groups()[A].color).toBe(TEAM_COLORS[5])
    expect(q('team-panel-name').style.background).toBe('rgb(163, 163, 255)')
  })

  it('a double-click on the capsule of a manual group opens the full form with the name field open (R9)', async () => {
    readRoot('S1', 'R1')
    act(() => { useManualGroupsStore.setState({ groups: { ...groups(), [A]: { ...groups()[A], members: [{ host: HOST, root: 'R1' }] } } }) })
    scene()
    act(() => { useTeamUiStore.getState().setSharedPanelMode('line') })
    fireEvent.doubleClick(q('team-panel-name'))
    expect(useTeamUiStore.getState().sharedPanelMode).toBe('full')
    expect(((await screen.findByTestId('personal-name-input')) as HTMLInputElement).value).toBe('iOS 發版')
  })
  it('a double-click on 「未分組」 does nothing (R9)', () => {
    scene()
    act(() => { useTeamUiStore.getState().setSharedPanelMode('line') })
    fireEvent.doubleClick(q('team-panel-name'))
    expect(useTeamUiStore.getState().sharedPanelMode).toBe('line')
  })
})

describe('the workbook button on an older host (R14)', () => {
  it('opens the session\'s own workbook inside the panel; the back arrow returns to the card', () => {
    seedWorkbook(HOST, 'S1', { status: 'Wiring the login form.', entries: [] })
    scene()
    fireEvent.click(q('personal-card-workbook'))
    expect(screen.getByTestId('personal-workbook')).toBeTruthy()
    expect(screen.queryByTestId('personal-card')).toBeNull()
    fireEvent.click(q('personal-workbook-back'))
    expect(screen.getByTestId('personal-card')).toBeTruthy()
  })
  it('no workbook, no button', () => {
    scene()
    expect(screen.queryByTestId('personal-card-workbook')).toBeNull()
  })
})

describe('the workbook button on a journal.v1 host (R14)', () => {
  it('opens and closes the session\'s journal card — the same switch as the title bar\'s 工作簿', () => {
    setJournalHost(HOST, true)
    const key = seedUnit(HOST, 'S1', { kind: 'session', id: 'S1', label: 'cc1' } as never)
    scene()
    const memo = () => readViewMemo(HOST, key).journalOpen
    expect(memo()).toBe(false)
    fireEvent.click(q('personal-card-workbook'))
    expect(memo()).toBe(true)
    expect(q('personal-card-workbook').getAttribute('aria-pressed')).toBe('true')
    expect(screen.queryByTestId('personal-workbook')).toBeNull() // the journal card, not the workbook view
    fireEvent.click(q('personal-card-workbook'))
    expect(memo()).toBe(false)
    setJournalHost(HOST, false)
  })
  it('no button when the journal cards would be another session\'s (a split tab whose first agent pane is codex)', () => {
    setJournalHost(HOST, true)
    seedUnit(HOST, 'S1', { kind: 'session', id: 'S1', label: 'cc1' } as never)
    scene()
    const cx = ccTab('sp', 'S0'); const cc = ccTab('sp2', 'S1')
    const codex = { ...cx, layout: { type: 'leaf', pane: { ...(cx.layout as { pane: { id: string; content: Record<string, unknown> } }).pane, content: { ...(cx.layout as { pane: { content: Record<string, unknown> } }).pane.content, rebuild: { sessionName: 'sp', tmuxInstance: 'i', agent: { type: 'codex', sessionId: 'S0', updatedAt: 1 }, capturedAt: 1 } } } } }
    addTab({ ...codex, layout: { type: 'split', direction: 'h', sizes: [50, 50], children: [codex.layout, cc.layout] } } as unknown as Tab)
    setActive('sp')
    expect(screen.queryByTestId('personal-card-workbook')).toBeNull()
    setJournalHost(HOST, false)
  })
})

describe('a session that already has a group starts a new one (codex R1)', () => {
  it('the name field opens on the NEW group, with its name — not on the old group', async () => {
    readRoot('S1', 'R1')
    act(() => { useManualGroupsStore.setState({ groups: { ...groups(), [A]: { ...groups()[A], members: [{ host: HOST, root: 'R1' }] } } }) })
    scene()
    fireEvent.click(q('personal-group-button'))
    fireEvent.click(q('personal-group-new'))
    const input = (await screen.findByTestId('personal-name-input')) as HTMLInputElement
    expect(input.value).toBe('任務編組 1')
    fireEvent.change(input, { target: { value: 'Fresh' } })
    fireEvent.keyDown(input, { key: 'Enter' })
    expect(groups()[A].name).toBe('iOS 發版')
    expect(Object.values(groups()).map((g) => g.name)).toContain('Fresh')
  })
})
