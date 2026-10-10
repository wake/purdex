// spa/src/components/team/TeamPanel.workbook.test.tsx — the team panel's workbook parts (WA-2b-1): the task line, the
// workbook button, the drilled-in view, the ended list. Real stores and provider; the light and host badge are stand-ins.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import { TeamDisplayProvider } from './TeamDisplayProvider'
import { TeamPanelArea } from './TeamPanelArea'
import { HOST, KEY, resetTeamStores, seedScene } from '../../lib/team/__tests__/team-fixture'
import { entry, seedWorkbook } from '../../lib/team/__tests__/workbook-fixture'
import { useTeamUiStore } from '../../stores/useTeamUiStore'
import { useWorkbookStore } from '../../stores/useWorkbookStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { useTabStore } from '../../stores/useTabStore'
import { useShownHostsStore } from '../../stores/useShownHostsStore'
import { clearModuleRegistry } from '../../lib/module-registry'
import type { EntryEvent } from '../../lib/workbook/types'

const fetchConversation = vi.hoisted(() => vi.fn())
vi.mock('../../lib/workbook/api', async (orig) => ({ ...(await orig<typeof import('../../lib/workbook/api')>()), fetchConversation }))
vi.mock('./TeamSeatIcon', () => ({
  TeamSeatIcon: () => <span data-testid="seat-icon" />,
  TeamSeatHostBadge: () => <span data-testid="seat-host" />,
}))

function scene() {
  seedScene({
    members: [['A', 'a-tm'], ['B', 'b-tm']],
    tabs: [['lead', 'lead-tm'], ['ma', 'a-tm']],
    workspaces: [{ id: 'w1', tabs: ['lead', 'ma'] }],
    activeTabId: 'lead',
  })
}
const mount = () => render(<TeamDisplayProvider><TeamPanelArea /></TeamDisplayProvider>)
const row = (id: string) => screen.getAllByTestId('team-panel-row').find((r) => r.getAttribute('data-session-id') === id)!

beforeEach(() => {
  cleanup()
  localStorage.clear()
  resetTeamStores()
  useWorkbookStore.getState().reset()
  useTeamUiStore.setState({ panel: { width: 412 }, teamDrill: {}, endedSeats: {}, workbookTabs: {} })
  clearModuleRegistry()
  fetchConversation.mockReset()
  fetchConversation.mockReturnValue(new Promise(() => {})) // an opened view asks once; the answer never lands
  useShownHostsStore.setState({ ids: [HOST] })
  useI18nStore.getState().setLocale('zh-TW')
})
afterEach(() => cleanup())

describe('the task line', () => {
  it('shows the first sentence of the status as line 2, the whole status in the tooltip', () => {
    scene()
    seedWorkbook(HOST, 'A', { status: '正在修登入頁。接著補測試。' })
    mount()
    const task = within(row('A')).getByTestId('team-panel-task')
    expect(task).toHaveTextContent('正在修登入頁')
    expect(task).not.toHaveTextContent('接著補測試')
    expect(task.getAttribute('title')).toBe('正在修登入頁。接著補測試。')
  })
  it('a seat without a workbook, or with a blank status, stays one line', () => {
    scene()
    seedWorkbook(HOST, 'B', { status: '   ' })
    mount()
    expect(within(row('A')).queryByTestId('team-panel-task')).toBeNull()
    expect(within(row('B')).queryByTestId('team-panel-task')).toBeNull()
  })
  it('a host without workbook.v1 shows no task', () => {
    scene()
    seedWorkbook(HOST, 'A', { status: 'busy' }, { v1: false })
    mount()
    expect(within(row('A')).queryByTestId('team-panel-task')).toBeNull()
  })
})

const wbButton = (id: string) => within(row(id)).queryByRole('button', { name: '開啟工作簿' })
const drill = () => useTeamUiStore.getState().teamDrill[KEY]

describe('the workbook button', () => {
  it('sits on the lead and member rows that have a workbook, after the context ring, and on no other', () => {
    scene()
    seedWorkbook(HOST, 'L', { status: 'x' })
    seedWorkbook(HOST, 'A', { status: '' })
    mount()
    expect(wbButton('L')).toBeTruthy()
    expect(wbButton('A')).toBeTruthy() // a workbook without a status still has one
    expect(wbButton('B')).toBeNull()
    const ring = within(row('A')).getByTestId('team-panel-ring')
    expect(ring.compareDocumentPosition(wbButton('A')!) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
  })
  it('is absent on a host without workbook.v1', () => {
    scene()
    seedWorkbook(HOST, 'A', { status: 'x' }, { v1: false })
    mount()
    expect(wbButton('A')).toBeNull()
  })
  it('a click drills into that seat and does not open its tab', () => {
    scene()
    seedWorkbook(HOST, 'A', { status: 'x' })
    mount()
    fireEvent.click(wbButton('A')!)
    expect(drill()).toEqual({ hostId: HOST, sessionId: 'A' })
    expect(useTabStore.getState().activeTabId).toBe('lead')
  })
  it('a click on the row itself still opens the seat and does not drill', () => {
    scene()
    seedWorkbook(HOST, 'A', { status: 'x' })
    mount()
    fireEvent.click(row('A'))
    expect(drill()).toBeUndefined()
    expect(useTabStore.getState().activeTabId).toBe('ma')
  })
  it('Enter on the button does not reach the row (which would open the tab)', () => {
    scene()
    seedWorkbook(HOST, 'A', { status: 'x' })
    mount()
    fireEvent.keyDown(wbButton('A')!, { key: 'Enter' })
    expect(useTabStore.getState().activeTabId).toBe('lead')
  })
})

describe('the drilled-in workbook view', () => {
  const view = () => screen.queryByTestId('team-seat-workbook')

  it('replaces the list; shows the title, the status and the latest entries as plain text; back returns', () => {
    scene()
    seedWorkbook(HOST, 'A', { status: '修登入頁', entries: [entry(2, { entry: '補完測試' }), entry(1, { entry: '開始修' })] })
    useTeamUiStore.getState().setTeamDrill(KEY, { hostId: HOST, sessionId: 'A' })
    mount()
    expect(view()).toBeTruthy()
    expect(screen.queryAllByTestId('team-panel-row')).toHaveLength(0)
    expect(view()).toHaveTextContent('title A 的工作簿')
    expect(view()).toHaveTextContent('修登入頁')
    expect(view()).toHaveTextContent('補完測試')
    expect(view()).toHaveTextContent('開始修')
    fireEvent.click(screen.getByRole('button', { name: '返回團隊' }))
    expect(drill()).toBeUndefined()
    expect(view()).toBeNull()
    expect(screen.getAllByTestId('team-panel-row').length).toBeGreaterThan(0)
  })
  it('says so when there is no status and no entries (and 「載入中」 while the page is out)', async () => {
    scene()
    seedWorkbook(HOST, 'A', {})
    useTeamUiStore.getState().setTeamDrill(KEY, { hostId: HOST, sessionId: 'A' })
    fetchConversation.mockRejectedValue(new Error('offline'))
    mount()
    expect(view()).toHaveTextContent('載入中')
    await act(async () => { await Promise.resolve() })
    expect(view()).toHaveTextContent('尚無狀態')
    expect(view()).toHaveTextContent('尚無紀錄')
  })
  it('the drill lives in the store: unmount and mount again shows the same view', () => {
    scene()
    seedWorkbook(HOST, 'A', { status: '修登入頁' })
    useTeamUiStore.getState().setTeamDrill(KEY, { hostId: HOST, sessionId: 'A' })
    const first = mount()
    first.unmount()
    mount()
    expect(view()).toHaveTextContent('修登入頁')
  })
  it('holds the conversation while open (counted) and lets go on unmount', () => {
    scene()
    const convKey = seedWorkbook(HOST, 'A', { status: 'x' })
    useTeamUiStore.getState().setTeamDrill(KEY, { hostId: HOST, sessionId: 'A' })
    const first = mount()
    expect(useWorkbookStore.getState().viewing[HOST]?.[convKey]).toBe(1)
    first.unmount()
    expect(useWorkbookStore.getState().viewing[HOST]?.[convKey]).toBeUndefined()
  })
  it('asks for the workbook once when it opens, and an event never fetches', () => {
    scene()
    const convKey = seedWorkbook(HOST, 'A', { status: 'x' })
    useTeamUiStore.getState().setTeamDrill(KEY, { hostId: HOST, sessionId: 'A' })
    mount()
    expect(fetchConversation).toHaveBeenCalledTimes(1)
    act(() => {
      for (let i = 1; i <= 30; i++) {
        useWorkbookStore.getState().applyEntry(HOST, { convKey, entry: entry(i, { convKey, sessionId: 'A' }) } as unknown as EntryEvent)
      }
    })
    expect(fetchConversation).toHaveBeenCalledTimes(1)
    expect(view()).toHaveTextContent('entry 30')
  })
  it('a drilled seat that is no longer on the roster keeps its view until back (the title comes from the ended list)', () => {
    scene()
    seedWorkbook(HOST, 'Z', { status: '留下的狀態' })
    useTeamUiStore.getState().recordEndedSeats(KEY, [{ hostId: HOST, sessionId: 'Z', title: 'gone Z', endedAt: 1 }])
    useTeamUiStore.getState().setTeamDrill(KEY, { hostId: HOST, sessionId: 'Z' })
    mount()
    expect(view()).toHaveTextContent('gone Z 的工作簿')
    expect(view()).toHaveTextContent('留下的狀態')
  })
})

describe('the ended list', () => {
  const ended = (id: string) => ({ hostId: HOST, sessionId: id, title: `gone ${id}`, endedAt: 1 })

  it('is absent with none; with some it is a collapsed 「已結束 (N)」 group', () => {
    scene()
    mount()
    expect(screen.queryByTestId('team-panel-ended')).toBeNull()
    cleanup()
    useTeamUiStore.setState({ endedSeats: { [KEY]: [] } }) // an empty list is none too
    mount()
    expect(screen.queryByTestId('team-panel-ended')).toBeNull()
    cleanup()
    useTeamUiStore.setState({ endedSeats: {} })
    useTeamUiStore.getState().recordEndedSeats(KEY, [ended('X'), ended('Y')])
    mount()
    expect(screen.getByRole('button', { name: /已結束 \(2\)/ })).toBeTruthy()
    expect(screen.queryAllByTestId('team-panel-ended-row')).toHaveLength(0)
  })
  it('expanding lists each seat (title, hint tooltip); a click drills into its workbook', () => {
    scene()
    useTeamUiStore.getState().recordEndedSeats(KEY, [ended('X'), ended('Y')])
    mount()
    fireEvent.click(screen.getByRole('button', { name: /已結束/ }))
    const rows = screen.getAllByTestId('team-panel-ended-row')
    expect(rows.map((r) => r.textContent)).toEqual(['gone X', 'gone Y'])
    expect(rows[0].getAttribute('title')).toBe('已結束，點擊開啟工作簿')
    fireEvent.click(rows[1])
    expect(drill()).toEqual({ hostId: HOST, sessionId: 'Y' })
    expect(screen.getByTestId('team-seat-workbook')).toHaveTextContent('gone Y 的工作簿')
  })
})
