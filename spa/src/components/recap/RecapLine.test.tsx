// spa/src/components/recap/RecapLine.test.tsx — the second line of a team row / the personal card (plan T14b, prototype decision 1 = B): the newest
// recap's headline with 「截至 HH:MM」; with no recap yet 「回合 N」 and the latest turn summary's title; nothing without the capability or any data.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import type { Recap, RecapsResponse, TurnSummary } from '../../lib/dialogue/types'

const fetchRecaps = vi.fn<(h: string, s: string, limit?: number) => Promise<RecapsResponse>>()
const fetchTurnSummaries = vi.fn<(h: string, s: string, r?: { from?: number; to?: number; limit?: number }) => Promise<TurnSummary[]>>()
const requestRecap = vi.fn()
vi.mock('../../lib/dialogue/api', () => ({
  fetchRecaps: (...a: [string, string, number?]) => fetchRecaps(...a),
  fetchTurnSummaries: (...a: [string, string, { from?: number; to?: number; limit?: number }?]) => fetchTurnSummaries(...a),
  requestRecap: (...a: unknown[]) => requestRecap(...a),
}))

import { RecapLine } from './RecapLine'
import { useDialogueStore } from '../../stores/useDialogueStore'
import { useI18nStore } from '../../stores/useI18nStore'

const at = (h: number, m: number) => new Date(2026, 10, 1, h, m).getTime()
const recap = (id: number, over: Partial<Recap> = {}): Recap =>
  ({ id, at: at(16, 5), from_no: 1, to_no: 30, trigger: 'auto', headline: `headline ${id}`, done: [], now: '', decided: [], waiting: [], next: '', ...over })
const resp = (recaps: Recap[], over: Partial<RecapsResponse> = {}): RecapsResponse =>
  ({ recaps, can_generate: true, newest_turn_no: 30, last_recap_to_no: recaps[0]?.to_no ?? 0, ...over })
const turn = (n: number): TurnSummary => ({ turn_no: n, at: at(15, n), title: `title ${n}`, summary: `summary ${n}`, push: '' })
const settle = async () => { await act(async () => { await new Promise((r) => setTimeout(r, 0)) }) }
const mount = (props: Partial<React.ComponentProps<typeof RecapLine>> = {}) => render(<RecapLine hostId="h1" sessionId="S1" onOpen={() => {}} {...props} />)

beforeEach(() => {
  cleanup()
  fetchRecaps.mockReset()
  fetchTurnSummaries.mockReset()
  requestRecap.mockReset()
  fetchTurnSummaries.mockResolvedValue([])
  useDialogueStore.getState().reset()
  useDialogueStore.getState().setSupport('h1', true)
  useI18nStore.getState().setLocale('zh-TW')
})
afterEach(() => { cleanup() })

describe('RecapLine', () => {
  it('shows the newest recap\'s headline and 截至 HH:MM in 24-hour local time', async () => {
    fetchRecaps.mockResolvedValue(resp([recap(2, { headline: '對話檔寫入器做完' })]))
    mount()
    await settle()
    expect(screen.getByTestId('recap-line')).toHaveTextContent('對話檔寫入器做完')
    expect(screen.getByTestId('recap-line-asof')).toHaveTextContent('截至 16:05')
    expect(fetchRecaps).toHaveBeenCalledWith('h1', 'S1', 1)
    expect(fetchTurnSummaries).not.toHaveBeenCalled() // a recap exists: no turn summary needed
  })

  it('with no recap shows 「回合 N」 and the latest turn summary\'s title (decision 1 = B)', async () => {
    fetchRecaps.mockResolvedValue(resp([], { newest_turn_no: 9 }))
    fetchTurnSummaries.mockResolvedValue([turn(9)])
    mount()
    await settle()
    expect(fetchTurnSummaries).toHaveBeenCalledWith('h1', 'S1', { limit: 1 })
    expect(screen.getByTestId('recap-line-turn')).toHaveTextContent('回合 9')
    expect(screen.getByTestId('recap-line')).toHaveTextContent('title 9')
    expect(screen.queryByTestId('recap-line-asof')).toBeNull()
  })

  it('the first recap replaces the turn line', async () => {
    fetchRecaps.mockResolvedValueOnce(resp([], { newest_turn_no: 9 }))
    fetchTurnSummaries.mockResolvedValue([turn(9)])
    mount()
    await settle()
    expect(screen.getByTestId('recap-line-turn')).toBeTruthy()
    fetchRecaps.mockResolvedValueOnce(resp([recap(1, { headline: '第一則' })]))
    await act(async () => { useDialogueStore.getState().applyRecapEvent('h1', JSON.stringify({ session_id: 'S1', recap_id: 1 })) })
    await settle()
    expect(screen.queryByTestId('recap-line-turn')).toBeNull()
    expect(screen.getByTestId('recap-line')).toHaveTextContent('第一則')
    expect(screen.getByTestId('recap-line-asof')).toHaveTextContent('截至 16:05')
  })

  it('renders nothing when there is neither a recap nor a turn summary', async () => {
    fetchRecaps.mockResolvedValue(resp([], { newest_turn_no: 0 }))
    mount()
    await settle()
    expect(screen.queryByTestId('recap-line')).toBeNull()
  })

  it('renders nothing and sends no request on a host without dialogue.v1', async () => {
    useDialogueStore.getState().setSupport('h1', false)
    mount()
    await settle()
    expect(screen.queryByTestId('recap-line')).toBeNull()
    expect(fetchRecaps).not.toHaveBeenCalled()
    expect(fetchTurnSummaries).not.toHaveBeenCalled()
  })

  it('while a recap is being written it says 產生中 beside the headline, or 正在產生第一則 recap with none yet', async () => {
    fetchRecaps.mockResolvedValue(resp([recap(2)]))
    mount()
    await settle()
    act(() => { useDialogueStore.setState((s) => ({ byHost: { h1: { S1: { ...s.byHost.h1.S1, generating: true } } } })) })
    expect(screen.getByTestId('recap-line-asof')).toHaveTextContent('產生中')
    cleanup()
    useDialogueStore.getState().reset()
    useDialogueStore.getState().setSupport('h1', true)
    fetchRecaps.mockResolvedValue(resp([], { newest_turn_no: 0 }))
    mount({ sessionId: 'S2' })
    await settle()
    act(() => { useDialogueStore.setState((s) => ({ byHost: { h1: { S2: { ...s.byHost.h1.S2, generating: true } } } })) })
    expect(screen.getByTestId('recap-line')).toHaveTextContent('正在產生第一則 recap')
  })

  it('an ended session\'s line is prefixed 最後：', async () => {
    fetchRecaps.mockResolvedValue(resp([recap(2, { headline: '推播改接回合摘要' })], { can_generate: false }))
    mount({ ended: true })
    await settle()
    expect(screen.getByTestId('recap-line')).toHaveTextContent('最後：推播改接回合摘要')
  })

  it('a click opens the recap history and does not reach the row behind it', async () => {
    fetchRecaps.mockResolvedValue(resp([recap(2)]))
    const onOpen = vi.fn()
    const onRow = vi.fn()
    render(<div role="button" tabIndex={0} onClick={onRow}><RecapLine hostId="h1" sessionId="S1" onOpen={onOpen} /></div>)
    await settle()
    fireEvent.click(screen.getByTestId('recap-line'))
    expect(onOpen).toHaveBeenCalledTimes(1)
    expect(onRow).not.toHaveBeenCalled()
  })
})
