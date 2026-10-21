// spa/src/lib/pool/host-observations.records.test.ts — the 行程 tab's observation records, per host (monitoring tabs spec M45; plan MON-8d): the newest 24 hours, older pages on request with the cursor sent back as it came,
// a record once by its id, a re-read that keeps the older pages, a late answer that writes nothing.
import { describe, it, expect, vi, beforeEach } from 'vitest'

const fetchObservations = vi.fn()
vi.mock('./observations-api', async (orig) => ({ ...(await orig<typeof import('./observations-api')>()), fetchObservations: (...a: unknown[]) => fetchObservations(...a) }))

import { clearHostObservations, holdObservations, loadOlder, loadRecords, observationKey, readOlderLoad, readRecords, readRecordsLoad } from './host-observations'
import { PoolReadError } from './api'
import type { ObservationsPage, PoolObservation } from './observations-api'

const NOW = 1_800_000_000_000
const KEY = observationKey('cfg-a', 'mlab')
const rec = (id: string, startedAt: number): PoolObservation => ({ id, host: 'mlab', session: 'ab12', via: 'session', command: id, signature: id, startedAt, endedAt: null, cpuSeconds: 1, cpuPeak: 100, memPeakMb: 1, procsPeak: 1 })
const page = (list: PoolObservation[], nextUntil: string | null = null, over: Partial<ObservationsPage> = {}): ObservationsPage => ({ host: 'mlab', known: true, observations: list, truncated: false, nextUntil, ...over })
const forbidden = () => new PoolReadError('/p', 403, { code: 'forbidden', message: '' } as never)

beforeEach(() => { fetchObservations.mockReset(); clearHostObservations() })

describe('loadRecords', () => {
  it('asks the last 24 hours, and shows the newest first', async () => {
    fetchObservations.mockResolvedValue(page([rec('a', NOW - 3000), rec('c', NOW - 1000), rec('b', NOW - 2000)]))
    expect(await loadRecords('cfg-a', 'mlab', 'h1', NOW)).toBe('ok')
    const [source, alias, query] = fetchObservations.mock.calls[0]
    expect([source, alias]).toEqual(['cfg-a', 'mlab'])
    expect(Date.parse(query.since)).toBe(NOW - 24 * 3600_000)
    expect(query.until).toBeUndefined()
    expect(readRecords('h1', KEY)!.list.map((o) => o.id)).toEqual(['c', 'b', 'a'])
  })

  it('a 403 is told apart from a failure, and drops what was read', async () => {
    fetchObservations.mockResolvedValueOnce(page([rec('a', NOW)]))
    await loadRecords('cfg-a', 'mlab', 'h1', NOW)
    fetchObservations.mockRejectedValueOnce(forbidden())
    expect(await loadRecords('cfg-a', 'mlab', 'h1', NOW + 60_000)).toBe('forbidden')
    expect(readRecords('h1', KEY)).toBeNull()
    fetchObservations.mockRejectedValueOnce(new Error('boom'))
    expect(await loadRecords('cfg-a', 'mlab', 'h1', NOW + 120_000)).toBe('error')
    expect(readRecordsLoad('h1')).toBe('error')
  })

  it('a failed re-read keeps the last good records', async () => {
    fetchObservations.mockResolvedValueOnce(page([rec('a', NOW)]))
    await loadRecords('cfg-a', 'mlab', 'h1', NOW)
    fetchObservations.mockRejectedValueOnce(new Error('boom'))
    await loadRecords('cfg-a', 'mlab', 'h1', NOW + 60_000)
    expect(readRecords('h1', KEY)!.list.map((o) => o.id)).toEqual(['a'])
  })

  it('the records belong to the source and alias they were read for', async () => {
    fetchObservations.mockResolvedValueOnce(page([rec('a', NOW)]))
    await loadRecords('cfg-a', 'mlab', 'h1', NOW)
    expect(readRecords('h1', observationKey('cfg-a', 'renamed'))).toBeNull()
    expect(readRecords('h1', observationKey('cfg-b', 'mlab'))).toBeNull()
  })

  it('the same ask while one is out shares it: one request', async () => {
    let answer: (v: unknown) => void = () => {}
    fetchObservations.mockReturnValueOnce(new Promise((r) => { answer = r }))
    const a = loadRecords('cfg-a', 'mlab', 'h1', NOW)
    const b = loadRecords('cfg-a', 'mlab', 'h1', NOW)
    expect(fetchObservations).toHaveBeenCalledTimes(1)
    answer(page([rec('a', NOW)]))
    expect([await a, await b]).toEqual(['ok', 'ok'])
  })

  it('an answer that comes back after the last page left writes nothing', async () => {
    let answer: (v: unknown) => void = () => {}
    fetchObservations.mockReturnValueOnce(new Promise((r) => { answer = r }))
    const release = holdObservations('h1')
    const p = loadRecords('cfg-a', 'mlab', 'h1', NOW)
    release()
    answer(page([rec('late', NOW)]))
    await p
    expect(readRecords('h1', KEY)).toBeNull()
  })
})

describe('loadOlder', () => {
  const CURSOR = '2026-10-20T03:04:05.123456789+08:00'

  it('sends the cursor back exactly as wwd wrote it, and the same since', async () => {
    fetchObservations.mockResolvedValueOnce(page([rec('a', NOW - 1000)], CURSOR))
    await loadRecords('cfg-a', 'mlab', 'h1', NOW)
    expect(readRecords('h1', KEY)!.nextUntil).toBe(CURSOR)
    fetchObservations.mockResolvedValueOnce(page([rec('z', NOW - 9000)], null))
    expect(await loadOlder('cfg-a', 'mlab', 'h1')).toBe('idle')
    const [, , query] = fetchObservations.mock.calls[1]
    expect(query.until).toBe(CURSOR)
    expect(query.since).toBe(fetchObservations.mock.calls[0][2].since)
    const got = readRecords('h1', KEY)!
    expect(got.list.map((o) => o.id)).toEqual(['a', 'z'])
    expect(got.nextUntil).toBeNull() // the last page: no more to ask
  })

  it('a record on two pages (the cursor is inclusive) is one', async () => {
    fetchObservations.mockResolvedValueOnce(page([rec('a', NOW - 1000), rec('b', NOW - 2000)], CURSOR))
    await loadRecords('cfg-a', 'mlab', 'h1', NOW)
    fetchObservations.mockResolvedValueOnce(page([rec('b', NOW - 2000), rec('c', NOW - 3000)], null))
    await loadOlder('cfg-a', 'mlab', 'h1')
    expect(readRecords('h1', KEY)!.list.map((o) => o.id)).toEqual(['a', 'b', 'c'])
  })

  it('the next older page is asked with the older page\'s cursor', async () => {
    fetchObservations.mockResolvedValueOnce(page([rec('a', NOW - 1000)], 'cursor-1'))
    await loadRecords('cfg-a', 'mlab', 'h1', NOW)
    fetchObservations.mockResolvedValueOnce(page([rec('b', NOW - 2000)], 'cursor-2'))
    await loadOlder('cfg-a', 'mlab', 'h1')
    fetchObservations.mockResolvedValueOnce(page([rec('c', NOW - 3000)], null))
    await loadOlder('cfg-a', 'mlab', 'h1')
    expect(fetchObservations.mock.calls[2][2].until).toBe('cursor-2')
  })

  it('a re-read of the newest page keeps the older pages the reader loaded', async () => {
    fetchObservations.mockResolvedValueOnce(page([rec('a', NOW - 1000)], CURSOR))
    await loadRecords('cfg-a', 'mlab', 'h1', NOW)
    fetchObservations.mockResolvedValueOnce(page([rec('z', NOW - 9000)], 'cursor-2'))
    await loadOlder('cfg-a', 'mlab', 'h1')
    fetchObservations.mockResolvedValueOnce(page([rec('n', NOW + 500), rec('a', NOW - 1000)], CURSOR))
    await loadRecords('cfg-a', 'mlab', 'h1', NOW + 60_000)
    const got = readRecords('h1', KEY)!
    expect(got.list.map((o) => o.id)).toEqual(['n', 'a', 'z'])
    expect(got.nextUntil).toBe('cursor-2') // the cursor of the oldest page loaded, not the newest page's
    expect(fetchObservations).toHaveBeenCalledTimes(3) // the re-read did not ask the older page again
  })

  it('a re-read lets go of the older records the sliding 24 hours have left (codex R1)', async () => {
    const DAY = 24 * 3600_000
    fetchObservations.mockResolvedValueOnce(page([rec('a', NOW - 1000)], CURSOR))
    await loadRecords('cfg-a', 'mlab', 'h1', NOW)
    fetchObservations.mockResolvedValueOnce(page([rec('edge', NOW - DAY + 30_000), rec('deep', NOW - DAY + 90_000)], 'cursor-2'))
    await loadOlder('cfg-a', 'mlab', 'h1')
    fetchObservations.mockResolvedValueOnce(page([rec('a', NOW - 1000)], CURSOR))
    await loadRecords('cfg-a', 'mlab', 'h1', NOW + 60_000) // the window now starts 60 s later: 'edge' has left it
    expect(readRecords('h1', KEY)!.list.map((o) => o.id)).toEqual(['a', 'deep'])
  })

  it('does nothing without a cursor, and asks once while one is out', async () => {
    fetchObservations.mockResolvedValueOnce(page([rec('a', NOW)], null))
    await loadRecords('cfg-a', 'mlab', 'h1', NOW)
    await loadOlder('cfg-a', 'mlab', 'h1')
    expect(fetchObservations).toHaveBeenCalledTimes(1)

    fetchObservations.mockResolvedValueOnce(page([rec('a', NOW)], CURSOR))
    await loadRecords('cfg-a', 'mlab', 'h1', NOW + 1000)
    let answer: (v: unknown) => void = () => {}
    fetchObservations.mockReturnValueOnce(new Promise((r) => { answer = r }))
    const x = loadOlder('cfg-a', 'mlab', 'h1')
    const y = loadOlder('cfg-a', 'mlab', 'h1')
    expect(readOlderLoad('h1')).toBe('loading')
    expect(fetchObservations).toHaveBeenCalledTimes(3)
    answer(page([rec('z', NOW - 9000)], null))
    await Promise.all([x, y])
    expect(readOlderLoad('h1')).toBe('idle')
  })

  it('a failure keeps what is shown and says so; the cursor stays so it can be tried again', async () => {
    fetchObservations.mockResolvedValueOnce(page([rec('a', NOW)], CURSOR))
    await loadRecords('cfg-a', 'mlab', 'h1', NOW)
    fetchObservations.mockRejectedValueOnce(new Error('boom'))
    expect(await loadOlder('cfg-a', 'mlab', 'h1')).toBe('error')
    expect(readRecords('h1', KEY)!.list.map((o) => o.id)).toEqual(['a'])
    expect(readRecords('h1', KEY)!.nextUntil).toBe(CURSOR)
  })

  it('a 403 drops the records', async () => {
    fetchObservations.mockResolvedValueOnce(page([rec('a', NOW)], CURSOR))
    await loadRecords('cfg-a', 'mlab', 'h1', NOW)
    fetchObservations.mockRejectedValueOnce(forbidden())
    await loadOlder('cfg-a', 'mlab', 'h1')
    expect(readRecords('h1', KEY)).toBeNull()
    expect(readRecordsLoad('h1')).toBe('forbidden')
  })

  it('an older page that comes back after the last page left writes nothing', async () => {
    fetchObservations.mockResolvedValueOnce(page([rec('a', NOW)], CURSOR))
    const release = holdObservations('h1')
    await loadRecords('cfg-a', 'mlab', 'h1', NOW)
    let answer: (v: unknown) => void = () => {}
    fetchObservations.mockReturnValueOnce(new Promise((r) => { answer = r }))
    const p = loadOlder('cfg-a', 'mlab', 'h1')
    release()
    answer(page([rec('late', NOW - 9000)], null))
    await p
    expect(readRecords('h1', KEY)!.list.map((o) => o.id)).toEqual(['a'])
  })
})
