// spa/src/components/UnattendedPanel.test.tsx — the "while you were away" list under the title bar's ▾ (unattended spec
// D-U23-6; plan PU-2c): the real panel over the real host store; only the API is mocked.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { createRef, StrictMode } from 'react'
import { UnattendedPanel } from './UnattendedPanel'
import { useHostStore } from '../stores/useHostStore'
import { useI18nStore } from '../stores/useI18nStore'
import { ApprovalApiError } from '../lib/team/approval-api'
import { getUnattended } from '../lib/team/unattended-api'
import { useUnattendedStore } from '../stores/useUnattendedStore'
import { useTeamRosterStore } from '../stores/useTeamRosterStore'
import { useRelayQuotaStore, quotaKey } from '../lib/team/relay-quota'
import { refetchHost, resetWriter } from '../lib/team/relay-quota-writer'
import type { Approval, SessionQuota, UnattendedView } from '../lib/team/types'

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
/** The (host, query) of every call, without the abort signal each carries. */
const askedOf = () => mockedGet.mock.calls.map(([h, q]) => (q === undefined ? [h] : [h, q]))
const flush = () => act(async () => { await new Promise<void>((r) => setTimeout(r, 0)) })

function open(hostIds: string[], onClose = vi.fn(), unreachableIds: string[] = []) {
  const anchorRef = createRef<HTMLDivElement>()
  const utils = render(<><div ref={anchorRef} /><UnattendedPanel hostIds={hostIds} unreachableIds={unreachableIds} anchorRef={anchorRef} onClose={onClose} /></>)
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
  vi.useFakeTimers({ toFake: ['Date'] })
  vi.setSystemTime(at(12, 0)) // the rows' time shows the date when it is not today
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
    expect(askedOf()).toEqual([[A], [B]])
  })

  it('empty state: nothing approved on any host', async () => {
    mockedGet.mockResolvedValue(page([]))
    open([A, B])
    expect(await screen.findByTestId('unattended-empty')).toHaveTextContent('自這次開啟無人值守以來，沒有自動通過的申請')
    expect(screen.queryByTestId('unattended-row')).toBeNull()
    expect(screen.queryByTestId('unattended-more')).toBeNull()
  })

  // #2063 (the user's screenshot): nothing inside is focusable, so the panel itself holds focus and must not draw a ring.
  it('empty state: the container holds focus and carries no focus outline', async () => {
    mockedGet.mockResolvedValue(page([]))
    open([A, B])
    await screen.findByTestId('unattended-empty')
    const panel = screen.getByTestId('unattended-panel')
    expect(document.activeElement === panel || panel.contains(document.activeElement)).toBe(true)
    expect(panel).toHaveClass('outline-hidden')
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

  it('a GET answer flagged list_failed is a failed read (not an empty page): named list_failed, the other host\'s rows still show', async () => {
    mockedGet.mockImplementation(async (hostId) => hostId === B
      ? page([], { list_failed: true })
      : page([approved('a1', at(9, 0))]))
    open([A, B])
    await waitFor(() => expect(rows()).toHaveLength(1))
    expect(screen.getByTestId('unattended-host-failed')).toHaveTextContent('air26：無法讀取（list_failed）')
  })

  it('a next page flagged list_failed keeps the rows and the cursor, and names the host', async () => {
    mockedGet.mockImplementation(async (_h, q) => q?.before === undefined
      ? page([approved('a2', at(9, 0))], { truncated: true, next_before: 5 })
      : page([], { list_failed: true }))
    open([A])
    fireEvent.click(await screen.findByTestId('unattended-more'))
    await waitFor(() => expect(screen.getByTestId('unattended-host-failed')).toHaveTextContent('mlab：無法讀取（list_failed）'))
    expect(rows()).toHaveLength(1)
    expect(screen.getByTestId('unattended-more')).not.toBeDisabled()
    fireEvent.click(screen.getByTestId('unattended-more'))
    expect(mockedGet).toHaveBeenLastCalledWith(A, { before: 5 }, expect.any(AbortSignal)) // retried with the kept cursor
  })

  it('a shown host that cannot be reached is named above the list and not asked; the reachable host\'s rows still show', async () => {
    mockedGet.mockResolvedValue(page([approved('a1', at(9, 0))]))
    open([A], vi.fn(), [B])
    await waitFor(() => expect(rows()).toHaveLength(1))
    const lines = screen.getAllByTestId('unattended-host-unreachable')
    expect(lines).toHaveLength(1)
    expect(lines[0]).toHaveTextContent('air26：無法連線，可能仍在自動通過')
    expect(lines[0].compareDocumentPosition(screen.getByTestId('unattended-row')) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    expect(askedOf()).toEqual([[A]])
    expect(screen.queryByTestId('unattended-empty')).toBeNull()
  })

  it('every shown host unreachable: only the unreachable lines, no empty state, nothing asked', async () => {
    open([], vi.fn(), [A, B])
    await flush()
    const lines = screen.getAllByTestId('unattended-host-unreachable')
    expect(lines.map((l) => l.textContent)).toEqual(['mlab：無法連線，可能仍在自動通過', 'air26：無法連線，可能仍在自動通過'])
    expect(screen.queryByTestId('unattended-empty')).toBeNull()
    expect(screen.queryByTestId('unattended-row')).toBeNull()
    expect(mockedGet).not.toHaveBeenCalled()
  })

  it('no unreachable host: no unreachable line', async () => {
    mockedGet.mockResolvedValue(page([]))
    open([A])
    await screen.findByTestId('unattended-empty')
    expect(screen.queryByTestId('unattended-host-unreachable')).toBeNull()
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

  it('a row from another day carries its date, so yesterday and today at the same time can be told apart', async () => {
    const yesterday = new Date(2026, 9, 7, 9, 30).getTime()
    mockedGet.mockResolvedValue(page([approved('t1', at(9, 30)), approved('y1', yesterday)]))
    open([A])
    await waitFor(() => expect(rows()).toHaveLength(2))
    expect(rows()).toEqual([
      'mlab：sess-t1 · 接力申請 · 09:30',
      'mlab：sess-y1 · 接力申請 · 10/7 09:30',
    ])
  })

  it('a row without decided_at takes its time (and date) from created_at', async () => {
    const r = approved('c1', at(9, 0), { created_at: new Date(2026, 9, 6, 22, 5).getTime() })
    delete r.decided_at
    mockedGet.mockResolvedValue(page([r]))
    open([A])
    await waitFor(() => expect(rows()).toEqual(['mlab：sess-c1 · 接力申請 · 10/6 22:05']))
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
      expect(mockedGet).toHaveBeenLastCalledWith(A, { before: at(8, 0) }, expect.any(AbortSignal))
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
      expect(mockedGet).toHaveBeenCalledWith(A, { before: 111 }, expect.any(AbortSignal))
      expect(mockedGet).toHaveBeenCalledWith(B, { before: 222 }, expect.any(AbortSignal))
      expect(screen.getByTestId('unattended-more')).toBeInTheDocument()
      fireEvent.click(screen.getByTestId('unattended-more'))
      await waitFor(() => expect(mockedGet).toHaveBeenCalledWith(B, { before: 333 }, expect.any(AbortSignal)))
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

  describe('one host that never answers', () => {
    const busy = () => document.querySelector('[aria-busy]')!.getAttribute('aria-busy')
    const settle = (ms: number) => act(async () => { await vi.advanceTimersByTimeAsync(ms) })

    it('does not hold back the other host: its rows show at once and the panel is no longer busy', async () => {
      vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout', 'Date'] }); vi.setSystemTime(at(12, 0))
      mockedGet.mockImplementation((hostId) => hostId === B
        ? new Promise<UnattendedView>(() => {}) // B never answers
        : Promise.resolve(page([approved('a1', at(9, 0))])))
      open([A, B])
      await settle(0)
      expect(rows()).toEqual(['mlab：sess-a1 · 接力申請 · 09:00'])
      expect(busy()).toBe('false')
      // B has not answered: the list is not "empty", and B is not (yet) named as failed.
      expect(screen.queryByTestId('unattended-empty')).toBeNull()
      expect(screen.queryByTestId('unattended-host-failed')).toBeNull()
    })

    it('stays busy, and says nothing is empty, while no host has answered', async () => {
      vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout', 'Date'] }); vi.setSystemTime(at(12, 0))
      mockedGet.mockImplementation(() => new Promise<UnattendedView>(() => {}))
      open([A, B])
      await settle(0)
      expect(busy()).toBe('true')
      expect(screen.queryByTestId('unattended-empty')).toBeNull()
    })

    it('gives up on the silent host after 10 s: a failed line (timeout), and its request is aborted', async () => {
      vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout', 'Date'] }); vi.setSystemTime(at(12, 0))
      const signals: Record<string, AbortSignal | undefined> = {}
      mockedGet.mockImplementation((hostId, _q, signal) => {
        signals[hostId] = signal
        return hostId === B ? new Promise<UnattendedView>(() => {}) : Promise.resolve(page([approved('a1', at(9, 0))]))
      })
      open([A, B])
      await settle(9_999)
      expect(screen.queryByTestId('unattended-host-failed')).toBeNull()
      expect(signals[B]!.aborted).toBe(false)
      await settle(1)
      expect(screen.getByTestId('unattended-host-failed')).toHaveTextContent('air26：無法讀取（timeout）')
      expect(signals[B]!.aborted).toBe(true)
      expect(rows()).toHaveLength(1)
    })

    it('every host silent: all are named after the timeout, and the list is not called empty', async () => {
      vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout', 'Date'] }); vi.setSystemTime(at(12, 0))
      mockedGet.mockImplementation(() => new Promise<UnattendedView>(() => {}))
      open([A, B])
      await settle(10_000)
      expect(screen.getAllByTestId('unattended-host-failed').map((f) => f.textContent)).toEqual(['mlab：無法讀取（timeout）', 'air26：無法讀取（timeout）'])
      expect(screen.queryByTestId('unattended-empty')).toBeNull()
      expect(busy()).toBe('false')
    })

    it('an answer that beats the timeout is not turned into a failure later', async () => {
      vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout', 'Date'] }); vi.setSystemTime(at(12, 0))
      mockedGet.mockResolvedValue(page([approved('a1', at(9, 0))]))
      open([A])
      await settle(0)
      await settle(20_000)
      expect(screen.queryByTestId('unattended-host-failed')).toBeNull()
      expect(rows()).toHaveLength(1)
    })

    it('顯示更多: one stuck host does not hold back the other\'s next page, and keeps its cursor after the timeout', async () => {
      vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout', 'Date'] }); vi.setSystemTime(at(12, 0))
      mockedGet.mockImplementation((hostId, q) => {
        if (q?.before === undefined) return Promise.resolve(page([approved(`${hostId}-2`, at(9, 0))], { truncated: true, next_before: hostId === A ? 111 : 222 }))
        return hostId === B ? new Promise<UnattendedView>(() => {}) : Promise.resolve(page([approved('a-1', at(5, 0))]))
      })
      open([A, B])
      await settle(0)
      expect(rows()).toHaveLength(2)
      fireEvent.click(screen.getByTestId('unattended-more'))
      await settle(0)
      expect(rows()).toHaveLength(3) // A's second page is in while B is still silent
      expect(screen.getByTestId('unattended-more')).toBeDisabled()
      await settle(10_000)
      expect(screen.getByTestId('unattended-host-failed')).toHaveTextContent('air26：無法讀取（timeout）')
      expect(rows()).toHaveLength(3)
      expect(screen.getByTestId('unattended-more')).not.toBeDisabled()
      // retry asks B again with its kept cursor
      fireEvent.click(screen.getByTestId('unattended-more'))
      expect(mockedGet).toHaveBeenLastCalledWith(B, { before: 222 }, expect.any(AbortSignal))
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

  describe('lifecycle (PU-2c critic)', () => {
    /** Every call's signal and its release, in order, per host. */
    function controlled() {
      const calls: { hostId: string; before?: number; signal?: AbortSignal; release: (v: UnattendedView) => void }[] = []
      mockedGet.mockImplementation((hostId, q, signal) => new Promise<UnattendedView>((res) => {
        calls.push({ hostId, before: q?.before, signal, release: res })
      }))
      return calls
    }

    it('StrictMode: the first setup\'s reads are aborted and a late old answer never overwrites the newer one', async () => {
      const calls = controlled()
      const anchorRef = createRef<HTMLDivElement>()
      render(<StrictMode><div ref={anchorRef} /><UnattendedPanel hostIds={[A]} anchorRef={anchorRef} onClose={vi.fn()} /></StrictMode>)
      await flush()
      expect(calls).toHaveLength(2) // setup, cleanup, setup
      expect(calls[0].signal!.aborted).toBe(true)
      expect(calls[1].signal!.aborted).toBe(false)
      await act(async () => { calls[1].release(page([approved('new', at(9, 0))])) })
      expect(rows()).toEqual(['mlab：sess-new · 接力申請 · 09:00'])
      await act(async () => { calls[0].release(page([approved('old', at(7, 0))])) }) // the cancelled one arrives late
      expect(rows()).toEqual(['mlab：sess-new · 接力申請 · 09:00'])
    })

    it('StrictMode: a late old answer does not commit even before the newer one has arrived', async () => {
      const calls = controlled()
      const anchorRef = createRef<HTMLDivElement>()
      render(<StrictMode><div ref={anchorRef} /><UnattendedPanel hostIds={[A]} anchorRef={anchorRef} onClose={vi.fn()} /></StrictMode>)
      await flush()
      await act(async () => { calls[0].release(page([approved('old', at(7, 0))])) })
      expect(screen.queryByTestId('unattended-row')).toBeNull()
      expect(document.querySelector('[aria-busy]')!.getAttribute('aria-busy')).toBe('true')
    })

    it('closing the panel aborts the first read and 「顯示更多」, and leaves no timer behind', async () => {
      vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout', 'Date'] }); vi.setSystemTime(at(12, 0))
      const calls = controlled()
      const { unmount } = open([A])
      await act(async () => { await vi.advanceTimersByTimeAsync(0) })
      expect(calls).toHaveLength(1)
      expect(vi.getTimerCount()).toBeGreaterThan(0)
      unmount()
      expect(calls[0].signal!.aborted).toBe(true)
      expect(vi.getTimerCount()).toBe(0)
    })

    it('closing the panel with a 「顯示更多」 read in flight aborts that read and clears its timer', async () => {
      vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout', 'Date'] }); vi.setSystemTime(at(12, 0))
      const calls = controlled()
      const { unmount } = open([A])
      await act(async () => { await vi.advanceTimersByTimeAsync(0) })
      await act(async () => { calls[0].release(page([approved('a2', at(9, 0))], { truncated: true, next_before: 5 })) })
      fireEvent.click(screen.getByTestId('unattended-more'))
      await act(async () => { await vi.advanceTimersByTimeAsync(0) })
      expect(calls).toHaveLength(2)
      expect(calls[1].signal!.aborted).toBe(false)
      unmount()
      expect(calls[1].signal!.aborted).toBe(true)
      expect(vi.getTimerCount()).toBe(0)
    })
  })

  it('the panel title and the close button come from the FloatingPanel', async () => {
    mockedGet.mockResolvedValue(page([]))
    const { onClose } = open([A])
    const panel = await screen.findByTestId('unattended-panel')
    expect(within(panel).getByText('無人值守期間自動通過')).toBeInTheDocument()
    fireEvent.click(within(panel).getByTestId('floating-panel-close'))
    expect(onClose).toHaveBeenCalled()
  })
})

// ---- relay quota (plan RQ-A Task 6) ----
describe('UnattendedPanel relay quota', () => {
  const quota = (id: string, over: Partial<SessionQuota> = {}): SessionQuota => ({
    session_id: id, root_session_id: `root-${id}`, title: `T-${id}`, address: `mlab/${id}-xx`, is_lead: false,
    self_left: 1, member_pool_left: 0, rev: 3, ...over,
  })
  const heldRow = (id: string, createdAt: number): Approval =>
    approved(id, createdAt + 1_000, { state: 'open', created_at: createdAt, decided_by: undefined, decided_at: undefined })

  beforeEach(() => {
    useUnattendedStore.getState().reset()
    useTeamRosterStore.getState().reset()
    useRelayQuotaStore.getState().reset()
    resetWriter()
  })

  it('no quota section for a host without the capability, even if its view happens to carry rows', async () => {
    useUnattendedStore.getState().setQuotaSupport(A, 'no')
    mockedGet.mockResolvedValue(page([], { quotas: [quota('p1')] }))
    open([A])
    await flush()
    expect(screen.queryByTestId('quota-section')).toBeNull()
    expect(useRelayQuotaStore.getState().confirmed).toEqual({})
  })

  it('with the capability: rows from the first page, the numbers seeded into the store by (host, root)', async () => {
    useUnattendedStore.getState().setQuotaSupport(A, 'yes')
    useTeamRosterStore.getState().apply(A, [])
    mockedGet.mockResolvedValue(page([], { quotas: [quota('p1', { self_left: 4, rev: 9 })] }))
    open([A])
    expect(await screen.findByTestId('quota-row')).toHaveTextContent('T-p1')
    expect(useRelayQuotaStore.getState().confirmed[quotaKey(A, 'root-p1')]).toEqual({ self_left: 4, member_pool_left: 0, rev: 9 })
  })

  it('events that arrive while the GET is out are applied after it, by the rev rule', async () => {
    useUnattendedStore.getState().setQuotaSupport(A, 'yes')
    useTeamRosterStore.getState().apply(A, [])
    let release!: (v: UnattendedView) => void
    mockedGet.mockReturnValue(new Promise<UnattendedView>((r) => { release = r }))
    open([A])
    act(() => { useRelayQuotaStore.getState().applyEvent(A, { op: 'changed', root_session_id: 'root-p1', self_left: 8, member_pool_left: 0, rev: 12 }) })
    expect(useRelayQuotaStore.getState().confirmed).toEqual({}) // buffered
    await act(async () => { release(page([], { quotas: [quota('p1', { self_left: 2, rev: 10 })] })) })
    expect(useRelayQuotaStore.getState().confirmed[quotaKey(A, 'root-p1')]).toEqual({ self_left: 8, member_pool_left: 0, rev: 12 })
    expect(await screen.findByTestId('quota-value')).toHaveTextContent('8')
  })

  it('a failed read still releases the buffered events', async () => {
    useUnattendedStore.getState().setQuotaSupport(A, 'yes')
    let fail!: (e: unknown) => void
    mockedGet.mockReturnValue(new Promise<UnattendedView>((_, rej) => { fail = rej }))
    open([A])
    act(() => { useRelayQuotaStore.getState().applyEvent(A, { op: 'changed', root_session_id: 'r', self_left: 5, member_pool_left: 0, rev: 2 }) })
    await act(async () => { fail(new ApprovalApiError(500, 'http_500')) })
    expect(useRelayQuotaStore.getState().confirmed[quotaKey(A, 'r')]?.self_left).toBe(5)
    expect(useRelayQuotaStore.getState().gets).toEqual({})
  })

  it('「讀不到額度」 when the daemon\'s quotas were null or malformed; 「沒有可設定的 session」 for []', async () => {
    useUnattendedStore.getState().setQuotaSupport(A, 'yes')
    useUnattendedStore.getState().setQuotaSupport(B, 'yes')
    useTeamRosterStore.getState().apply(A, [])
    useTeamRosterStore.getState().apply(B, [])
    mockedGet.mockImplementation(async (hostId) => hostId === A ? page([], { quotasFailed: true }) : page([], { quotas: [] }))
    open([A, B])
    expect(await screen.findByTestId('quota-unreadable')).toHaveTextContent('mlab：讀不到額度')
    expect(screen.getByTestId('quota-none')).toHaveTextContent('沒有可設定的 session')
    expect(screen.getAllByTestId('quota-host-heading').map((h) => h.textContent)).toEqual(['mlab', 'air26'])
  })

  it('the held section only when held is non-empty, newest first; the approved list below is unchanged', async () => {
    useUnattendedStore.getState().setQuotaSupport(A, 'yes')
    useTeamRosterStore.getState().apply(A, [])
    mockedGet.mockResolvedValue(page([approved('a1', at(9, 30))], { quotas: [], held: [heldRow('h1', at(10, 0)), heldRow('h2', at(10, 30))] }))
    open([A])
    await waitFor(() => expect(screen.getAllByTestId('held-row')).toHaveLength(2))
    expect(screen.getByTestId('held-title')).toHaveTextContent('額度用完，等你核准')
    expect(screen.getAllByTestId('held-row').map((r) => r.textContent)).toEqual(['mlab：sess-h2 · 接力申請 · 10:30', 'mlab：sess-h1 · 接力申請 · 10:00'])
    expect(rows()).toEqual(['mlab：sess-a1 · 接力申請 · 09:30'])
  })

  it('no held section when held is absent or empty', async () => {
    useUnattendedStore.getState().setQuotaSupport(A, 'yes')
    useTeamRosterStore.getState().apply(A, [])
    mockedGet.mockResolvedValue(page([], { quotas: [], held: [] }))
    open([A])
    await flush()
    expect(screen.queryByTestId('held-section')).toBeNull()
  })

  it('a re-read (after pending_lineage) replaces the host\'s rows, clears an old failure, and releases its events', async () => {
    useUnattendedStore.getState().setQuotaSupport(A, 'yes')
    useTeamRosterStore.getState().apply(A, [])
    mockedGet.mockResolvedValueOnce(page([], { quotasFailed: true }))
    open([A])
    expect(await screen.findByTestId('quota-unreadable')).toBeInTheDocument()
    mockedGet.mockResolvedValueOnce(page([], { quotas: [quota('p9', { root_session_id: 'real-root', self_left: 6 })] }))
    await act(async () => { refetchHost(A) })
    expect(await screen.findByTestId('quota-row')).toHaveTextContent('T-p9')
    expect(screen.queryByTestId('quota-unreadable')).toBeNull()
    expect(useRelayQuotaStore.getState().confirmed[quotaKey(A, 'real-root')]?.self_left).toBe(6)
  })

  it('closing the panel unregisters the re-read', async () => {
    useUnattendedStore.getState().setQuotaSupport(A, 'yes')
    useTeamRosterStore.getState().apply(A, [])
    mockedGet.mockResolvedValue(page([], { quotas: [] }))
    const view = open([A])
    await flush()
    view.unmount()
    mockedGet.mockClear()
    refetchHost(A)
    expect(mockedGet).not.toHaveBeenCalled()
  })
})

