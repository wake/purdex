// 7a-4b: the 5 minute history of a host tab from the daemon (loaded once when the tab opens), and the zone of each sample for the band.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { parseFacts } from './parse'
import darwinJson from './__fixtures__/facts_darwin.json'
import { fleetHistory } from '../fleet/history'
import { fast } from '../fleet/__fixtures__/builders'
import type { PoolFacts } from './types'

const fetchFactsHistory = vi.fn()
vi.mock('./api', async (orig) => ({ ...(await orig<typeof import('./api')>()), fetchFactsHistory: (...a: unknown[]) => fetchFactsHistory(...a) }))

import { PoolReadError } from './api'
import { loadFactsHistory, invalidateFactsHistory, readZones, recordZone, clearZoneMemory, readHistoryLoad, FACTS_HISTORY_MS, FACTS_HISTORY_SKEW_MS } from './history'

const darwin = parseFacts(darwinJson)!
const NOW = Date.UTC(2026, 9, 18, 5, 10, 0)
const rec = (secAgo: number, over: Partial<PoolFacts> = {}, zone: PoolFacts['derived']['zone'] = 'normal', idle = 80): PoolFacts => ({
  ...darwin, at: NOW - secAgo * 1000, cpu: { ...darwin.cpu, idle_pct: idle }, derived: { ...darwin.derived, zone }, ...over,
})
const refused = (code: string) => new PoolReadError('/x', 501, { code, action: 'none' } as never)

beforeEach(() => { fetchFactsHistory.mockReset(); fleetHistory.clear(); clearZoneMemory() })

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
