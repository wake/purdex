// The host tab's 重指令排隊 section, 當下 (watcher area spec §6.1): lanes, the gate line, waiting cards, the table, hover pairing.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, fireEvent, within, act } from '@testing-library/react'
import { QueueSection } from './QueueSection'
import { clearViewMemory, readQueueTab, writeQueueTab } from '../../lib/watch/view-memory'
import type { FleetQueue, FleetHolder, FleetWaiter } from '../../lib/fleet/types'
import { useI18nStore } from '../../stores/useI18nStore'
import { useNexHostStore } from '../../stores/useNexHostStore'
const t = (k: string, p?: Record<string, string | number>) => useI18nStore.getState().t(k, p)

const NOW = Date.parse('2026-10-16T12:00:00Z')
const holder = (o: Partial<FleetHolder> = {}): FleetHolder => ({ class: 'heavy', slot: 1, command: 'go test ./x', project: 'purdex', session: '', since: NOW - 83_000, pid: 10, ...o })
const waiter = (o: Partial<FleetWaiter> = {}): FleetWaiter => ({ class: 'heavy', place: 1, command: 'pnpm build', project: 'spa', session: '', since: NOW - 30_000, ...o })
const queue = (o: Partial<FleetQueue> = {}): FleetQueue => ({
  heavy_used: 0, heavy_total: 2, light_used: 0, light_total: 3, holders: [], waiters: [], longest_wait_s: 0,
  admission_held: false, admission_reason: '', heavy_paused: false, light_paused: false, ...o,
})
const renderQ = (q: FleetQueue | undefined, offline = false) => render(<QueueSection fleetHostId="d-a" queue={q} offline={offline} />)
const lane = (c: 'heavy' | 'light') => within(screen.getByTestId(`lane-${c}`))

beforeEach(() => { clearViewMemory(); useNexHostStore.setState({ byHost: {} } as never);vi.useFakeTimers(); vi.setSystemTime(NOW) })
afterEach(() => vi.useRealTimers())

describe('QueueSection — lanes', () => {
  it('slot cards: running ones with command and elapsed, free ones dashed', () => {
    renderQ(queue({ heavy_used: 1, holders: [holder()] }))
    const heavy = lane('heavy')
    expect(heavy.getByText(t('watch.queue.slots', { used: 1, total: 2 }))).toBeInTheDocument()
    expect(heavy.getAllByTestId('q-holder')).toHaveLength(1)
    expect(heavy.getByTestId('q-holder')).toHaveTextContent('go test ./x')
    expect(heavy.getByTestId('q-holder')).toHaveTextContent('1:23')
    expect(heavy.getAllByTestId('q-free')).toHaveLength(1)
    expect(heavy.getByTestId('q-free')).toHaveTextContent(t('watch.queue.free'))
    expect(lane('light').getAllByTestId('q-free')).toHaveLength(3)
  })

  it('a paused class (flag or total 0) says 已暫停 and its gate line is the warning colour', () => {
    renderQ(queue({ heavy_total: 0, heavy_paused: true, light_paused: true, light_total: 2 }))
    for (const c of ['heavy', 'light'] as const) {
      expect(lane(c).getByText(t('watch.queue.paused'))).toBeInTheDocument()
      const gate = lane(c).getByTestId('gate')
      expect(gate).toHaveAttribute('data-state', 'paused')
      expect(gate).toHaveTextContent(t('watch.queue.gate.paused'))
      expect(within(gate).getByText(t('watch.queue.gate.paused')).className).toContain('text-status-warning-text')
    }
  })

  it('a total of 0 without the flag is paused as well', () => {
    renderQ(queue({ light_total: 0 }))
    expect(lane('light').getByText(t('watch.queue.paused'))).toBeInTheDocument()
    expect(lane('heavy').queryByText(t('watch.queue.paused'))).toBeNull()
  })
})

describe('QueueSection — the gate line', () => {
  it('open with a free slot: no word', () => {
    renderQ(queue())
    const gate = lane('heavy').getByTestId('gate')
    expect(gate).toHaveAttribute('data-state', 'open')
    expect(gate.textContent).toBe('')
  })
  it('slots full: 等名額', () => {
    renderQ(queue({ heavy_used: 2, holders: [holder(), holder({ slot: 2, pid: 11 })] }))
    const gate = lane('heavy').getByTestId('gate')
    expect(gate).toHaveAttribute('data-state', 'wait')
    expect(gate).toHaveTextContent(t('watch.queue.gate.wait'))
    expect(lane('light').getByTestId('gate')).toHaveAttribute('data-state', 'open')
  })
  it('admission held: 暫停放行 in the error colour, the reason as its title', () => {
    renderQ(queue({ admission_held: true, admission_reason: 'memory pressure: critical' }))
    const gate = lane('heavy').getByTestId('gate')
    expect(gate).toHaveAttribute('data-state', 'held')
    const word = within(gate).getByText(t('watch.queue.gate.held'))
    expect(word.className).toContain('text-status-error-text')
    expect(gate).toHaveAttribute('title', 'memory pressure: critical')
  })
})

describe('QueueSection — waiting', () => {
  it('count and longest: 排隊 n · 最久 m:ss, counted from since', () => {
    renderQ(queue({ waiters: [waiter({ since: NOW - 30_000 }), waiter({ place: 2, since: NOW - 95_000 })] }))
    expect(screen.getByTestId('queue-count')).toHaveTextContent(t('watch.queue.count', { n: 2 }) + t('watch.queue.longest', { t: '1:35' }))
    act(() => { vi.advanceTimersByTime(5000) })
    expect(screen.getByTestId('queue-count')).toHaveTextContent(t('watch.queue.longest', { t: '1:40' }))
  })

  it('nobody waiting: the count has no 最久, the warning colour is off', () => {
    renderQ(queue())
    const c = screen.getByTestId('queue-count')
    expect(c).toHaveTextContent(t('watch.queue.count', { n: 0 }))
    expect(c.textContent).not.toContain(t('watch.queue.longest', { t: '' }).trim().slice(0, 4))
    expect(c.querySelector('.text-status-warning-text')).toBeNull()
  })

  it('cards show place, command and wait; the wait turns warning past 2 minutes', () => {
    renderQ(queue({ waiters: [waiter({ since: NOW - 119_000 })] }))
    const card = lane('heavy').getByTestId('q-waiting')
    expect(card).toHaveTextContent('1')
    expect(card).toHaveTextContent('pnpm build')
    const wait = within(card).getByTestId('q-wait')
    expect(wait).toHaveTextContent('1:59')
    expect(wait.className).not.toContain('text-status-warning-text')
    act(() => { vi.advanceTimersByTime(2000) })
    expect(wait).toHaveTextContent('2:01')
    expect(wait.className).toContain('text-status-warning-text')
  })

  it('lanes keep their own queues', () => {
    renderQ(queue({ waiters: [waiter(), waiter({ class: 'light', command: 'git status' })] }))
    expect(lane('heavy').getAllByTestId('q-waiting')).toHaveLength(1)
    expect(lane('light').getByTestId('q-waiting')).toHaveTextContent('git status')
  })
})

describe('QueueSection — the table', () => {
  it('groups 執行中 and 排隊中（n） with project and the gate pid', () => {
    renderQ(queue({ heavy_used: 1, holders: [holder({ pid: 4242 })], waiters: [waiter({ project: 'spa' })] }))
    const table = within(screen.getByTestId('queue-table'))
    expect(table.getByText(t('watch.queue.group_running'))).toBeInTheDocument()
    expect(table.getByText(t('watch.queue.group_waiting', { n: 1 }))).toBeInTheDocument()
    const rows = table.getAllByTestId('q-row')
    expect(rows).toHaveLength(2)
    expect(rows[0]).toHaveTextContent('purdex')
    expect(rows[0]).toHaveTextContent(t('watch.queue.pid', { pid: 4242 }))
    expect(rows[0]).toHaveTextContent(t('watch.queue.ran', { t: '1:23' }))
    expect(rows[1]).toHaveTextContent('spa')
    expect(rows[1]).toHaveTextContent(t('watch.queue.waited', { t: '0:30' }))
  })

  it('null since reads — and never NaN', () => {
    renderQ(queue({ heavy_used: 1, holders: [holder({ since: null })], waiters: [waiter({ since: null })] }))
    expect(screen.getByTestId('queue-now').textContent).not.toContain('NaN')
    expect(lane('heavy').getByTestId('q-holder')).toHaveTextContent('—')
    expect(lane('heavy').getByTestId('q-wait')).toHaveTextContent('—')
    expect(screen.getByTestId('queue-count').textContent).not.toContain('NaN')
  })

  it('20 holders and 20 waiters all render', () => {
    const holders = Array.from({ length: 20 }, (_, i) => holder({ slot: i + 1, pid: 100 + i, command: `job-h${i}` }))
    const waiters = Array.from({ length: 20 }, (_, i) => waiter({ place: i + 1, command: `job-w${i}` }))
    renderQ(queue({ heavy_used: 20, heavy_total: 20, holders, waiters }))
    expect(screen.getAllByTestId('q-holder')).toHaveLength(20)
    expect(screen.getAllByTestId('q-waiting')).toHaveLength(20)
    expect(screen.getAllByTestId('q-row')).toHaveLength(40)
  })
})

describe('QueueSection — hover pairing', () => {
  it('hovering a card lights its row, and a row its card', () => {
    renderQ(queue({ heavy_used: 1, holders: [holder()], waiters: [waiter()] }))
    const card = screen.getByTestId('q-holder')
    const rows = screen.getAllByTestId('q-row')
    expect(rows[0]).not.toHaveAttribute('data-hot')
    fireEvent.mouseEnter(card)
    expect(rows[0]).toHaveAttribute('data-hot')
    expect(rows[1]).not.toHaveAttribute('data-hot')
    fireEvent.mouseLeave(card)
    expect(rows[0]).not.toHaveAttribute('data-hot')
    fireEvent.mouseEnter(rows[1])
    expect(screen.getByTestId('q-waiting')).toHaveAttribute('data-hot')
    expect(card).not.toHaveAttribute('data-hot')
  })
})

describe('QueueSection — the clock', () => {
  it('one interval, cleared on unmount', () => {
    const set = vi.spyOn(globalThis, 'setInterval')
    const clear = vi.spyOn(globalThis, 'clearInterval')
    const { unmount } = renderQ(queue({ waiters: [waiter()] }))
    expect(set).toHaveBeenCalledTimes(1)
    unmount()
    expect(clear).toHaveBeenCalledTimes(1)
  })
})

describe('QueueSection — switch and states', () => {
  it('当下｜歷史: 歷史 is disabled until W5d, the choice goes to view memory', () => {
    renderQ(queue())
    const sw = within(screen.getByTestId('queue-tabs'))
    expect(sw.getByRole('button', { name: t('watch.queue.tab.now') })).toHaveAttribute('aria-pressed', 'true')
    expect(sw.getByRole('button', { name: t('watch.queue.tab.history') })).toBeDisabled()
    expect(readQueueTab('d-a')).toBe('now')
  })

  it('offline: the one sentence and nothing else', () => {
    renderQ(queue({ heavy_used: 1, holders: [holder()] }), true)
    expect(screen.getByText(t('watch.queue.offline'))).toBeInTheDocument()
    expect(screen.queryByTestId('queue-now')).toBeNull()
  })

  it('a title, in the section', () => {
    renderQ(queue())
    expect(screen.getByRole('button', { name: t('watch.queue.title') })).toBeInTheDocument()
  })
})

describe('QueueSection — review follow-up', () => {
  it('layout: slots and waiting cards sit in wrapping, shrinkable containers (nothing hidden at any width)', () => {
    renderQ(queue({ heavy_used: 1, holders: [holder()], waiters: [waiter(), waiter({ place: 2 })] }))
    const slots = screen.getByTestId('slot-line-heavy')
    const waiting = screen.getByTestId('queue-line-heavy')
    for (const el of [slots, waiting]) {
      expect(el.className).toContain('flex-wrap')
      expect(el.className).toContain('min-w-0')
      expect(el.className).not.toContain('overflow-x-auto')
    }
    expect(screen.getByTestId('lane-heavy').className).not.toMatch(/\bflex items-center\b/)
    expect(within(slots).getByTestId('gate')).toBeInTheDocument()
    expect(within(slots).getByTestId('q-holder')).toBeInTheDocument()
    expect(within(waiting).getAllByTestId('q-waiting')).toHaveLength(2)
  })

  it('held and paused at once: the lane says 已暫停, the gate line says 暫停放行 (held outranks paused outranks full)', () => {
    renderQ(queue({ heavy_paused: true, admission_held: true, admission_reason: 'mem' }))
    expect(lane('heavy').getByText(t('watch.queue.paused'))).toBeInTheDocument()
    const gate = lane('heavy').getByTestId('gate')
    expect(gate).toHaveAttribute('data-state', 'held')
    expect(gate).toHaveTextContent(t('watch.queue.gate.held'))
  })

  it('longest is never below the daemon figure when one waiter has no since', () => {
    renderQ(queue({ longest_wait_s: 200, waiters: [waiter({ since: NOW - 30_000 }), waiter({ place: 2, since: null })] }))
    expect(screen.getByTestId('queue-count')).toHaveTextContent(t('watch.queue.longest', { t: '3:20' }))
  })

  it('longest keeps ticking from the frame arrival when every since is null', () => {
    renderQ(queue({ longest_wait_s: 100, waiters: [waiter({ since: null })] }))
    expect(screen.getByTestId('queue-count')).toHaveTextContent(t('watch.queue.longest', { t: '1:40' }))
    act(() => { vi.advanceTimersByTime(5000) })
    expect(screen.getByTestId('queue-count')).toHaveTextContent(t('watch.queue.longest', { t: '1:45' }))
  })

  it('the interval runs only while 當下 is shown', () => {
    // 歷史 is only shown for a host that announces `gate.decisions.v1`; the host is not connected, so the body itself reads nothing.
    useNexHostStore.setState({ byHost: { 'cfg-a': { daemonCapabilities: ['gate.decisions.v1'] } } } as never)
    writeQueueTab('d-a', 'history')
    const set = vi.spyOn(globalThis, 'setInterval')
    const clear = vi.spyOn(globalThis, 'clearInterval')
    const { unmount } = render(<QueueSection fleetHostId="d-a" hostConfigId="cfg-a" queue={queue({ waiters: [waiter()] })} offline={false} />)
    expect(screen.getByTestId('queue-history')).toBeInTheDocument()
    expect(set).not.toHaveBeenCalled()
    unmount()
    expect(clear).not.toHaveBeenCalled()
  })

  it('hover pairing survives a re-order: the hovered waiter keeps its card when it moves from place 2 to 1', () => {
    const a = waiter({ place: 1, command: 'cmd-a', since: NOW - 50_000 })
    const b = waiter({ place: 2, command: 'cmd-b', since: NOW - 40_000 })
    const { rerender } = renderQ(queue({ waiters: [a, b] }))
    fireEvent.mouseEnter(screen.getAllByTestId('q-waiting')[1])
    rerender(<QueueSection fleetHostId="d-a" queue={queue({ waiters: [{ ...b, place: 1 }, { ...a, place: 2 }] })} offline={false} />)
    const cards = screen.getAllByTestId('q-waiting')
    expect(cards[0]).toHaveTextContent('cmd-b')
    expect(cards[0]).toHaveAttribute('data-hot')
    expect(cards[1]).not.toHaveAttribute('data-hot')
  })
})
