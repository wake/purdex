// MON-6c task 4: the host page's hook onto the host history — reads once on mount, joins live, tops up the long track each minute, and calls off what is out on leaving.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { parseFacts } from './parse'
import darwinJson from './__fixtures__/facts_darwin.json'
import type { PoolFacts, SeriesPoint } from './types'

const fetchFactsHistory = vi.fn()
const fetchPoolSeries = vi.fn()
vi.mock('./api', async (orig) => ({
  ...(await orig<typeof import('./api')>()),
  fetchFactsHistory: (...a: unknown[]) => fetchFactsHistory(...a),
  fetchPoolSeries: (...a: unknown[]) => fetchPoolSeries(...a),
}))

import { usePoolStore } from './store'
import { useNexHostStore } from '../../stores/useNexHostStore'
import { clearHostHistory, readHostLoad } from './host-history'
import { useHostSeries } from './use-host-series'

const darwin = parseFacts(darwinJson)!
const T0 = Date.UTC(2026, 9, 18, 5, 10, 0)
const rec = (secAgo: number, idle = 80): PoolFacts => ({ ...darwin, at: T0 - secAgo * 1000, cpu: { ...darwin.cpu, idle_pct: idle } })
const pt = (minAgo: number, idle = 70): SeriesPoint => ({
  at: T0 - minAgo * 60_000, zone: 'normal', cpu_idle_pct: idle, mem_available_mb: 4000, pressure: 0, page_out_rate: null, disk_max_used_pct: null, headroom: { heavy: 1, light: 1 },
})
const flush = () => act(async () => { await vi.advanceTimersByTimeAsync(0) })

beforeEach(() => {
  vi.useFakeTimers()
  vi.setSystemTime(T0)
  fetchFactsHistory.mockReset(); fetchPoolSeries.mockReset(); clearHostHistory()
  fetchFactsHistory.mockResolvedValue({ step: '5s', records: [rec(30, 50), rec(20, 60)] })
  fetchPoolSeries.mockResolvedValue({ step: '1m', truncated: false, points: [pt(10, 40), pt(5, 30)] })
  usePoolStore.setState({ sourceHostId: 'd1' } as never)
  useNexHostStore.setState({ byHost: { d1: { phase: 'ready', daemonCapabilities: ['pool.series.v1'] } } } as never)
})
afterEach(() => { vi.useRealTimers() })

describe('useHostSeries on a short range', () => {
  it('reads the 5 s history once on mount and returns the cut samples', async () => {
    const { result } = renderHook(() => useHostSeries('h1', 'air26', '5m', null))
    await flush()
    expect(fetchFactsHistory).toHaveBeenCalledTimes(1)
    expect(fetchPoolSeries).not.toHaveBeenCalled()
    expect(result.current.samples.map((s) => s.cpuPct)).toEqual([50, 40])
    expect(result.current.load).toBe('ok')
  })
  it('a new facts record joins at once; the same one again does not double', async () => {
    const live = rec(5, 10)
    const { result, rerender } = renderHook(({ f }: { f: PoolFacts | null }) => useHostSeries('h1', 'air26', '5m', f), { initialProps: { f: null as PoolFacts | null } })
    await flush()
    rerender({ f: live })
    await flush()
    expect(result.current.samples.map((s) => s.cpuPct)).toEqual([50, 40, 90])
    rerender({ f: { ...live } })
    await flush()
    expect(result.current.samples).toHaveLength(3)
  })
  it('without a source nothing is read', async () => {
    usePoolStore.setState({ sourceHostId: null } as never)
    renderHook(() => useHostSeries('h1', 'air26', '5m', null))
    await flush()
    expect(fetchFactsHistory).not.toHaveBeenCalled()
  })
  it('an answer that comes back after unmounting writes nothing', async () => {
    let resolve!: (v: unknown) => void
    fetchFactsHistory.mockReturnValueOnce(new Promise((r) => { resolve = r }))
    const { unmount } = renderHook(() => useHostSeries('h1', 'air26', '5m', null))
    unmount()
    resolve({ step: '5s', records: [rec(10)] })
    await flush()
    expect(readHostLoad('h1', 'short')).toBe('idle')
  })
})

describe('useHostSeries on a long range', () => {
  it('reads the series on mount and tops it up from the last sample every minute', async () => {
    const { result } = renderHook(() => useHostSeries('h1', 'air26', '1h', null))
    await flush()
    expect(fetchPoolSeries).toHaveBeenCalledTimes(1)
    expect(fetchFactsHistory).not.toHaveBeenCalled()
    expect(result.current.samples.map((s) => s.cpuPct)).toEqual([60, 70])
    fetchPoolSeries.mockResolvedValue({ step: '1m', truncated: false, points: [pt(5, 30), pt(0, 20)] })
    await act(async () => { await vi.advanceTimersByTimeAsync(60_000) })
    expect(fetchPoolSeries).toHaveBeenCalledTimes(2)
    expect(fetchPoolSeries.mock.calls[1][2]).toBe(new Date(T0 - 5 * 60_000).toISOString())
  })
  it('without pool.series.v1 it reads nothing and gives no samples', async () => {
    useNexHostStore.setState({ byHost: { d1: { phase: 'ready', daemonCapabilities: [] } } } as never)
    const { result } = renderHook(() => useHostSeries('h1', 'air26', '6h', null))
    await flush()
    await act(async () => { await vi.advanceTimersByTimeAsync(120_000) })
    expect(fetchPoolSeries).not.toHaveBeenCalled()
    expect(result.current.samples).toEqual([])
  })
  it('leaving stops the top-ups', async () => {
    const { unmount } = renderHook(() => useHostSeries('h1', 'air26', '1h', null))
    await flush()
    unmount()
    await act(async () => { await vi.advanceTimersByTimeAsync(180_000) })
    expect(fetchPoolSeries).toHaveBeenCalledTimes(1)
  })
  it('switching the range reads the other source', async () => {
    const { rerender } = renderHook(({ r }: { r: '5m' | '1h' }) => useHostSeries('h1', 'air26', r, null), { initialProps: { r: '5m' as '5m' | '1h' } })
    await flush()
    rerender({ r: '1h' })
    await flush()
    expect(fetchFactsHistory).toHaveBeenCalledTimes(1)
    expect(fetchPoolSeries).toHaveBeenCalledTimes(1)
  })
})
