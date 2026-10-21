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
  const byStep = (five: PoolFacts[], one: PoolFacts[]) => fetchFactsHistory.mockImplementation(async (_s: string, _a: string, _since: string, step: string) => (
    step === '5s' ? { step: '5s', records: five } : { step: '1m', records: one }
  ))
  it('reads the 5 s history once on mount and returns the cut samples', async () => {
    byStep([rec(1795, 70), rec(30, 50), rec(20, 60)], [])
    const { result } = renderHook(() => useHostSeries('h1', 'air26', '5m', null))
    await flush()
    expect(fetchFactsHistory).toHaveBeenCalledTimes(1)
    expect(fetchFactsHistory.mock.calls[0][3]).toBe('5s')
    expect(fetchPoolSeries).not.toHaveBeenCalled()
    expect(result.current.samples.map((s) => s.cpuPct)).toEqual([50, 40])
    expect(result.current.load).toBe('ok')
  })
  it('a 5 s history that only has the latest also reads the 1 minute records for the front', async () => {
    byStep([rec(30, 50), rec(20, 60)], [rec(1500, 10), rec(600, 20)])
    const { result } = renderHook(() => useHostSeries('h1', 'air26', '30m', null))
    await flush()
    expect(fetchFactsHistory.mock.calls.map((c) => c[3])).toEqual(['5s', '1m'])
    expect(result.current.samples.map((s) => s.cpuPct)).toEqual([90, 80, 50, 40])
  })
  describe('a gap in the live records', () => {
    const covered = () => byStep([rec(1795, 70), rec(30, 50), rec(20, 60)], [])
    const fiveCalls = () => fetchFactsHistory.mock.calls.filter((c) => c[3] === '5s').length
    it('a live record two minutes after the newest sample reads the 5 s history again', async () => {
      covered()
      const { rerender } = renderHook(({ f }: { f: PoolFacts | null }) => useHostSeries('h1', 'air26', '5m', f), { initialProps: { f: null as PoolFacts | null } })
      await flush()
      expect(fiveCalls()).toBe(1)
      vi.setSystemTime(T0 + 120_000)
      rerender({ f: rec(-120, 10) })
      await flush()
      expect(fiveCalls()).toBe(2)
    })
    it('a live record 5 s after the newest sample does not', async () => {
      covered()
      const { rerender } = renderHook(({ f }: { f: PoolFacts | null }) => useHostSeries('h1', 'air26', '5m', f), { initialProps: { f: null as PoolFacts | null } })
      await flush()
      rerender({ f: rec(15, 10) })
      await flush()
      expect(fiveCalls()).toBe(1)
    })
    it('an empty track is not read again because of a live record', async () => {
      byStep([], [])
      const { rerender } = renderHook(({ f }: { f: PoolFacts | null }) => useHostSeries('h1', 'air26', '5m', f), { initialProps: { f: null as PoolFacts | null } })
      await flush()
      expect(fiveCalls()).toBe(1)
      vi.setSystemTime(T0 + 120_000)
      rerender({ f: rec(-120, 10) })
      await flush()
      expect(fiveCalls()).toBe(1)
    })
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
  it('a read that takes longer than a minute is not overtaken by the next top-up (the answer still lands)', async () => {
    let resolve!: (v: unknown) => void
    fetchPoolSeries.mockReturnValueOnce(new Promise((r) => { resolve = r }))
    const { result } = renderHook(() => useHostSeries('h1', 'air26', '1h', null))
    await flush()
    await act(async () => { await vi.advanceTimersByTimeAsync(130_000) })
    expect(fetchPoolSeries).toHaveBeenCalledTimes(1)
    resolve({ step: '1m', truncated: false, points: [pt(10, 40)] })
    await flush()
    expect(result.current.samples.length).toBeGreaterThan(0)
  })
  it('capabilities that arrive after the page opened start the long read', async () => {
    useNexHostStore.setState({ byHost: { d1: { phase: 'ready', daemonCapabilities: undefined } } } as never)
    const { result } = renderHook(() => useHostSeries('h1', 'air26', '1h', null))
    await flush()
    expect(fetchPoolSeries).not.toHaveBeenCalled()
    act(() => useNexHostStore.setState({ byHost: { d1: { phase: 'ready', daemonCapabilities: ['pool.series.v1'] } } } as never))
    await flush()
    expect(fetchPoolSeries).toHaveBeenCalledTimes(1)
    expect(result.current.samples.length).toBeGreaterThan(0)
  })
  it('two hooks on the same host: one leaving does not call off the other, and both draw what lands', async () => {
    let resolveA!: (v: unknown) => void
    let resolveB!: (v: unknown) => void
    fetchFactsHistory.mockReturnValueOnce(new Promise((r) => { resolveA = r }))
    fetchFactsHistory.mockReturnValueOnce(new Promise((r) => { resolveB = r }))
    const a = renderHook(() => useHostSeries('h1', 'air26', '5m', null))
    const b = renderHook(() => useHostSeries('h1', 'air26', '5m', null))
    await flush()
    b.unmount()
    resolveA({ step: '5s', records: [rec(1795, 10), rec(20, 10)] }) // superseded by b's read
    resolveB({ step: '5s', records: [rec(1795, 10), rec(10, 60)] }) // the latest read lands though the page that made it left: a still holds the track
    await flush()
    expect(a.result.current.samples.map((s) => s.cpuPct)).toEqual([40])
  })
  it('two hooks on the same host both redraw when one of them reads', async () => {
    const a = renderHook(() => useHostSeries('h1', 'air26', '5m', null))
    await flush()
    const b = renderHook(() => useHostSeries('h1', 'air26', '5m', null))
    await flush()
    const before = a.result.current.rev
    fetchFactsHistory.mockResolvedValue({ step: '5s', records: [rec(5, 10)] })
    b.rerender()
    // b's own mount read landed before a was asked; a new read by a third party (a live record through b) must still reach a
    const live = renderHook(() => useHostSeries('h1', 'air26', '5m', rec(2, 20)))
    await flush()
    expect(a.result.current.rev).toBeGreaterThan(before)
    expect(a.result.current.samples.map((s) => s.cpuPct)).toContain(80)
    live.unmount()
  })
  it('a capability withdrawn after the long track was read leaves it empty again', async () => {
    const { result } = renderHook(() => useHostSeries('h1', 'air26', '1h', null))
    await flush()
    expect(result.current.samples.length).toBeGreaterThan(0)
    act(() => useNexHostStore.setState({ byHost: { d1: { phase: 'ready', daemonCapabilities: [] } } } as never))
    await flush()
    expect(result.current.samples).toEqual([])
    expect(result.current.load).toBe('idle')
  })
  it('the host\'s constants arriving after the series completed fill the memory used figure of the whole range', async () => {
    const { result, rerender } = renderHook(({ f }: { f: PoolFacts | null }) => useHostSeries('h1', 'air26', '1h', f), { initialProps: { f: null as PoolFacts | null } })
    await flush()
    expect(result.current.samples[0].memUsedMb).toBeNull()
    rerender({ f: rec(1, 50) })
    await flush()
    expect(result.current.samples[0].memUsedMb).not.toBeNull()
  })
  it('two long hooks on one host share the one read in flight, and both draw its answer', async () => {
    const a = renderHook(() => useHostSeries('h1', 'air26', '1h', null))
    const b = renderHook(() => useHostSeries('h1', 'air26', '1h', null))
    await flush()
    expect(fetchPoolSeries).toHaveBeenCalledTimes(1)
    expect(a.result.current.samples.length).toBeGreaterThan(0)
    expect(b.result.current.samples.length).toBeGreaterThan(0)
  })
  it('a read called off by leaving does not block the same ask when the page comes back', async () => {
    fetchPoolSeries.mockReturnValueOnce(new Promise(() => undefined))
    const first = renderHook(() => useHostSeries('h1', 'air26', '1h', null))
    await flush()
    first.unmount()
    const { result } = renderHook(() => useHostSeries('h1', 'air26', '1h', null))
    await flush()
    expect(fetchPoolSeries).toHaveBeenCalledTimes(2)
    expect(result.current.samples.length).toBeGreaterThan(0)
  })
  it('leaving stops the top-ups', async () => {
    const { unmount } = renderHook(() => useHostSeries('h1', 'air26', '1h', null))
    await flush()
    unmount()
    await act(async () => { await vi.advanceTimersByTimeAsync(180_000) })
    expect(fetchPoolSeries).toHaveBeenCalledTimes(1)
  })
  it('switching the range reads the other source', async () => {
    fetchFactsHistory.mockResolvedValue({ step: '5s', records: [rec(1795, 70), rec(30, 50)] })
    const { rerender } = renderHook(({ r }: { r: '5m' | '1h' }) => useHostSeries('h1', 'air26', r, null), { initialProps: { r: '5m' as '5m' | '1h' } })
    await flush()
    rerender({ r: '1h' })
    await flush()
    expect(fetchFactsHistory).toHaveBeenCalledTimes(1)
    expect(fetchPoolSeries).toHaveBeenCalledTimes(1)
  })
})
