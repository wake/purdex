// W5d: the 歷史 body (watcher area spec §6.1): the shared bar scale, absent fields, failed records, the hover card, the class switch,
// the capability, the 30 s re-read, hidden = no read, a re-point drops the old answer, 403 / 500 / network.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, fireEvent, within, act, cleanup } from '@testing-library/react'
import type { GateDecision } from '../../lib/watch/decisions-types'

const fetchGateDecisions = vi.fn()
vi.mock('../../lib/watch/api', () => ({ fetchGateDecisions: (...a: unknown[]) => fetchGateDecisions(...a) }))
import { QueueSection } from './QueueSection'
import { QueueHistory } from './QueueHistory'
import { clearViewMemory, readHistoryClass, writeQueueTab } from '../../lib/watch/view-memory'
import { useHostStore } from '../../stores/useHostStore'
import { useNexHostStore } from '../../stores/useNexHostStore'
import { useI18nStore } from '../../stores/useI18nStore'
import type { FleetQueue } from '../../lib/fleet/types'
const t = (k: string, p?: Record<string, string | number>) => useI18nStore.getState().t(k, p)

const NOW = Date.parse('2026-10-17T12:00:00Z')
const rec = (o: Partial<GateDecision> = {}): GateDecision => ({
  time: '2026-10-17T11:00:00Z', argv: ['go', 'test', './x'], cwd: '/Users/w/purdex', session: 'abcdef1234567890', class: 'heavy', outcome: 'slot',
  waitS: 0, waitReasons: [], applied: {}, exit: 0, durationS: 60, ...o,
})
const ok = (records: GateDecision[], skipped = 0) => ({ kind: 'ok', records, skipped })
const flush = async () => { await act(async () => { await vi.advanceTimersByTimeAsync(0) }) }
const host = (o: Record<string, unknown> = {}) => ({ id: 'cfg-a', name: 'mlab', ip: '1', port: 1, token: 't', order: 0, ...o })
const setHost = (h = host()) => useHostStore.setState({ hosts: { 'cfg-a': h }, hostOrder: ['cfg-a'], runtime: { 'cfg-a': { status: 'connected' } } } as never)
const renderH = () => render(<QueueHistory fleetHostId="d-a" hostConfigId="cfg-a" />)
const widthOf = (el: HTMLElement) => parseFloat(el.style.width)

beforeEach(() => {
  cleanup(); clearViewMemory(); fetchGateDecisions.mockReset(); fetchGateDecisions.mockResolvedValue(ok([]))
  vi.useFakeTimers(); vi.setSystemTime(NOW); setHost()
})
afterEach(() => { vi.useRealTimers(); Object.defineProperty(document, 'visibilityState', { value: 'visible', configurable: true }) })

describe('QueueHistory — the bar', () => {
  it('every row uses one scale: the longest bar is the full scale, the others are in proportion', async () => {
    fetchGateDecisions.mockResolvedValue(ok([
      rec({ time: '2026-10-17T11:30:00Z', waitS: 0, durationS: 600 }), // 10 min: the longest, scale 10
      rec({ time: '2026-10-17T11:00:00Z', waitS: 0, durationS: 150 }), // 2.5 min
    ]))
    renderH(); await flush()
    expect(screen.getByTestId('hist-scale')).toHaveTextContent(t('watch.history.scale', { n: 10 }))
    const bars = screen.getAllByTestId('hist-bar')
    expect(widthOf(bars[0])).toBeCloseTo(100)
    expect(widthOf(bars[1])).toBeCloseTo(25)
  })

  it('hatched = waited, solid = ran; a wait under 3 s has no hatched part', async () => {
    fetchGateDecisions.mockResolvedValue(ok([rec({ time: '2026-10-17T11:30:00Z', waitS: 90, durationS: 180 }), rec({ waitS: 1, durationS: 60 })]))
    renderH(); await flush()
    const [a, b] = screen.getAllByTestId('hist-row')
    expect(within(a).getByTestId('hist-bar-wait')).toBeInTheDocument()
    expect(within(a).getByTestId('hist-bar-run')).toBeInTheDocument()
    expect(within(b).queryByTestId('hist-bar-wait')).toBeNull()
    expect(within(b).getByText(t('watch.history.no_wait'))).toBeInTheDocument()
  })

  it('a command that only waited has a hatched part, no run part, no exit code and 「—」 in every usage column', async () => {
    fetchGateDecisions.mockResolvedValue(ok([rec({ outcome: 'interrupted', waitS: 120, durationS: 0, exit: 130 })]))
    renderH(); await flush()
    const row = screen.getByTestId('hist-row')
    expect(within(row).getByTestId('hist-bar-wait')).toBeInTheDocument()
    expect(within(row).queryByTestId('hist-bar-run')).toBeNull()
    expect(within(row).getByText(t('watch.history.never_ran'))).toBeInTheDocument()
    for (const id of ['hist-cores', 'hist-mem', 'hist-faults', 'hist-exit']) expect(within(row).getByTestId(id)).toHaveTextContent('—')
  })

  it('duration_s holds the wait: a started command whose duration equals its wait draws only the hatched part and no cores', async () => {
    fetchGateDecisions.mockResolvedValue(ok([rec({ waitS: 120, durationS: 120, cpuS: 9 })]))
    renderH(); await flush()
    const row = screen.getByTestId('hist-row')
    expect(within(row).getByTestId('hist-bar-wait')).toBeInTheDocument()
    expect(within(row).queryByTestId('hist-bar-run')).toBeNull()
    expect(within(row).getByTestId('hist-cores')).toHaveTextContent('—')
  })

  it('a failed record (exit != 0) is red in the bar and the exit code; a good one is not', async () => {
    fetchGateDecisions.mockResolvedValue(ok([rec({ time: '2026-10-17T11:30:00Z', exit: 2 }), rec({ exit: 0 })]))
    renderH(); await flush()
    const [bad, good] = screen.getAllByTestId('hist-row')
    expect(within(bad).getByTestId('hist-bar-run')).toHaveAttribute('data-failed', 'true')
    expect(within(bad).getByTestId('hist-bar-run').className).toContain('bg-status-error')
    expect(within(bad).getByTestId('hist-exit').className).toContain('text-status-error-text')
    expect(within(bad).getByTestId('hist-exit')).toHaveTextContent('2')
    expect(within(good).getByTestId('hist-bar-run')).not.toHaveAttribute('data-failed')
    expect(within(good).getByTestId('hist-exit').className).not.toContain('text-status-error-text')
  })
})

describe('QueueHistory — the columns', () => {
  it('cores, peak memory (GB from 1024 MB) and faults; absent fields read 「—」, a zero stays 0', async () => {
    fetchGateDecisions.mockResolvedValue(ok([
      rec({ time: '2026-10-17T11:30:00Z', cpuS: 150, durationS: 60, maxRssMb: 2048, majorFaults: 12345 }),
      rec({ time: '2026-10-17T11:20:00Z', cpuS: 0, maxRssMb: 512, majorFaults: 0 }),
      rec({ time: '2026-10-17T11:10:00Z' }),
    ]))
    renderH(); await flush()
    const [a, b, c] = screen.getAllByTestId('hist-row')
    expect(within(a).getByTestId('hist-cores')).toHaveTextContent(t('watch.history.cores', { n: '2.5' }))
    expect(within(a).getByTestId('hist-mem')).toHaveTextContent(t('watch.history.gb', { n: 2 }))
    expect(within(a).getByTestId('hist-faults').className).toContain('text-status-error-text')
    expect(within(b).getByTestId('hist-cores')).toHaveTextContent(t('watch.history.cores', { n: '0.0' }))
    expect(within(b).getByTestId('hist-mem')).toHaveTextContent(t('watch.history.mb', { n: 512 }))
    expect(within(b).getByTestId('hist-faults')).toHaveTextContent(t('watch.history.times', { n: 0 }))
    for (const id of ['hist-cores', 'hist-mem', 'hist-faults']) expect(within(c).getByTestId(id)).toHaveTextContent('—')
  })

  it('there is no disk read / write column', async () => {
    fetchGateDecisions.mockResolvedValue(ok([rec()]))
    renderH(); await flush()
    expect(screen.queryByText('磁碟讀寫')).toBeNull()
    expect(screen.getAllByRole('columnheader')).toHaveLength(9)
  })

  it('the title counts the rows and names the class', async () => {
    fetchGateDecisions.mockResolvedValue(ok([rec(), rec({ time: '2026-10-17T10:00:00Z' })]))
    renderH(); await flush()
    expect(screen.getByTestId('history-title')).toHaveTextContent(t('watch.history.title', { kind: t('watch.history.kind.heavy'), n: 2 }))
  })
})

describe('QueueHistory — the hover card', () => {
  it('shows the full command, project · session, the three times, why it waited and the applied parameters', async () => {
    fetchGateDecisions.mockResolvedValue(ok([rec({
      argv: ['pnpm', 'build', '--filter', 'spa'], waitS: 30, durationS: 60, waitReasons: ['queue', 'memory-pressure'],
      applied: { 'slots.heavy': { value: '2', source: 'config' }, 'priority.mode': { value: 'nice', source: 'default' } },
    })]))
    renderH(); await flush()
    fireEvent.mouseEnter(screen.getByTestId('hist-row'))
    expect(screen.getByTestId('hist-tip-command')).toHaveTextContent('pnpm build --filter spa')
    expect(screen.getByTestId('hist-tip-where')).toHaveTextContent('purdex · abcdef1234567890')
    const times = screen.getByTestId('hist-tip-times').textContent ?? ''
    expect(times.match(/\d\d:\d\d:\d\d/g)).toHaveLength(3)
    expect(screen.getByTestId('hist-tip-why')).toHaveTextContent(`${t('watch.history.why.queue')}、${t('watch.history.why.memory-pressure')}`)
    const params = screen.getByTestId('hist-tip-params')
    expect(params).toHaveTextContent(`slots.heavy = 2 (${t('watch.history.source.config')})`)
    expect(params).toHaveTextContent(`priority.mode = nice (${t('watch.history.source.default')})`)
    fireEvent.mouseLeave(screen.getByTestId('hist-row'))
    expect(screen.queryByTestId('hist-tip')).toBeNull()
  })

  it('leaves out a line whose field is absent (no reason, no parameters, no session, no end for a command that never ran)', async () => {
    fetchGateDecisions.mockResolvedValue(ok([rec({ session: undefined, cwd: undefined, outcome: 'refused', durationS: 0 })]))
    renderH(); await flush()
    fireEvent.mouseEnter(screen.getByTestId('hist-row'))
    expect(screen.queryByTestId('hist-tip-why')).toBeNull()
    expect(screen.queryByTestId('hist-tip-params')).toBeNull()
    expect(screen.queryByTestId('hist-tip-where')).toBeNull()
    expect((screen.getByTestId('hist-tip-times').textContent ?? '').match(/\d\d:\d\d:\d\d/g)).toHaveLength(2)
  })
})

describe('QueueHistory — reading', () => {
  it('reads once on open for the remembered class and passes a 3-hour window', async () => {
    renderH(); await flush()
    expect(fetchGateDecisions).toHaveBeenCalledTimes(1)
    expect(fetchGateDecisions).toHaveBeenCalledWith('cfg-a', 'heavy', new Date(NOW - 3 * 3600_000).toISOString())
  })

  it('the class switch re-reads for the other class and is remembered', async () => {
    renderH(); await flush()
    fireEvent.click(within(screen.getByTestId('history-class')).getByRole('button', { name: t('watch.history.kind.light') }))
    await flush()
    expect(fetchGateDecisions).toHaveBeenLastCalledWith('cfg-a', 'light', expect.any(String))
    expect(readHistoryClass('d-a')).toBe('light')
  })

  it('re-reads every 30 s while shown, and stops after unmount', async () => {
    const { unmount } = renderH(); await flush()
    expect(fetchGateDecisions).toHaveBeenCalledTimes(1)
    await act(async () => { await vi.advanceTimersByTimeAsync(30_000) })
    expect(fetchGateDecisions).toHaveBeenCalledTimes(2)
    await act(async () => { await vi.advanceTimersByTimeAsync(30_000) })
    expect(fetchGateDecisions).toHaveBeenCalledTimes(3)
    unmount()
    await act(async () => { await vi.advanceTimersByTimeAsync(90_000) })
    expect(fetchGateDecisions).toHaveBeenCalledTimes(3)
  })

  it('reads nothing while the window is hidden, and once it is shown again', async () => {
    Object.defineProperty(document, 'visibilityState', { value: 'hidden', configurable: true })
    renderH(); await flush()
    await act(async () => { await vi.advanceTimersByTimeAsync(90_000) })
    expect(fetchGateDecisions).not.toHaveBeenCalled()
    Object.defineProperty(document, 'visibilityState', { value: 'visible', configurable: true })
    await act(async () => { document.dispatchEvent(new Event('visibilitychange')) })
    await flush()
    expect(fetchGateDecisions).toHaveBeenCalledTimes(1)
  })

  it('an answer that arrives after a re-point is dropped, and the new endpoint is asked', async () => {
    let resolveOld: (v: unknown) => void = () => {}
    fetchGateDecisions.mockImplementationOnce(() => new Promise((r) => { resolveOld = r }))
    renderH(); await flush()
    expect(screen.getByTestId('history-loading')).toBeInTheDocument()
    fetchGateDecisions.mockResolvedValueOnce(ok([rec({ argv: ['new-endpoint'] })]))
    act(() => setHost(host({ ip: '2' })))
    await flush()
    resolveOld(ok([rec({ argv: ['old-endpoint'] })]))
    await flush()
    expect(screen.queryByText('old-endpoint', { exact: false })).toBeNull()
    expect(screen.getByText('new-endpoint', { exact: false })).toBeInTheDocument()
  })

  it('an answer that arrives after the host is gone is dropped', async () => {
    let resolveOld: (v: unknown) => void = () => {}
    fetchGateDecisions.mockImplementationOnce(() => new Promise((r) => { resolveOld = r }))
    renderH(); await flush()
    act(() => useHostStore.setState({ hosts: {}, hostOrder: [], runtime: {} } as never))
    resolveOld(ok([rec({ argv: ['ghost'] })]))
    await flush()
    expect(screen.queryByText('ghost', { exact: false })).toBeNull()
  })

  it('a slow answer of the old class does not land under the new one', async () => {
    let resolveHeavy: (v: unknown) => void = () => {}
    fetchGateDecisions.mockImplementationOnce(() => new Promise((r) => { resolveHeavy = r }))
    renderH(); await flush()
    fetchGateDecisions.mockResolvedValueOnce(ok([rec({ class: 'light', argv: ['light-cmd'] })]))
    fireEvent.click(within(screen.getByTestId('history-class')).getByRole('button', { name: t('watch.history.kind.light') }))
    await flush()
    resolveHeavy(ok([rec({ argv: ['heavy-cmd'] })]))
    await flush()
    expect(screen.queryByText('heavy-cmd', { exact: false })).toBeNull()
    expect(screen.getByText('light-cmd', { exact: false })).toBeInTheDocument()
  })
})

describe('QueueHistory — errors', () => {
  it.each([['forbidden'], ['server'], ['network']])('%s: one line, no crash, no table', async (kind) => {
    fetchGateDecisions.mockResolvedValue({ kind })
    renderH(); await flush()
    expect(screen.getByTestId('history-error')).toHaveTextContent(t(`watch.history.err_${kind}`))
    expect(screen.queryByRole('table')).toBeNull()
  })

  it('skipped records add one small line; none adds nothing', async () => {
    fetchGateDecisions.mockResolvedValue(ok([rec()], 3))
    renderH(); await flush()
    expect(screen.getByTestId('history-skipped')).toHaveTextContent(t('watch.history.skipped', { n: 3 }))
    cleanup()
    fetchGateDecisions.mockResolvedValue(ok([rec()], 0))
    renderH(); await flush()
    expect(screen.queryByTestId('history-skipped')).toBeNull()
  })

  it('no records: one sentence', async () => {
    renderH(); await flush()
    expect(screen.getByTestId('history-empty')).toHaveTextContent(t('watch.history.empty'))
  })
})

const queue: FleetQueue = { heavy_used: 0, heavy_total: 2, light_used: 0, light_total: 3, holders: [], waiters: [], longest_wait_s: 0, admission_held: false, admission_reason: '', heavy_paused: false, light_paused: false }
const nexWith = (...caps: string[]) => useNexHostStore.setState({ byHost: { 'cfg-a': { info: null, capabilities: null, phase: 'unavailable', error: null, fetchedAt: 1, generation: 1, fingerprint: 'f', daemonCapabilities: caps } } } as never)
const renderSection = () => render(<QueueSection fleetHostId="d-a" hostConfigId="cfg-a" queue={queue} offline={false} />)

describe('QueueSection — the capability', () => {
  it('without gate.decisions.v1: 歷史 is disabled with a reason and nothing is read, even when 歷史 was remembered', async () => {
    nexWith('watch.v1'); writeQueueTab('d-a', 'history')
    renderSection(); await flush()
    const btn = screen.getByRole('button', { name: t('watch.queue.tab.history') })
    expect(btn).toBeDisabled()
    expect(btn).toHaveAttribute('title', t('watch.queue.tab.history_unsupported'))
    expect(screen.queryByTestId('queue-history')).toBeNull()
    expect(screen.getByTestId('queue-now')).toBeInTheDocument()
    expect(fetchGateDecisions).not.toHaveBeenCalled()
  })

  it('with gate.decisions.v1: 歷史 can be picked and reads', async () => {
    nexWith('watch.v1', 'gate.decisions.v1')
    renderSection(); await flush()
    const btn = screen.getByRole('button', { name: t('watch.queue.tab.history') })
    expect(btn).toBeEnabled()
    fireEvent.click(btn); await flush()
    expect(screen.getByTestId('queue-history')).toBeInTheDocument()
    expect(fetchGateDecisions).toHaveBeenCalledTimes(1)
  })

  it('a host config without a daemon id (no hostConfigId) cannot show 歷史', async () => {
    nexWith('gate.decisions.v1')
    render(<QueueSection fleetHostId="d-a" queue={queue} offline={false} />); await flush()
    expect(screen.getByRole('button', { name: t('watch.queue.tab.history') })).toBeDisabled()
  })
})
