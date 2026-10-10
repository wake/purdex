// spa/src/components/team/TeamSeatWorkbookView.log.test.tsx — the workbook view's status and 紀錄 (WA-2b-2, parts 1–2): grouping by
// thing, finished groups collapsed, the todo change lines, a refresh entry's text, the entry states, 「更多」.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import { TeamSeatWorkbookView } from './TeamSeatWorkbookView'
import { entry, seedWorkbook } from '../../lib/team/__tests__/workbook-fixture'
import { useWorkbookStore } from '../../stores/useWorkbookStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { clearViewMemos } from '../../lib/workbook/view-memory'

const fetchConversation = vi.hoisted(() => vi.fn())
vi.mock('../../lib/workbook/api', async (orig) => ({ ...(await orig<typeof import('../../lib/workbook/api')>()), fetchConversation }))

const mount = () => render(<TeamSeatWorkbookView hostId="h1" sessionId="S" title="T" />)
const ref = (id: number, title: string) => ({ id, title })

beforeEach(() => {
  cleanup()
  useWorkbookStore.getState().reset()
  clearViewMemos()
  fetchConversation.mockReset()
  fetchConversation.mockReturnValue(new Promise(() => {}))
  useI18nStore.getState().setLocale('zh-TW')
})
afterEach(() => cleanup())

describe('目前狀況', () => {
  it('shows the status and when it was written', () => {
    seedWorkbook('h1', 'S', { status: '修登入頁', statusAt: 1_760_000_000_000 })
    mount()
    expect(screen.getByTestId('team-seat-workbook-status').textContent).toBe('修登入頁')
    expect(screen.getByTestId('team-seat-workbook-status-time').textContent).not.toBe('')
  })
})

describe('紀錄 groups', () => {
  const seed = () => seedWorkbook('h1', 'S', {
    status: 's',
    entries: [
      entry(5, { thing: 'B', entry: 'b2' }), entry(4, { thing: 'A', entry: 'a2', thingDone: true }),
      entry(3, { thing: 'B', entry: 'b1' }), entry(2, { thing: 'A', entry: 'a1' }),
    ],
    oldestId: 2, exhausted: true,
  })
  const groups = () => screen.getAllByTestId('workbook-group')

  it('groups by thing, the most recent thing first, 進行中 / 完成', () => {
    seed(); mount()
    expect(groups().map((g) => g.getAttribute('data-thing'))).toEqual(['B', 'A'])
    expect(within(groups()[0]).getByText('進行中')).toBeTruthy()
    expect(within(groups()[1]).getByText('完成')).toBeTruthy()
  })
  it('a finished group is collapsed until clicked; an open one shows its entries newest first', () => {
    seed(); mount()
    expect(within(groups()[0]).getAllByTestId('team-seat-workbook-entry').map((e) => e.textContent)).toEqual([expect.stringContaining('b2'), expect.stringContaining('b1')])
    expect(within(groups()[1]).queryAllByTestId('team-seat-workbook-entry')).toHaveLength(0)
    fireEvent.click(within(groups()[1]).getByRole('button'))
    expect(within(groups()[1]).getAllByTestId('team-seat-workbook-entry')).toHaveLength(2)
    fireEvent.click(within(groups()[1]).getByRole('button'))
    expect(within(groups()[1]).queryAllByTestId('team-seat-workbook-entry')).toHaveLength(0)
  })
  it('says so when there are no entries', async () => {
    fetchConversation.mockRejectedValue(new Error('offline'))
    seedWorkbook('h1', 'S', { status: 's' }); mount()
    await act(async () => { await Promise.resolve() })
    expect(screen.getByText('尚無紀錄')).toBeTruthy()
  })
})

describe('entry rows', () => {
  it('lists the todo changes under the entry: ＋ added, ✓ done, － dropped', () => {
    seedWorkbook('h1', 'S', { entries: [entry(2, { entry: 'did it', todoChanges: { added: [ref(1, '寫測試')], done: [ref(2, '修 bug')], dropped: [ref(3, '舊的')] } })] }, { v2: true })
    mount()
    const e = screen.getByTestId('team-seat-workbook-entry')
    expect(within(e).getByText('＋ 寫測試')).toBeTruthy()
    expect(within(e).getByText('✓ 修 bug')).toBeTruthy()
    expect(within(e).getByText('－ 舊的')).toBeTruthy()
  })
  it('a refresh entry reads 「重整：完成 a、移除 b、新增 c」 with its lines', () => {
    seedWorkbook('h1', 'S', { entries: [entry(2, { kind: 'refresh', entry: 'ignored', todoChanges: { added: [ref(1, 'n1'), ref(2, 'n2'), ref(3, 'n3')], done: [ref(4, 'd1')], dropped: [ref(5, 'x1'), ref(6, 'x2')] } })] }, { v2: true })
    mount()
    const e = screen.getByTestId('team-seat-workbook-entry')
    expect(e.textContent).toContain('重整：完成 1、移除 2、新增 3')
    expect(within(e).getByText('＋ n1')).toBeTruthy()
  })
  it('a refresh that changed nothing says so', () => {
    seedWorkbook('h1', 'S', { entries: [entry(2, { kind: 'refresh' })] }, { v2: true })
    mount()
    expect(screen.getByTestId('team-seat-workbook-entry').textContent).toContain('重整：沒有變更')
  })
  it('pending reads 整理中…, failed 整理失敗 with the reason on hover, skipped is not shown', () => {
    seedWorkbook('h1', 'S', { entries: [
      entry(4, { thing: '', state: 'pending', entry: '' }), entry(3, { thing: '', state: 'failed', reason: 'timeout', entry: '' }),
      entry(2, { thing: '', state: 'skipped' }), entry(1, { thing: 'A', entry: 'ok one' }),
    ] })
    mount()
    expect(screen.getAllByTestId('team-seat-workbook-entry')).toHaveLength(3)
    expect(screen.getByText('整理中…')).toBeTruthy()
    expect(screen.getByText('整理失敗').closest('[title]')?.getAttribute('title')).toBe('timeout')
  })
})

describe('更多', () => {
  it('pages older entries through the store, and is gone once exhausted', async () => {
    fetchConversation.mockRejectedValue(new Error('offline'))
    seedWorkbook('h1', 'S', { entries: [entry(3)], oldestId: 3, exhausted: false })
    const loadMore = vi.fn().mockResolvedValue(undefined)
    useWorkbookStore.setState({ loadMore })
    mount()
    await act(async () => { await Promise.resolve() })
    fireEvent.click(screen.getByRole('button', { name: '更多' }))
    expect(loadMore).toHaveBeenCalledWith('h1', 'c-S')
    act(() => useWorkbookStore.setState((s) => ({ byHost: { h1: { byConv: { 'c-S': { ...s.byHost.h1.byConv['c-S'], exhausted: true } } } } })))
    expect(screen.queryByRole('button', { name: '更多' })).toBeNull()
  })
})
