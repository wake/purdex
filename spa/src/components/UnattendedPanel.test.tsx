// spa/src/components/UnattendedPanel.test.tsx — the "while you were away" list under the title bar's ▾ (unattended spec
// D-U23-6; plan PU-2c): the real panel over the real host store; only the API is mocked.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { createRef } from 'react'
import { UnattendedPanel } from './UnattendedPanel'
import { useHostStore } from '../stores/useHostStore'
import { useI18nStore } from '../stores/useI18nStore'
import { ApprovalApiError } from '../lib/team/approval-api'
import { getUnattended } from '../lib/team/unattended-api'
import type { Approval, UnattendedView } from '../lib/team/types'

vi.mock('../lib/team/unattended-api', () => ({ getUnattended: vi.fn() }))
const mockedGet = vi.mocked(getUnattended)

const A = 'host-a'
const B = 'host-b'
const C = 'host-c'
const at = (h: number, m: number) => new Date(2026, 9, 8, h, m).getTime()

const approved = (id: string, decidedAt: number, over: Partial<Approval> = {}): Approval => ({
  id, kind: 'self_relay', host_id: 'd1',
  origin: { session_id: `S-${id}`, ref: `_${id}`, name: `sess-${id}`, pid: 1, proc_start: 'x', cwd: '/w', tmux: 'p:@1.%1' },
  payload: {}, state: 'approved', created_at: decidedAt - 1_000, deadline_at: decidedAt + 540_000, lease_until: decidedAt + 30_000,
  decided_by: { kind: 'unattended', label: '無人值守模式' }, decided_at: decidedAt,
  ...over,
})
const page = (rows: Approval[], over: Partial<UnattendedView> = {}): UnattendedView => ({
  on: true, since: at(8, 0), changed_at: at(8, 0), approved: rows, truncated: false, ...over,
})

const rows = () => screen.getAllByTestId('unattended-row').map((r) => r.textContent)
const flush = () => act(async () => { await new Promise<void>((r) => setTimeout(r, 0)) })

function open(hostIds: string[], onClose = vi.fn()) {
  const anchorRef = createRef<HTMLDivElement>()
  const utils = render(<><div ref={anchorRef} /><UnattendedPanel hostIds={hostIds} anchorRef={anchorRef} onClose={onClose} /></>)
  return { ...utils, onClose }
}

beforeEach(() => {
  useI18nStore.getState().setLocale('zh-TW')
  useHostStore.setState({
    hosts: {
      [A]: { id: A, name: 'mlab', ip: '1', port: 1, token: 't', order: 0 },
      [B]: { id: B, name: 'air26', ip: '2', port: 2, token: 't', order: 1 },
      [C]: { id: C, name: 'air19', ip: '3', port: 3, token: 't', order: 2 },
    },
    hostOrder: [A, B, C], activeHostId: A, runtime: {},
  })
  mockedGet.mockReset()
})
afterEach(() => { vi.useRealTimers(); useHostStore.getState().reset() })

describe('UnattendedPanel', () => {
  it('merges two hosts newest first: <host>：<session> · <kind> · <HH:mm>', async () => {
    mockedGet.mockImplementation(async (hostId) => hostId === A
      ? page([approved('a2', at(9, 30)), approved('a1', at(7, 5))])
      : page([approved('b1', at(8, 45))]))
    open([A, B])
    await waitFor(() => expect(rows()).toHaveLength(3))
    expect(rows()).toEqual([
      'mlab：sess-a2 · 接力申請 · 09:30',
      'air26：sess-b1 · 接力申請 · 08:45',
      'mlab：sess-a1 · 接力申請 · 07:05',
    ])
    expect(mockedGet.mock.calls).toEqual([[A], [B]])
  })

  it('empty state: nothing approved on any host', async () => {
    mockedGet.mockResolvedValue(page([]))
    open([A, B])
    expect(await screen.findByTestId('unattended-empty')).toHaveTextContent('自上次開啟以來，沒有自動通過的申請')
    expect(screen.queryByTestId('unattended-row')).toBeNull()
    expect(screen.queryByTestId('unattended-more')).toBeNull()
  })

  it('shows nothing, not the empty state, until every first page has answered', async () => {
    let release: (v: UnattendedView) => void = () => {}
    mockedGet.mockImplementation(() => new Promise((res) => { release = res }))
    open([A])
    await flush()
    expect(screen.queryByTestId('unattended-empty')).toBeNull()
    expect(screen.queryByTestId('unattended-row')).toBeNull()
    await act(async () => { release(page([])) })
    expect(await screen.findByTestId('unattended-empty')).toBeInTheDocument()
  })

  it('a host whose GET failed is named; the other hosts\' rows still show', async () => {
    mockedGet.mockImplementation(async (hostId) => {
      if (hostId === B) throw new ApprovalApiError(503, 'not_ready')
      return page([approved('a1', at(9, 0))])
    })
    open([A, B])
    await waitFor(() => expect(rows()).toHaveLength(1))
    const failed = screen.getAllByTestId('unattended-host-failed')
    expect(failed).toHaveLength(1)
    expect(failed[0]).toHaveTextContent('air26：無法讀取（not_ready）')
    expect(screen.queryByTestId('unattended-empty')).toBeNull()
  })

  it('kind labels for lead and self_relay', async () => {
    mockedGet.mockResolvedValue(page([
      approved('l1', at(9, 0), { kind: 'lead' }),
      approved('r1', at(8, 0), { kind: 'self_relay' }),
    ]))
    open([A])
    await waitFor(() => expect(rows()).toHaveLength(2))
    expect(rows()).toEqual(['mlab：sess-l1 · lead 申請 · 09:00', 'mlab：sess-r1 · 接力申請 · 08:00'])
  })

  it('names the session by its title when it has one', async () => {
    const r = approved('t1', at(9, 0))
    r.origin = { ...r.origin, title: '重構小隊' }
    mockedGet.mockResolvedValue(page([r]))
    open([A])
    await waitFor(() => expect(rows()).toEqual(['mlab：重構小隊 · 接力申請 · 09:00']))
  })

  it('shows when the switch was last turned on, from the earliest answering host', async () => {
    vi.useFakeTimers({ toFake: ['Date'] })
    vi.setSystemTime(at(12, 0))
    mockedGet.mockImplementation(async (hostId) => hostId === A
      ? page([], { since: at(8, 30) })
      : page([], { since: at(7, 15) }))
    open([A, B])
    expect(await screen.findByTestId('unattended-since')).toHaveTextContent('自 07:15 起')
  })

  it('adds the date to the "since" line when the switch was turned on on another day', async () => {
    vi.useFakeTimers({ toFake: ['Date'] })
    vi.setSystemTime(at(12, 0))
    mockedGet.mockResolvedValue(page([], { since: new Date(2026, 9, 6, 7, 15).getTime() }))
    open([A])
    expect(await screen.findByTestId('unattended-since')).toHaveTextContent('自 10/6 07:15 起')
  })

  it('shows no "since" line when no answering host has ever been on', async () => {
    mockedGet.mockResolvedValue(page([], { on: false, since: 0, changed_at: 0 }))
    open([A])
    await screen.findByTestId('unattended-empty')
    expect(screen.queryByTestId('unattended-since')).toBeNull()
  })

  describe('顯示更多', () => {
    it('fetches the next page of the truncated host only and merges it in order', async () => {
      mockedGet.mockImplementation(async (hostId, q) => {
        if (hostId === A && q?.before === undefined) return page([approved('a3', at(9, 0)), approved('a2', at(8, 0))], { truncated: true, next_before: at(8, 0) })
        if (hostId === A && q?.before === at(8, 0)) return page([approved('a1', at(6, 0))])
        return page([approved('b1', at(7, 0))]) // B is not truncated
      })
      open([A, B])
      await waitFor(() => expect(rows()).toHaveLength(3))
      expect(mockedGet).toHaveBeenCalledTimes(2)
      fireEvent.click(screen.getByTestId('unattended-more'))
      await waitFor(() => expect(rows()).toHaveLength(4))
      expect(rows()).toEqual([
        'mlab：sess-a3 · 接力申請 · 09:00',
        'mlab：sess-a2 · 接力申請 · 08:00',
        'air26：sess-b1 · 接力申請 · 07:00',
        'mlab：sess-a1 · 接力申請 · 06:00',
      ])
      expect(mockedGet).toHaveBeenCalledTimes(3)
      expect(mockedGet).toHaveBeenLastCalledWith(A, { before: at(8, 0) })
    })

    it('the button goes when no host is truncated', async () => {
      mockedGet.mockImplementation(async (_h, q) => q?.before === undefined
        ? page([approved('a2', at(9, 0))], { truncated: true, next_before: at(9, 0) })
        : page([approved('a1', at(8, 0))]))
      open([A])
      fireEvent.click(await screen.findByTestId('unattended-more'))
      await waitFor(() => expect(rows()).toHaveLength(2))
      expect(screen.queryByTestId('unattended-more')).toBeNull()
    })

    it('keeps the button while one of two hosts has more, and pages each host by its own cursor', async () => {
      mockedGet.mockImplementation(async (hostId, q) => {
        if (q?.before === undefined) return page([approved(`${hostId}-2`, at(9, 0))], { truncated: true, next_before: hostId === A ? 111 : 222 })
        if (hostId === A) return page([approved('a-1', at(5, 0))]) // A ends here
        return page([approved('b-1', at(4, 0))], { truncated: true, next_before: 333 }) // B still has more
      })
      open([A, B])
      fireEvent.click(await screen.findByTestId('unattended-more'))
      await waitFor(() => expect(rows()).toHaveLength(4))
      expect(mockedGet).toHaveBeenCalledWith(A, { before: 111 })
      expect(mockedGet).toHaveBeenCalledWith(B, { before: 222 })
      expect(screen.getByTestId('unattended-more')).toBeInTheDocument()
      fireEvent.click(screen.getByTestId('unattended-more'))
      await waitFor(() => expect(mockedGet).toHaveBeenCalledWith(B, { before: 333 }))
      expect(mockedGet.mock.calls.filter(([h]) => h === A)).toHaveLength(2) // A not asked a third time
    })

    it('a failed next page keeps the rows and the button, and names the host', async () => {
      mockedGet.mockImplementation(async (_h, q) => {
        if (q?.before === undefined) return page([approved('a2', at(9, 0))], { truncated: true, next_before: 5 })
        throw new ApprovalApiError(0, 'network', 'Failed to fetch')
      })
      open([A])
      fireEvent.click(await screen.findByTestId('unattended-more'))
      await waitFor(() => expect(screen.getByTestId('unattended-host-failed')).toHaveTextContent('mlab：無法讀取（network）'))
      expect(rows()).toHaveLength(1)
      expect(screen.getByTestId('unattended-more')).toBeInTheDocument()
    })

    it('a second click while a page is in flight sends nothing more', async () => {
      let release: (v: UnattendedView) => void = () => {}
      mockedGet.mockImplementation((_h, q) => q?.before === undefined
        ? Promise.resolve(page([approved('a2', at(9, 0))], { truncated: true, next_before: 5 }))
        : new Promise((res) => { release = res }))
      open([A])
      const more = await screen.findByTestId('unattended-more')
      fireEvent.click(more)
      fireEvent.click(more)
      expect(mockedGet).toHaveBeenCalledTimes(2)
      await act(async () => { release(page([])) })
    })
  })

  it('an answer that arrives after the panel closed is dropped without an error', async () => {
    let release: (v: UnattendedView) => void = () => {}
    mockedGet.mockImplementation(() => new Promise((res) => { release = res }))
    const { unmount } = open([A])
    unmount()
    await act(async () => { release(page([approved('a1', at(9, 0))])) })
    expect(screen.queryByTestId('unattended-row')).toBeNull()
  })

  it('the panel title and the close button come from the FloatingPanel', async () => {
    mockedGet.mockResolvedValue(page([]))
    const { onClose } = open([A])
    const panel = await screen.findByTestId('unattended-panel')
    expect(within(panel).getByText('期間自動通過')).toBeInTheDocument()
    fireEvent.click(within(panel).getByTestId('floating-panel-close'))
    expect(onClose).toHaveBeenCalled()
  })
})
