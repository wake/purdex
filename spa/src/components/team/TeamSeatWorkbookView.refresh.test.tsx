// spa/src/components/team/TeamSeatWorkbookView.refresh.test.tsx — the workbook view's v2 part 4: the 重整 control.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { TeamSeatWorkbookView } from './TeamSeatWorkbookView'
import { entry, seedWorkbook } from '../../lib/team/__tests__/workbook-fixture'
import { useWorkbookStore, type ConvState, type RefreshOutcome } from '../../stores/useWorkbookStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { clearViewMemos } from '../../lib/workbook/view-memory'

const fetchConversation = vi.hoisted(() => vi.fn())
vi.mock('../../lib/workbook/api', async (orig) => ({ ...(await orig<typeof import('../../lib/workbook/api')>()), fetchConversation }))

const seed = (over: Partial<ConvState> = {}, v2 = true) => seedWorkbook('h1', 'S', { status: 's', ...over }, { v2 })
const mount = () => render(<TeamSeatWorkbookView hostId="h1" sessionId="S" title="T" />)
const settle = () => act(async () => { await Promise.resolve() })
const btn = () => screen.getByTestId('workbook-refresh') as HTMLButtonElement
const stubRefresh = (outcome: RefreshOutcome) => {
  const requestRefresh = vi.fn().mockResolvedValue(outcome)
  useWorkbookStore.setState({ requestRefresh })
  return requestRefresh
}
const patchConv = (p: Partial<ConvState>) => act(() => useWorkbookStore.setState((s) => ({ byHost: { h1: { byConv: { 'c-S': { ...s.byHost.h1.byConv['c-S'], ...p } } } } })))

beforeEach(() => {
  cleanup()
  useWorkbookStore.getState().reset()
  clearViewMemos()
  fetchConversation.mockReset()
  fetchConversation.mockRejectedValue(new Error('offline'))
  useI18nStore.getState().setLocale('zh-TW')
})
afterEach(() => cleanup())

describe('重整', () => {
  it('v1 daemon: no control', async () => {
    seed({ refreshAvailable: true }, false); mount(); await settle()
    expect(screen.queryByTestId('workbook-refresh')).toBeNull()
  })
  it('disabled without a refreshable session, with the reason as its tooltip', async () => {
    seed({ refreshAvailable: false }); mount(); await settle()
    expect(btn().disabled).toBe(true)
    expect(btn().title).toBe('這段對話目前沒有可重整的 session')
  })
  it('enabled when refreshAvailable (follows the flag); the tooltip says it uses the main model and costs tokens', async () => {
    seed({ refreshAvailable: false }); mount(); await settle()
    patchConv({ refreshAvailable: true })
    expect(btn().disabled).toBe(false)
    expect(btn().title).toContain('主模型')
    expect(btn().title).toContain('有成本')
    expect(btn().title).toContain('7.5 萬')
    expect(btn().title).toContain('64 萬')
  })
  it('a click asks the store; the 202 entry makes it 重整中…, its ok event ends it', async () => {
    seed({ refreshAvailable: true }); mount(); await settle()
    const req = vi.fn(async (): Promise<RefreshOutcome> => {
      useWorkbookStore.getState().applyEntry('h1', { convKey: 'c-S', sessionId: 'S', entry: entry(7, { kind: 'refresh', state: 'pending', thing: '' }) } as never)
      return { kind: 'accepted', entryId: 7 }
    })
    useWorkbookStore.setState({ requestRefresh: req })
    await act(async () => { fireEvent.click(btn()) })
    expect(req).toHaveBeenCalledWith('h1', 'c-S')
    expect(btn().textContent).toContain('重整中…')
    expect(btn().disabled).toBe(true)
    act(() => useWorkbookStore.getState().applyEntry('h1', { convKey: 'c-S', sessionId: 'S', entry: entry(7, { kind: 'refresh', state: 'ok', updatedAt: 99 }) } as never))
    expect(btn().textContent).toContain('重整')
    expect(btn().textContent).not.toContain('重整中')
    expect(btn().disabled).toBe(false)
  })
  it('a refresh already pending in the conversation shows 重整中… without a click', async () => {
    seed({ refreshAvailable: true, entries: [entry(7, { kind: 'refresh', state: 'pending' })] }); mount(); await settle()
    expect(btn().textContent).toContain('重整中…')
  })
  it('409 not_live: the conversation is asked again (the flag corrects itself)', async () => {
    seed({ refreshAvailable: true }); mount(); await settle()
    const before = fetchConversation.mock.calls.length
    stubRefresh({ kind: 'not_live' })
    await act(async () => { fireEvent.click(btn()) })
    expect(fetchConversation.mock.calls.length).toBe(before + 1)
  })
  it.each(['not_live', 'refresh_pending'] as const)('409 %s while a page load of the conversation is out: still asked again; 重整中… holds until the answer lands', async (kind) => {
    seed({ refreshAvailable: true }); mount(); await settle()
    patchConv({ loading: true }) // a loadMore / loadUntil is in flight
    const before = fetchConversation.mock.calls.length
    let land: (v: unknown) => void = () => {}
    fetchConversation.mockReturnValue(new Promise((res) => { land = res }))
    stubRefresh({ kind })
    await act(async () => { fireEvent.click(btn()) })
    expect(fetchConversation.mock.calls.length).toBe(before + 1)
    expect(btn().textContent).toContain('重整中…')
    expect(btn().disabled).toBe(true)
    await act(async () => { land({ kind: 'ok', page: { convKey: 'c-S', status: 's', statusAt: 5, entries: [entry(7, { kind: 'refresh', state: 'pending' })], todos: null, refreshAvailable: true } }) })
    expect(btn().textContent).toContain('重整中…') // now because the conversation holds the pending entry
  })
  it('409 refresh_pending: shows 重整中… while the conversation is asked again, then follows the store', async () => {
    seed({ refreshAvailable: true }); mount(); await settle()
    const before = fetchConversation.mock.calls.length
    let release: () => void = () => {}
    fetchConversation.mockReturnValue(new Promise<never>((_, rej) => { release = () => rej(new Error('x')) }))
    stubRefresh({ kind: 'refresh_pending' })
    await act(async () => { fireEvent.click(btn()) })
    expect(fetchConversation.mock.calls.length).toBe(before + 1)
    expect(btn().textContent).toContain('重整中…')
    await act(async () => { release(); await Promise.resolve() })
    expect(btn().textContent).not.toContain('重整中')
  })
})
