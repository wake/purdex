// 7a-4b: the 5 minute history of a host tab from the daemon (loaded once when the tab opens), and the zone of each sample for the band.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { parseFacts } from './parse'
import darwinJson from './__fixtures__/facts_darwin.json'
import { fleetHistory } from '../fleet/history'
import type { PoolFacts } from './types'

const fetchFactsHistory = vi.fn()
vi.mock('./api', async (orig) => ({ ...(await orig<typeof import('./api')>()), fetchFactsHistory: (...a: unknown[]) => fetchFactsHistory(...a) }))

import { PoolReadError } from './api'
import { loadFactsHistory, readZones, recordZone, clearZoneMemory, readHistoryLoad, FACTS_HISTORY_MS } from './history'

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
    expect(fetchFactsHistory).toHaveBeenCalledWith('d1', 'air26', new Date(NOW - FACTS_HISTORY_MS).toISOString(), '5s')
    expect(fleetHistory.series('f-air26', 'cpu', '5m', NOW).map((p) => p.avg)).toEqual([10, 80, 95])
    expect(readZones('f-air26').map((z) => z.zone)).toEqual(['normal', 'warning', 'danger'])
  })

  it('what the App collected itself is kept, not doubled', async () => {
    fleetHistory.record('f-air26', { at: NOW - 10_000, cpu: 33, mem: 1, level: 1, page_rate: 0, disk: 1, load1: 0, ncpu: 8, zone: 'normal', load: 0 })
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

describe('zone points', () => {
  it('are kept in time order, once per time, and only the last 5 minutes', () => {
    recordZone('h', NOW, 'normal')
    recordZone('h', NOW - 10_000, 'warning')
    recordZone('h', NOW, 'danger') // the same time again: the first stays
    recordZone('h', NOW - 6 * 60_000, 'normal') // older than the window
    expect(readZones('h')).toEqual([{ t: NOW - 10_000, zone: 'warning' }, { t: NOW, zone: 'normal' }])
  })
})
