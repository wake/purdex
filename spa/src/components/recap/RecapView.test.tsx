// spa/src/components/recap/RecapView.test.tsx — the recap history (plan T14b; docs/pages/recap-view-mock.html, decisions 1 and 2 = B): newest first with the newest open,
// the 完成／目前／已定／等你／下一步 fields, the turn summaries of each span, the turns after the last recap, and the regenerate button in each of its states.
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

import { RecapView } from './RecapView'
import { useDialogueStore } from '../../stores/useDialogueStore'
import { useI18nStore } from '../../stores/useI18nStore'

const at = (h: number, m: number) => new Date(2026, 10, 1, h, m).getTime()
const recap = (id: number, over: Partial<Recap> = {}): Recap => ({
  id, at: at(15, id), from_no: (id - 1) * 30 + 1, to_no: id * 30, trigger: 'auto', headline: `headline ${id}`,
  done: [`done ${id}a`, `done ${id}b`], now: `now ${id}`, decided: [`decided ${id}`], waiting: [], next: `next ${id}`, ...over,
})
const resp = (recaps: Recap[], over: Partial<RecapsResponse> = {}): RecapsResponse =>
  ({ recaps, can_generate: true, newest_turn_no: recaps[0]?.to_no ?? 0, last_recap_to_no: recaps[0]?.to_no ?? 0, ...over })
const turn = (n: number): TurnSummary => ({ turn_no: n, at: at(16, n), title: `title ${n}`, summary: `summary ${n}`, push: '' })
const settle = async () => { await act(async () => { await new Promise((r) => setTimeout(r, 0)) }) }
const onBack = vi.fn()
const mount = () => render(<RecapView hostId="h1" sessionId="S1" label="Freya" backLabel="返回團隊" onBack={onBack} />)
const regen = () => screen.getByTestId('recap-regen') as HTMLButtonElement

beforeEach(() => {
  cleanup()
  fetchRecaps.mockReset()
  fetchTurnSummaries.mockReset()
  requestRecap.mockReset()
  onBack.mockReset()
  fetchTurnSummaries.mockResolvedValue([])
  requestRecap.mockResolvedValue('up_to_date')
  useDialogueStore.getState().reset()
  useDialogueStore.getState().setSupport('h1', true)
  useI18nStore.getState().setLocale('zh-TW')
})
afterEach(() => { cleanup() })

describe('RecapView: recaps', () => {
  it('lists newest first, the newest open with its fields in Chinese and 等你 marked, older ones collapsed until clicked', async () => {
    fetchRecaps.mockResolvedValue(resp([recap(2, { waiting: ['看原型'] }), recap(1)]))
    mount()
    await settle()
    expect(fetchRecaps).toHaveBeenCalledWith('h1', 'S1', undefined)
    expect(screen.getByTestId('recap-title')).toHaveTextContent('Freya 的 recap')
    const items = screen.getAllByTestId('recap-item')
    expect(items).toHaveLength(2)
    expect(items[0]).toHaveTextContent('headline 2')
    expect(items[0]).toHaveTextContent('15:02')
    expect(items[0]).toHaveTextContent('回合 31–60')
    expect(items[0]).toHaveTextContent('自動')
    expect(within(items[0]).getByTestId('recap-sec-done')).toHaveTextContent('完成')
    expect(within(items[0]).getByTestId('recap-sec-done')).toHaveTextContent('done 2b')
    expect(within(items[0]).getByTestId('recap-sec-now')).toHaveTextContent('目前')
    expect(within(items[0]).getByTestId('recap-sec-decided')).toHaveTextContent('已定')
    expect(within(items[0]).getByTestId('recap-sec-waiting')).toHaveTextContent('等你')
    expect(within(items[0]).getByTestId('recap-sec-waiting').className).toMatch(/status-warning/)
    expect(within(items[0]).getByTestId('recap-sec-next')).toHaveTextContent('下一步')
    expect(within(items[1]).queryByTestId('recap-sec-done')).toBeNull() // collapsed
    fireEvent.click(within(items[1]).getByTestId('recap-headline'))
    expect(within(items[1]).getByTestId('recap-sec-done')).toHaveTextContent('done 1a')
  })

  it('names every trigger the daemon sends: every30 as 自動, compact as 壓縮時, anything else as is', async () => {
    fetchRecaps.mockResolvedValue(resp([
      recap(3, { trigger: 'weird' }), recap(2, { trigger: 'compact' }), recap(1, { trigger: 'every30' }),
    ]))
    mount()
    await settle()
    const items = screen.getAllByTestId('recap-item')
    expect(items[0]).toHaveTextContent('· weird')
    expect(items[1]).toHaveTextContent('· 壓縮時')
    expect(items[2]).toHaveTextContent('· 自動')
    expect(items[2]).not.toHaveTextContent('every30')
  })

  it('leaves out an empty field (no 等你 box when nothing waits)', async () => {
    fetchRecaps.mockResolvedValue(resp([recap(1)]))
    mount()
    await settle()
    expect(screen.queryByTestId('recap-sec-waiting')).toBeNull()
  })

  it('a span\'s turn summaries open under it: number, time, title and summary, newest first', async () => {
    fetchRecaps.mockResolvedValue(resp([recap(1, { from_no: 1, to_no: 3 })], { newest_turn_no: 3, last_recap_to_no: 3 }))
    fetchTurnSummaries.mockResolvedValue([turn(1), turn(2), turn(3)])
    mount()
    await settle()
    expect(fetchTurnSummaries).toHaveBeenCalledWith('h1', 'S1', { from: 1, to: 3 })
    const toggle = screen.getByTestId('recap-turns-toggle')
    expect(toggle).toHaveTextContent('這段的回合摘要 · 3 則')
    expect(screen.queryByTestId('recap-turn-row')).toBeNull()
    fireEvent.click(toggle)
    const rows = screen.getAllByTestId('recap-turn-row')
    expect(rows.map((r) => within(r).getByTestId('recap-turn-no').textContent)).toEqual(['3', '2', '1'])
    expect(rows[0]).toHaveTextContent('16:03')
    expect(rows[0]).toHaveTextContent('title 3')
    expect(rows[0]).toHaveTextContent('summary 3')
  })

  it('the back button calls onBack', async () => {
    fetchRecaps.mockResolvedValue(resp([recap(1)]))
    mount()
    await settle()
    fireEvent.click(screen.getByTestId('recap-back'))
    expect(onBack).toHaveBeenCalledTimes(1)
    expect(screen.getByTestId('recap-back')).toHaveAttribute('aria-label', '返回團隊')
  })
})

describe('RecapView: the status line and the regenerate button', () => {
  it('up to date: nothing is asked of the daemon, the button is off and the status says so', async () => {
    fetchRecaps.mockResolvedValue(resp([recap(1)]))
    mount()
    await settle()
    expect(requestRecap).not.toHaveBeenCalled()
    expect(screen.getByTestId('recap-status')).toHaveTextContent('已是最新，最後一則涵蓋到回合 30')
    expect(regen().disabled).toBe(true)
    expect(regen()).toHaveAttribute('title', '已是最新：沒有新的回合摘要')
  })

  it('opening with newer turn summaries asks for a recap once ("open"), and the session shows as generating', async () => {
    fetchRecaps.mockResolvedValue(resp([recap(1)], { newest_turn_no: 33 }))
    fetchTurnSummaries.mockResolvedValue([turn(31), turn(32), turn(33)])
    requestRecap.mockResolvedValue('queued')
    mount()
    await settle()
    expect(requestRecap).toHaveBeenCalledTimes(1)
    expect(requestRecap).toHaveBeenCalledWith('h1', 'S1', 'open')
    expect(screen.getByTestId('recap-status')).toHaveTextContent('正在產生新的 recap')
    expect(regen().disabled).toBe(true)
    expect(regen()).toHaveTextContent('產生中…')
    expect(screen.getByTestId('recap-generating')).toBeTruthy()
    expect(screen.getByTestId('recap-after')).toHaveTextContent('這 3 則正在寫進新的 recap')
  })

  it('newer turn summaries and no job queued: the button is on and a press asks for a "manual" recap', async () => {
    fetchRecaps.mockResolvedValue(resp([recap(1)], { newest_turn_no: 33 }))
    fetchTurnSummaries.mockResolvedValue([turn(31), turn(32), turn(33)])
    requestRecap.mockResolvedValueOnce('up_to_date') // the open-time ask: the daemon holds off
    mount()
    await settle()
    expect(screen.getByTestId('recap-status')).toHaveTextContent('有 3 則新的回合摘要，按「重新產生」更新')
    expect(regen().disabled).toBe(false)
    requestRecap.mockResolvedValueOnce('queued')
    fireEvent.click(regen())
    await settle()
    expect(requestRecap).toHaveBeenLastCalledWith('h1', 'S1', 'manual')
    expect(screen.getByTestId('recap-status')).toHaveTextContent('正在產生新的 recap')
    expect(regen().disabled).toBe(true)
  })

  it('a failed request says so and leaves the button on', async () => {
    fetchRecaps.mockResolvedValue(resp([recap(1)], { newest_turn_no: 33 }))
    requestRecap.mockResolvedValueOnce('up_to_date')
    mount()
    await settle()
    requestRecap.mockRejectedValueOnce(new Error('boom'))
    fireEvent.click(regen())
    await settle()
    expect(screen.getByTestId('recap-notice')).toHaveTextContent('重新產生沒有成功')
    expect(regen().disabled).toBe(false)
  })

  it('a dialogue.recap event ends generating: the new recap tops the list, open, marked 新', async () => {
    fetchRecaps.mockResolvedValueOnce(resp([recap(1)], { newest_turn_no: 33 }))
    requestRecap.mockResolvedValue('queued')
    mount()
    await settle()
    expect(screen.getByTestId('recap-generating')).toBeTruthy()
    fetchRecaps.mockResolvedValueOnce(resp([recap(2, { from_no: 4, to_no: 33, headline: 'headline new' }), recap(1)], { newest_turn_no: 33 }))
    await act(async () => { useDialogueStore.getState().applyRecapEvent('h1', JSON.stringify({ session_id: 'S1', recap_id: 2 })) })
    await settle()
    expect(screen.queryByTestId('recap-generating')).toBeNull()
    const items = screen.getAllByTestId('recap-item')
    expect(items[0]).toHaveTextContent('headline new')
    expect(within(items[0]).getByTestId('recap-sec-done')).toBeTruthy()
    expect(within(items[0]).getByTestId('recap-new')).toHaveTextContent('新')
    expect(within(items[1]).queryByTestId('recap-new')).toBeNull()
    expect(screen.getByTestId('recap-status')).toHaveTextContent('已是最新，最後一則涵蓋到回合 33')
  })

  it('no turn summaries at all: the button is off with the empty text, and nothing is asked', async () => {
    fetchRecaps.mockResolvedValue(resp([], { newest_turn_no: 0, last_recap_to_no: 0 }))
    mount()
    await settle()
    expect(requestRecap).not.toHaveBeenCalled()
    expect(screen.getByTestId('recap-status')).toHaveTextContent('還沒有回合摘要')
    expect(regen().disabled).toBe(true)
    expect(screen.getByTestId('recap-empty')).toHaveTextContent('還沒有 recap')
  })

  it('an ended session (can_generate false): the button is off and says why, nothing is asked', async () => {
    fetchRecaps.mockResolvedValue(resp([recap(1)], { can_generate: false, newest_turn_no: 34 }))
    fetchTurnSummaries.mockResolvedValue([turn(31), turn(32), turn(33), turn(34)])
    mount()
    await settle()
    expect(requestRecap).not.toHaveBeenCalled()
    expect(screen.getByTestId('recap-status')).toHaveTextContent('session 已結束，無法產生新的 recap')
    expect(regen().disabled).toBe(true)
    expect(regen()).toHaveAttribute('title', 'session 已結束，無法產生新的 recap')
    expect(screen.getByTestId('recap-after')).toHaveTextContent('結束前最後 4 回合沒有寫進 recap')
  })

  it('an ended session with no recap at all says it left none', async () => {
    fetchRecaps.mockResolvedValue(resp([], { can_generate: false, newest_turn_no: 0 }))
    mount()
    await settle()
    expect(screen.getByTestId('recap-empty')).toHaveTextContent('這個 session 沒有留下 recap。')
  })

  it('a recaps read that fails says so', async () => {
    fetchRecaps.mockRejectedValue(new Error('down'))
    mount()
    await settle()
    expect(screen.getByTestId('recap-status')).toHaveTextContent('讀不到 recap')
    expect(regen().disabled).toBe(true)
  })
})

describe('RecapView: a failed re-read after a dialogue.recap event', () => {
  it('shows the read-failed line once the retries give up, with the old recaps still listed', async () => {
    vi.useFakeTimers()
    try {
      fetchRecaps.mockResolvedValueOnce(resp([recap(1)]))
      mount()
      await act(async () => { await vi.advanceTimersByTimeAsync(0) })
      fetchRecaps.mockRejectedValue(new Error('down'))
      act(() => { useDialogueStore.getState().applyRecapEvent('h1', JSON.stringify({ session_id: 'S1', recap_id: 2 })) })
      await act(async () => { await vi.advanceTimersByTimeAsync(7000) })
      expect(screen.getByTestId('recap-status')).toHaveTextContent('讀不到 recap')
      expect(screen.getAllByTestId('recap-item')).toHaveLength(1)
    } finally { vi.useRealTimers() }
  })
})

describe('RecapView: the turns after the newest recap (decision 2 = B)', () => {
  it('lists them above the recaps, open at first, newest first, and folds on a click', async () => {
    fetchRecaps.mockResolvedValue(resp([recap(1)], { newest_turn_no: 33 }))
    fetchTurnSummaries.mockImplementation(async (_h, _s, r) => (r?.from === 31 ? [turn(31), turn(32), turn(33)] : []))
    requestRecap.mockResolvedValueOnce('up_to_date')
    mount()
    await settle()
    expect(fetchTurnSummaries).toHaveBeenCalledWith('h1', 'S1', { from: 31 })
    const after = screen.getByTestId('recap-after')
    expect(after).toHaveTextContent('有 3 則新的回合摘要，還沒寫進 recap')
    expect(within(after).getAllByTestId('recap-turn-row').map((r) => within(r).getByTestId('recap-turn-no').textContent)).toEqual(['33', '32', '31'])
    // it sits above the first recap
    expect(after.compareDocumentPosition(screen.getAllByTestId('recap-item')[0]) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    fireEvent.click(within(after).getByTestId('recap-after-toggle'))
    expect(within(after).queryByTestId('recap-turn-row')).toBeNull()
  })

  it('counts the turns by number, not by how many summaries the daemon listed (it lists at most the newest 100)', async () => {
    fetchRecaps.mockResolvedValue(resp([recap(1, { to_no: 30 })], { newest_turn_no: 180, last_recap_to_no: 30 }))
    fetchTurnSummaries.mockImplementation(async (_h, _s, r) => (r?.from === 31 ? Array.from({ length: 100 }, (_, i) => turn(81 + i)) : []))
    requestRecap.mockResolvedValue('up_to_date')
    mount()
    await settle()
    expect(screen.getByTestId('recap-status')).toHaveTextContent('有 150 則新的回合摘要')
    expect(screen.getByTestId('recap-after')).toHaveTextContent('有 150 則新的回合摘要，還沒寫進 recap')
    expect(screen.getByTestId('recap-after-capped')).toHaveTextContent('只列出最新 100 則')
  })

  it('an ended session counts its last turns by number too', async () => {
    fetchRecaps.mockResolvedValue(resp([recap(1, { to_no: 30 })], { newest_turn_no: 180, last_recap_to_no: 30, can_generate: false }))
    fetchTurnSummaries.mockImplementation(async (_h, _s, r) => (r?.from === 31 ? Array.from({ length: 100 }, (_, i) => turn(81 + i)) : []))
    mount()
    await settle()
    expect(screen.getByTestId('recap-after')).toHaveTextContent('結束前最後 150 回合沒有寫進 recap')
    cleanup()
    useDialogueStore.getState().reset()
    useDialogueStore.getState().setSupport('h1', true)
    fetchRecaps.mockResolvedValue(resp([recap(1, { to_no: 30 })], { newest_turn_no: 180, last_recap_to_no: 30 }))
    requestRecap.mockResolvedValue('queued')
    mount()
    await settle()
    expect(screen.getByTestId('recap-after')).toHaveTextContent('這 150 則正在寫進新的 recap')
  })

  it('no capped note when exactly the limit of turns is waiting and all of them are listed', async () => {
    fetchRecaps.mockResolvedValue(resp([recap(1, { to_no: 30 })], { newest_turn_no: 130, last_recap_to_no: 30 }))
    fetchTurnSummaries.mockImplementation(async (_h, _s, r) => (r?.from === 31 ? Array.from({ length: 100 }, (_, i) => turn(31 + i)) : []))
    requestRecap.mockResolvedValue('up_to_date')
    mount()
    await settle()
    expect(screen.queryByTestId('recap-after-capped')).toBeNull()
  })

  it('no capped note when a turn has no summary (fewer listed than turns, under the limit)', async () => {
    fetchRecaps.mockResolvedValue(resp([recap(1)], { newest_turn_no: 33 }))
    fetchTurnSummaries.mockImplementation(async (_h, _s, r) => (r?.from === 31 ? [turn(31), turn(33)] : []))
    requestRecap.mockResolvedValue('up_to_date')
    mount()
    await settle()
    expect(screen.getByTestId('recap-after')).toHaveTextContent('有 3 則新的回合摘要')
    expect(screen.queryByTestId('recap-after-capped')).toBeNull()
  })

  it('no capped note when the listed summaries are all of them', async () => {
    fetchRecaps.mockResolvedValue(resp([recap(1)], { newest_turn_no: 33 }))
    fetchTurnSummaries.mockImplementation(async (_h, _s, r) => (r?.from === 31 ? [turn(31), turn(32), turn(33)] : []))
    mount()
    await settle()
    expect(screen.queryByTestId('recap-after-capped')).toBeNull()
  })

  it('labels: ended says the last N turns before it ended, generating says these N are being written', async () => {
    fetchRecaps.mockResolvedValue(resp([recap(1)], { newest_turn_no: 33, can_generate: false }))
    fetchTurnSummaries.mockImplementation(async (_h, _s, r) => (r?.from === 31 ? [turn(31), turn(32), turn(33)] : []))
    mount()
    await settle()
    expect(screen.getByTestId('recap-after')).toHaveTextContent('結束前最後 3 回合沒有寫進 recap')
    cleanup()
    useDialogueStore.getState().reset()
    useDialogueStore.getState().setSupport('h1', true)
    fetchRecaps.mockResolvedValue(resp([recap(1)], { newest_turn_no: 33 }))
    requestRecap.mockResolvedValue('queued')
    mount()
    await settle()
    expect(screen.getByTestId('recap-after')).toHaveTextContent('這 3 則正在寫進新的 recap')
  })

  it('is absent when every turn is already in a recap', async () => {
    fetchRecaps.mockResolvedValue(resp([recap(1)]))
    mount()
    await settle()
    expect(screen.queryByTestId('recap-after')).toBeNull()
  })
})
