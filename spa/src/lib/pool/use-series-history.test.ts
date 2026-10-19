// 7a-5: the host tab's hook onto the series read — 1 h / 6 h / 24 h, once per open, behind the `pool.series.v1` gate.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, waitFor, act } from '@testing-library/react'
import { fleetHistory } from '../fleet/history'

const fetchPoolSeries = vi.fn()
vi.mock('./api', async (orig) => ({ ...(await orig<typeof import('./api')>()), fetchPoolSeries: (...a: unknown[]) => fetchPoolSeries(...a) }))

import { usePoolStore } from './store'
import { useNexHostStore } from '../../stores/useNexHostStore'
import { useSeriesHistory } from './use-series-history'

const NOW = Date.now()
const point = (agoMin: number) => ({ at: NOW - agoMin * 60_000, cpu_idle_pct: 50, mem_available_mb: 4000, pressure: 0, page_out_rate: null, disk_max_used_pct: null })

function announce(on: boolean) {
  useNexHostStore.setState({ byHost: { d1: { daemonCapabilities: on ? ['pool.series.v1'] : [] } } } as never)
}

beforeEach(() => {
  fetchPoolSeries.mockReset()
  fleetHistory.clear()
  usePoolStore.setState({ sourceHostId: 'd1' } as never)
  announce(true)
})

describe('useSeriesHistory', () => {
  it('reads a long range once and counts the finished load, so the charts redraw', async () => {
    fetchPoolSeries.mockResolvedValue({ points: [point(30), point(20)] })
    const { result } = renderHook(() => useSeriesHistory('f1', 'air26', '1h', true, 8000))
    expect(result.current).toBe(0)
    await waitFor(() => expect(result.current).toBe(1))
    expect(fetchPoolSeries).toHaveBeenCalledTimes(1)
    expect(fleetHistory.series('f1', 'cpu', '1h', Date.now()).length).toBeGreaterThan(0)
  })

  it('a total of 0 on the first live sample, then a real one: the series is read again, so the memory chart is not left empty', async () => {
    fetchPoolSeries.mockResolvedValue({ points: [point(30), point(20)] })
    const { rerender } = renderHook(({ total }) => useSeriesHistory('f1', 'air26', '1h', true, total), { initialProps: { total: 0 } })
    await waitFor(() => expect(fetchPoolSeries).toHaveBeenCalledTimes(1))
    expect(fleetHistory.series('f1', 'mem', '1h', Date.now())).toHaveLength(0)
    rerender({ total: 8000 })
    await waitFor(() => expect(fetchPoolSeries).toHaveBeenCalledTimes(2))
    await waitFor(() => expect(fleetHistory.series('f1', 'mem', '1h', Date.now()).length).toBeGreaterThan(0))
  })

  it('the 5 minute range does not read, and no load is counted', async () => {
    fetchPoolSeries.mockResolvedValue({ points: [point(1)] })
    const { result } = renderHook(() => useSeriesHistory('f1', 'air26', '5m', true, 8000))
    await act(async () => { await new Promise((r) => setTimeout(r, 0)) })
    expect(fetchPoolSeries).not.toHaveBeenCalled()
    expect(result.current).toBe(0)
  })

  it('a daemon that did not announce the series does not get asked', () => {
    announce(false)
    renderHook(() => useSeriesHistory('f1', 'air26', '6h', true, 8000))
    expect(fetchPoolSeries).not.toHaveBeenCalled()
  })

  it('a host that is not live does not read', () => {
    renderHook(() => useSeriesHistory('f1', 'air26', '24h', false, 8000))
    expect(fetchPoolSeries).not.toHaveBeenCalled()
  })

  it('leaving the tab calls the load off: a late answer writes nothing', async () => {
    let done: (v: unknown) => void = () => {}
    fetchPoolSeries.mockReturnValue(new Promise((r) => { done = r }))
    const { unmount, result } = renderHook(() => useSeriesHistory('f1', 'air26', '1h', true, 8000))
    unmount()
    await act(async () => { done({ points: [point(30)] }); await new Promise((r) => setTimeout(r, 0)) })
    expect(result.current).toBe(0)
    expect(fleetHistory.series('f1', 'cpu', '1h', Date.now()).length).toBe(0)
  })
})
