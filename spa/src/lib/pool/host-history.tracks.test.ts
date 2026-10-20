// MON-6c task 3: the two tracks of the host page's memory — what is read, what joins live, what a late or future answer may not do.
import { describe, it, expect, vi, beforeEach } from 'vitest'
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

import { PoolReadError } from './api'
import { useNexHostStore } from '../../stores/useNexHostStore'
import {
  appendLive, clearHostHistory, invalidateHostHistory, loadLong, loadShort, readHostLoad, readSamples, readsFiveSecond, seriesAnnounced, subscribeHost, RANGE_MS, SKEW_MS,
} from './host-history'

const darwin = parseFacts(darwinJson)!
const NOW = Date.UTC(2026, 9, 18, 5, 10, 0)
const rec = (secAgo: number, idle = 80): PoolFacts => ({ ...darwin, at: NOW - secAgo * 1000, cpu: { ...darwin.cpu, idle_pct: idle } })
const pt = (minAgo: number, idle = 70): SeriesPoint => ({
  at: NOW - minAgo * 60_000, zone: 'normal', cpu_idle_pct: idle, mem_available_mb: 4000, pressure: 0, page_out_rate: null, disk_max_used_pct: null, headroom: { heavy: 1, light: 1 },
})
const refused = (code: string) => new PoolReadError('/x', 501, { code, action: 'none' } as never)
const caps = (list: string[] | undefined) => useNexHostStore.setState({ byHost: { d1: { phase: 'ready', daemonCapabilities: list } } } as never)
const cpus = (hostId: string, range: Parameters<typeof readSamples>[1]) => readSamples(hostId, range, NOW).map((s) => s.cpuPct)
const deferred = <T,>() => { let resolve!: (v: T) => void; const promise = new Promise<T>((r) => { resolve = r }); return { promise, resolve } }

beforeEach(() => { fetchFactsHistory.mockReset(); fetchPoolSeries.mockReset(); clearHostHistory(); caps(['pool.series.v1']) })

describe('ranges', () => {
  it('30 minutes and shorter read the 5 s records, longer ones the series', () => {
    expect((['5m', '15m', '30m', '1h', '6h', '24h'] as const).map(readsFiveSecond)).toEqual([true, true, true, false, false, false])
  })
})

describe('loadShort', () => {
  it('reads 30 minutes at 5 s (a minute further back for skew); 5, 15 and 30 minute ranges are cut from it', async () => {
    fetchFactsHistory.mockResolvedValue({ step: '5s', records: [rec(1500, 10), rec(600, 20), rec(60, 30)] })
    expect(await loadShort('d1', 'air26', 'h1', NOW)).toBe('ok')
    expect(fetchFactsHistory).toHaveBeenCalledWith('d1', 'air26', new Date(NOW - RANGE_MS['30m'] - SKEW_MS).toISOString(), '5s')
    expect(cpus('h1', '30m')).toEqual([90, 80, 70])
    expect(cpus('h1', '15m')).toEqual([80, 70])
    expect(cpus('h1', '5m')).toEqual([70])
  })
  it('a sample that is already there stays; a live one is not covered by the older answer', async () => {
    appendLive('h1', rec(10, 5), NOW)
    fetchFactsHistory.mockResolvedValue({ step: '5s', records: [rec(20, 50), rec(10, 99)] })
    await loadShort('d1', 'air26', 'h1', NOW)
    expect(cpus('h1', '5m')).toEqual([50, 95])
  })
  it('a sample from the future is dropped, one a few seconds ahead is tolerated', async () => {
    fetchFactsHistory.mockResolvedValue({ step: '5s', records: [rec(10, 80), rec(-3, 70), rec(-60, 10)] })
    await loadShort('d1', 'air26', 'h1', NOW)
    expect(cpus('h1', '5m')).toEqual([20, 30])
  })
  it('host_not_local is told apart and nothing is written; any other failure is error', async () => {
    fetchFactsHistory.mockRejectedValueOnce(refused('host_not_local'))
    expect(await loadShort('d1', 'air19', 'h1', NOW)).toBe('host_not_local')
    expect(readHostLoad('h1', 'short')).toBe('host_not_local')
    fetchFactsHistory.mockRejectedValueOnce(new Error('boom'))
    expect(await loadShort('d1', 'air19', 'h2', NOW)).toBe('error')
    expect(readSamples('h1', '30m', NOW)).toEqual([])
  })
  it('an answer that comes back after a later load started writes nothing', async () => {
    const first = deferred<{ step: string; records: PoolFacts[] }>()
    fetchFactsHistory.mockReturnValueOnce(first.promise)
    const early = loadShort('d1', 'air26', 'h1', NOW)
    fetchFactsHistory.mockResolvedValueOnce({ step: '5s', records: [rec(10, 60)] })
    await loadShort('d1', 'air26', 'h1', NOW)
    first.resolve({ step: '5s', records: [rec(20, 1)] })
    await early
    expect(cpus('h1', '5m')).toEqual([40])
  })
  it('an invalidated load writes nothing when it comes back (the page left)', async () => {
    const d = deferred<{ step: string; records: PoolFacts[] }>()
    fetchFactsHistory.mockReturnValueOnce(d.promise)
    const p = loadShort('d1', 'air26', 'h1', NOW)
    invalidateHostHistory('h1', 'short')
    d.resolve({ step: '5s', records: [rec(10)] })
    await p
    expect(readSamples('h1', '5m', NOW)).toEqual([])
    expect(readHostLoad('h1', 'short')).toBe('idle')
  })
  it('keeps 31 minutes back from the newest', async () => {
    // newest is 100 s ago, so a sample 2000 s ago is 1900 s older than it (> 31 minutes) and goes; one at 1500 s stays. Read as of 1000 s ago to see the older ones.
    fetchFactsHistory.mockResolvedValue({ step: '5s', records: [rec(2000, 10), rec(1500, 20), rec(100, 30)] })
    await loadShort('d1', 'air26', 'h1', NOW)
    expect(readSamples('h1', '30m', NOW - 1000_000).map((s) => s.cpuPct)).toEqual([80])
  })
})

describe('appendLive', () => {
  it('a new record joins; the same time twice is one sample', () => {
    appendLive('h1', rec(10, 80), NOW)
    appendLive('h1', rec(10, 10), NOW)
    appendLive('h1', rec(5, 70), NOW)
    expect(cpus('h1', '5m')).toEqual([20, 30])
  })
})

describe('the memory is bounded', () => {
  it('past 32 hosts the oldest ones nobody holds or listens to are let go; one that is listened to stays', () => {
    appendLive('keep', rec(10), NOW)
    const off = subscribeHost('keep', () => undefined)
    for (let i = 0; i < 40; i++) appendLive(`x${i}`, rec(10), NOW)
    expect(readSamples('x0', '5m', NOW)).toEqual([])
    expect(readSamples('x39', '5m', NOW)).toHaveLength(1)
    expect(readSamples('keep', '5m', NOW)).toHaveLength(1)
    off()
  })
})

describe('the memory is bounded when the hosts were all in use', () => {
  it('the cap is met again once they are let go, without a new host arriving', () => {
    const offs: Array<() => void> = []
    for (let i = 0; i < 40; i++) { appendLive(`y${i}`, rec(10), NOW); offs.push(subscribeHost(`y${i}`, () => undefined)) }
    expect(readSamples('y0', '5m', NOW)).toHaveLength(1)
    offs.forEach((off) => off())
    expect(readSamples('y0', '5m', NOW)).toEqual([])
    expect(readSamples('y39', '5m', NOW)).toHaveLength(1)
  })
})

describe('seriesAnnounced and loadLong', () => {
  it('without pool.series.v1 the series is not asked and the track stays empty', async () => {
    caps(['pool.v1'])
    expect(seriesAnnounced('d1')).toBe(false)
    expect(await loadLong({ source: 'd1', alias: 'air26', hostId: 'h1', range: '1h', memTotalMb: 16000, ncpu: 10, now: NOW })).toBe('idle')
    expect(fetchPoolSeries).not.toHaveBeenCalled()
  })
  it('reads the range at 1 minute (a minute further back) and keeps the samples', async () => {
    fetchPoolSeries.mockResolvedValue({ step: '1m', truncated: false, points: [pt(50, 10), pt(30, 20), pt(1, 30)] })
    expect(await loadLong({ source: 'd1', alias: 'air26', hostId: 'h1', range: '1h', memTotalMb: 16000, ncpu: 10, now: NOW })).toBe('ok')
    expect(fetchPoolSeries).toHaveBeenCalledWith('d1', 'air26', new Date(NOW - RANGE_MS['1h'] - SKEW_MS).toISOString(), '1m')
    expect(cpus('h1', '1h')).toEqual([90, 80, 70])
    expect(readSamples('h1', '1h', NOW)[0].memUsedMb).toBe(12000)
  })
  it('a refresh reads only from the last sample and merges', async () => {
    fetchPoolSeries.mockResolvedValueOnce({ step: '1m', truncated: false, points: [pt(5, 10), pt(4, 20)] })
    await loadLong({ source: 'd1', alias: 'air26', hostId: 'h1', range: '1h', memTotalMb: null, ncpu: null, now: NOW })
    fetchPoolSeries.mockResolvedValueOnce({ step: '1m', truncated: false, points: [pt(4, 99), pt(3, 40)] })
    await loadLong({ source: 'd1', alias: 'air26', hostId: 'h1', range: '1h', memTotalMb: null, ncpu: null, now: NOW, refresh: true })
    expect(fetchPoolSeries).toHaveBeenLastCalledWith('d1', 'air26', new Date(NOW - 4 * 60_000).toISOString(), '1m')
    expect(cpus('h1', '1h')).toEqual([90, 80, 60])
  })
  it('switching to a longer range reads it in full and keeps what the shorter one had', async () => {
    fetchPoolSeries.mockResolvedValueOnce({ step: '1m', truncated: false, points: [pt(10, 10)] })
    await loadLong({ source: 'd1', alias: 'air26', hostId: 'h1', range: '1h', memTotalMb: null, ncpu: null, now: NOW })
    fetchPoolSeries.mockResolvedValueOnce({ step: '1m', truncated: false, points: [pt(300, 20), pt(10, 77)] })
    await loadLong({ source: 'd1', alias: 'air26', hostId: 'h1', range: '6h', memTotalMb: null, ncpu: null, now: NOW })
    expect(fetchPoolSeries).toHaveBeenLastCalledWith('d1', 'air26', new Date(NOW - RANGE_MS['6h'] - SKEW_MS).toISOString(), '1m')
    expect(cpus('h1', '6h')).toEqual([80, 90])
  })
  it('a point from the future is dropped; a failed read is error, host_not_local is told apart', async () => {
    fetchPoolSeries.mockResolvedValueOnce({ step: '1m', truncated: false, points: [pt(2, 10), pt(-5, 20)] })
    await loadLong({ source: 'd1', alias: 'air26', hostId: 'h1', range: '1h', memTotalMb: null, ncpu: null, now: NOW })
    expect(cpus('h1', '1h')).toEqual([90])
    fetchPoolSeries.mockRejectedValueOnce(refused('host_not_local'))
    expect(await loadLong({ source: 'd1', alias: 'air26', hostId: 'h2', range: '1h', memTotalMb: null, ncpu: null, now: NOW })).toBe('host_not_local')
    fetchPoolSeries.mockRejectedValueOnce(new Error('x'))
    expect(await loadLong({ source: 'd1', alias: 'air26', hostId: 'h3', range: '1h', memTotalMb: null, ncpu: null, now: NOW })).toBe('error')
  })
  it('an answer that comes back after the page left writes nothing', async () => {
    const d = deferred<{ step: string; truncated: boolean; points: SeriesPoint[] }>()
    fetchPoolSeries.mockReturnValueOnce(d.promise)
    const p = loadLong({ source: 'd1', alias: 'air26', hostId: 'h1', range: '1h', memTotalMb: null, ncpu: null, now: NOW })
    invalidateHostHistory('h1')
    d.resolve({ step: '1m', truncated: false, points: [pt(2)] })
    await p
    expect(readSamples('h1', '1h', NOW)).toEqual([])
  })
})
