// 7a-4b: the 5 minute history of a host tab from the daemon (loaded once when the tab opens), and the zone of each sample for the band.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { parseFacts } from './parse'
import darwinJson from './__fixtures__/facts_darwin.json'
import { fleetHistory } from '../fleet/history'
import { fast } from '../fleet/__fixtures__/builders'
import type { PoolFacts } from './types'

const fetchFactsHistory = vi.fn()
const fetchPoolSeries = vi.fn()
vi.mock('./api', async (orig) => ({
  ...(await orig<typeof import('./api')>()),
  fetchFactsHistory: (...a: unknown[]) => fetchFactsHistory(...a),
  fetchPoolSeries: (...a: unknown[]) => fetchPoolSeries(...a),
}))

import { PoolReadError } from './api'
import { useNexHostStore } from '../../stores/useNexHostStore'
import { loadFactsHistory, loadSeries, seriesAnnounced, invalidateFactsHistory, readZones, recordZone, clearZoneMemory, readHistoryLoad, FACTS_HISTORY_MS, FACTS_HISTORY_SKEW_MS } from './history'

const darwin = parseFacts(darwinJson)!
const NOW = Date.UTC(2026, 9, 18, 5, 10, 0)
const rec = (secAgo: number, over: Partial<PoolFacts> = {}, zone: PoolFacts['derived']['zone'] = 'normal', idle = 80): PoolFacts => ({
  ...darwin, at: NOW - secAgo * 1000, cpu: { ...darwin.cpu, idle_pct: idle }, derived: { ...darwin.derived, zone }, ...over,
})
const refused = (code: string) => new PoolReadError('/x', 501, { code, action: 'none' } as never)

beforeEach(() => { fetchPoolSeries.mockReset(); fetchFactsHistory.mockReset(); fleetHistory.clear(); clearZoneMemory() })

describe('loadFactsHistory', () => {
  it('asks for the last 5 minutes at 5 s, seeds the raw track and keeps each sample\'s zone', async () => {
    fetchFactsHistory.mockResolvedValue({ step: '5s', records: [rec(20, {}, 'normal', 90), rec(15, {}, 'warning', 20), rec(10, {}, 'danger', 5)] })
    const out = await loadFactsHistory('d1', 'air26', 'f-air26', NOW)
    expect(out).toBe('ok')
    expect(fetchFactsHistory).toHaveBeenCalledWith('d1', 'air26', new Date(NOW - FACTS_HISTORY_MS - FACTS_HISTORY_SKEW_MS).toISOString(), '5s')
    expect(fleetHistory.series('f-air26', 'cpu', '5m', NOW).map((p) => p.avg)).toEqual([10, 80, 95])
    expect(readZones('f-air26').map((z) => z.zone)).toEqual(['normal', 'warning', 'danger'])
  })

  it('what the App collected itself is kept, not doubled', async () => {
    fleetHistory.record('f-air26', fast({ at: NOW - 10_000, cpu: 33 }))
    fetchFactsHistory.mockResolvedValue({ step: '5s', records: [rec(20), rec(10, {}, 'normal', 1)] })
    await loadFactsHistory('d1', 'air26', 'f-air26', NOW)
    expect(fleetHistory.series('f-air26', 'cpu', '5m', NOW).map((p) => [p.t, p.avg])).toEqual([[NOW - 20_000, 20], [NOW - 10_000, 33]])
  })

  it('host_not_local is told apart: nothing seeded', async () => {
    fetchFactsHistory.mockRejectedValue(refused('host_not_local'))
    expect(await loadFactsHistory('d1', 'air19', 'f-air19', NOW)).toBe('host_not_local')
    expect(fleetHistory.rawCount('f-air19')).toBe(0)
    expect(readZones('f-air19')).toEqual([])
  })

  it('any other failure is an error, with nothing seeded', async () => {
    fetchFactsHistory.mockRejectedValue(refused('wwd_unavailable'))
    expect(await loadFactsHistory('d1', 'air26', 'f-air26', NOW)).toBe('error')
    fetchFactsHistory.mockRejectedValue(new Error('boom'))
    expect(await loadFactsHistory('d1', 'air26', 'f-air26', NOW)).toBe('error')
    expect(fleetHistory.rawCount('f-air26')).toBe(0)
  })

  it('an empty history (wwd just started) is ok and seeds nothing', async () => {
    fetchFactsHistory.mockResolvedValue({ step: '5s', records: [] })
    expect(await loadFactsHistory('d1', 'air26', 'f-air26', NOW)).toBe('ok')
    expect(readZones('f-air26')).toEqual([])
  })

  it('the outcome is remembered per host, so a tab switch shows it at once', async () => {
    fetchFactsHistory.mockRejectedValue(refused('host_not_local'))
    await loadFactsHistory('d1', 'air19', 'f-air19', NOW)
    expect(readHistoryLoad('f-air19')).toBe('host_not_local')
    expect(readHistoryLoad('f-other')).toBe('idle')
  })
})

describe('overlapping loads of one host', () => {
  const later = <T,>() => { let ok!: (v: T) => void; let no!: (e: unknown) => void; const p = new Promise<T>((a, b) => { ok = a; no = b }); return { p, ok, no } }

  it('the later load owns the memory: an older one that comes back after it changes nothing', async () => {
    const slow = later<unknown>()
    fetchFactsHistory.mockReturnValueOnce(slow.p)
    fetchFactsHistory.mockResolvedValueOnce({ step: '5s', records: [rec(20, {}, 'normal', 90)] })
    const first = loadFactsHistory('d1', 'air26', 'f-air26', NOW)
    const second = loadFactsHistory('d1', 'air26', 'f-air26', NOW)
    expect(await second).toBe('ok')
    slow.no(refused('host_not_local'))
    await first
    expect(readHistoryLoad('f-air26')).toBe('ok')
    expect(readZones('f-air26').map((z) => z.zone)).toEqual(['normal'])
  })

  it('an older load that has data does not write it over the later one\'s', async () => {
    const slow = later<unknown>()
    fetchFactsHistory.mockReturnValueOnce(slow.p)
    fetchFactsHistory.mockResolvedValueOnce({ step: '5s', records: [rec(20, {}, 'normal', 90)] })
    const first = loadFactsHistory('d1', 'old-alias', 'f-air26', NOW)
    await loadFactsHistory('d1', 'air26', 'f-air26', NOW)
    slow.ok({ step: '5s', records: [rec(15, {}, 'danger', 5)] })
    await first
    expect(readZones('f-air26').map((z) => z.zone)).toEqual(['normal'])
    expect(fleetHistory.series('f-air26', 'cpu', '5m', NOW).map((p) => p.avg)).toEqual([10])
  })
})

describe('invalidating a load that nothing replaces (the tab left, the range changed)', () => {
  it('a load that is out when it is invalidated writes nothing when it comes back', async () => {
    let ok!: (v: unknown) => void
    fetchFactsHistory.mockReturnValueOnce(new Promise((a) => { ok = a }))
    const out = loadFactsHistory('d1', 'air26', 'f-air26', NOW)
    invalidateFactsHistory('f-air26')
    ok({ step: '5s', records: [rec(20, {}, 'danger', 5)] })
    await out
    expect(readHistoryLoad('f-air26')).toBe('idle')
    expect(readZones('f-air26')).toEqual([])
    expect(fleetHistory.rawCount('f-air26')).toBe(0)
  })

  it('it does not undo what an earlier load already wrote', async () => {
    fetchFactsHistory.mockResolvedValueOnce({ step: '5s', records: [rec(20, {}, 'normal', 90)] })
    await loadFactsHistory('d1', 'air26', 'f-air26', NOW)
    invalidateFactsHistory('f-air26')
    expect(readHistoryLoad('f-air26')).toBe('ok')
    expect(readZones('f-air26').map((z) => z.zone)).toEqual(['normal'])
  })
})

describe('zone points and a daemon whose clock is ahead', () => {
  it('a sample dated far ahead of now is ignored, so it cannot push the window past the real ones', () => {
    recordZone('h', NOW - 20_000, 'warning', NOW)
    recordZone('h', NOW + 600_000, 'danger', NOW)
    recordZone('h', NOW - 10_000, 'normal', NOW)
    expect(readZones('h')).toEqual([{ t: NOW - 20_000, zone: 'warning' }, { t: NOW - 10_000, zone: 'normal' }])
  })

  it('loading the history drops the ahead-dated records and keeps the rest', async () => {
    fetchFactsHistory.mockResolvedValue({ step: '5s', records: [rec(20, {}, 'normal', 90), rec(-600, {}, 'danger', 5)] })
    await loadFactsHistory('d1', 'air26', 'f-air26', NOW)
    expect(readZones('f-air26').map((z) => z.zone)).toEqual(['normal'])
    expect(fleetHistory.series('f-air26', 'cpu', '5m', NOW).map((p) => p.avg)).toEqual([10])
  })
})

describe('zone points', () => {
  it('are kept in time order, once per time, and only the last 5 minutes', () => {
    recordZone('h', NOW, 'normal')
    recordZone('h', NOW - 10_000, 'warning')
    recordZone('h', NOW, 'danger') // the same time again: the first stays
    recordZone('h', NOW - 6 * 60_000, 'normal') // older than the window
    expect(readZones('h')).toEqual([{ t: NOW - 10_000, zone: 'warning' }, { t: NOW, zone: 'normal' }])
  })
})

describe('loadSeries (1 h, 6 h and 24 h from the daemon\'s 1 minute series)', () => {
  const pt = (secAgo: number, over: Record<string, unknown> = {}) => ({
    at: NOW - secAgo * 1000, zone: 'normal', cpu_idle_pct: 75, mem_available_mb: 4000, pressure: 1, page_out_rate: 12, disk_max_used_pct: 55, headroom: { heavy: 1, light: 1 }, ...over,
  })
  const run = (over: Record<string, unknown> = {}) => loadSeries({ sourceHostId: 'd1', alias: 'air26', fleetHostId: 'f-air26', range: '1h', memTotalMb: 16000, now: NOW, ...over } as never)
  const series = (item: 'cpu' | 'mem' | 'level' | 'page_rate' | 'disk', range: '1h' | '6h' | '24h' = '1h') => fleetHistory.series('f-air26', item, range, NOW).map((p) => p.avg)

  it('asks for the range at 1 m, reaching back a skew margin, and seeds the five charted items', async () => {
    fetchPoolSeries.mockResolvedValue({ step: '1m', truncated: false, points: [pt(120), pt(60, { cpu_idle_pct: 50, mem_available_mb: 8000, pressure: 2, page_out_rate: 300, disk_max_used_pct: 90 })] })
    expect(await run()).toBe('ok')
    expect(fetchPoolSeries).toHaveBeenCalledWith('d1', 'air26', new Date(NOW - 3600_000 - FACTS_HISTORY_SKEW_MS).toISOString(), '1m')
    expect(series('cpu')).toEqual([25, 50])
    expect(series('mem')).toEqual([75, 50]) // used % from the host's total, not an invented one
    expect(series('level')).toEqual([1, 2])
    expect(series('page_rate')).toEqual([12, 300])
    expect(series('disk')).toEqual([55, 90])
  })

  it('the range is the one asked for', async () => {
    fetchPoolSeries.mockResolvedValue({ step: '1m', truncated: false, points: [] })
    await run({ range: '24h' })
    expect(fetchPoolSeries.mock.calls[0][2]).toBe(new Date(NOW - 24 * 3600_000 - FACTS_HISTORY_SKEW_MS).toISOString())
  })

  it('a reading the daemon has no value for is a gap for that item, not a 0', async () => {
    fetchPoolSeries.mockResolvedValue({ step: '1m', truncated: false, points: [pt(60, { page_out_rate: null, disk_max_used_pct: null })] })
    await run()
    expect(series('cpu')).toEqual([25])
    expect(series('page_rate')).toEqual([])
    expect(series('disk')).toEqual([])
  })

  it('without the host\'s total memory the memory chart is not invented', async () => {
    fetchPoolSeries.mockResolvedValue({ step: '1m', truncated: false, points: [pt(60)] })
    await run({ memTotalMb: null })
    expect(series('mem')).toEqual([])
    expect(series('cpu')).toEqual([25])
  })

  it('truncated: what came is seeded', async () => {
    fetchPoolSeries.mockResolvedValue({ step: '1m', truncated: true, points: [pt(60)] })
    expect(await run({ range: '24h' })).toBe('ok')
    expect(series('cpu', '24h')).toEqual([25])
  })

  it('host_not_local and any other failure are told apart, with nothing seeded', async () => {
    fetchPoolSeries.mockRejectedValueOnce(refused('host_not_local'))
    expect(await run()).toBe('host_not_local')
    fetchPoolSeries.mockRejectedValueOnce(new Error('boom'))
    expect(await run()).toBe('error')
    expect(series('cpu')).toEqual([])
  })

  it('does not touch the 5 minute note of the host', async () => {
    fetchPoolSeries.mockRejectedValue(refused('host_not_local'))
    await run()
    expect(readHistoryLoad('f-air26')).toBe('idle')
  })

  it('the later load of the host owns the memory; an invalidated one writes nothing', async () => {
    let ok!: (v: unknown) => void
    fetchPoolSeries.mockReturnValueOnce(new Promise((a) => { ok = a }))
    fetchPoolSeries.mockResolvedValueOnce({ step: '1m', truncated: false, points: [pt(60, { cpu_idle_pct: 90 })] })
    const first = run()
    await run()
    ok({ step: '1m', truncated: false, points: [pt(60, { cpu_idle_pct: 10, at: NOW - 600_000 })] })
    await first
    expect(series('cpu')).toEqual([10])
    let ok2!: (v: unknown) => void
    fleetHistory.clear()
    fetchPoolSeries.mockReturnValueOnce(new Promise((a) => { ok2 = a }))
    const third = run()
    invalidateFactsHistory('f-air26')
    ok2({ step: '1m', truncated: false, points: [pt(60)] })
    await third
    expect(series('cpu')).toEqual([])
  })
})

describe('seriesAnnounced (the capability gate)', () => {
  const caps = (list: string[] | undefined) => useNexHostStore.setState({ byHost: { d1: { phase: 'ready', daemonCapabilities: list } } } as never)

  it('true only when the daemon the pool is read through announces pool.series.v1', () => {
    caps(['pool.v1', 'pool.series.v1'])
    expect(seriesAnnounced('d1')).toBe(true)
    caps(['pool.v1'])
    expect(seriesAnnounced('d1')).toBe(false)
    caps(undefined)
    expect(seriesAnnounced('d1')).toBe(false)
    expect(seriesAnnounced('other')).toBe(false)
    expect(seriesAnnounced(null)).toBe(false)
  })
})
