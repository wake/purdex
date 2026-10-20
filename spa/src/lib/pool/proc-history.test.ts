// spa/src/lib/pool/proc-history.test.ts — the 行程 tab's hour of processes, one record every 15 s (monitoring tabs spec M45; plan MON-8b): thinned from wwd's 5 s records, topped up from the live facts,
// read only when somebody asks, and a late answer never writes over a newer one.
import { describe, it, expect, vi, beforeEach } from 'vitest'

const fetchFactsHistory = vi.fn()
vi.mock('./api', async (orig) => ({ ...(await orig<typeof import('./api')>()), fetchFactsHistory: (...a: unknown[]) => fetchFactsHistory(...a) }))

import {
  PROC_KEEP_MS, PROC_STEP_MS, appendLiveProcs, clearProcHistory, holdProcs, loadProcHistory, procsFresh, readProcLoad, readProcSnaps,
} from './proc-history'
import { PoolReadError } from './api'
import { parseFacts } from './parse'
import darwinJson from './__fixtures__/facts_darwin.json'
import type { PoolFacts, PoolProc } from './types'

const darwin = parseFacts(darwinJson)!
const NOW = 1_800_000_000_000
const proc = (pid: number): PoolProc => ({ pid, name: `p${pid}`, cpu_pct: 1, mem_mb: 10, mem_kind: 'footprint', owner: null })
const rec = (at: number, pid = 1): PoolFacts => ({ ...darwin, at, procs: [proc(pid)] })
/** 5 s records from `from` to `to` inclusive. */
const fives = (from: number, to: number): PoolFacts[] => { const out: PoolFacts[] = []; for (let t = from; t <= to; t += 5000) out.push(rec(t, t / 1000)); return out }
const ok = (records: PoolFacts[]) => ({ step: '5s', records })

beforeEach(() => { fetchFactsHistory.mockReset(); clearProcHistory() })

describe('loadProcHistory', () => {
  it('keeps one record every 15 s of the 5 s records (0, 15, 30, 45, 60)', async () => {
    fetchFactsHistory.mockResolvedValue(ok(fives(NOW - 60_000, NOW)))
    expect(await loadProcHistory('cfg-a', 'mlab', 'h1', NOW)).toBe('ok')
    expect(readProcSnaps('h1').map((s) => s.at)).toEqual([NOW - 60_000, NOW - 45_000, NOW - 30_000, NOW - 15_000, NOW])
    expect(PROC_STEP_MS).toBe(15_000)
  })

  it('keeps only the time and the processes, and asks 5 s records from an hour (and a minute of skew) back', async () => {
    fetchFactsHistory.mockResolvedValue(ok([rec(NOW)]))
    await loadProcHistory('cfg-a', 'mlab', 'h1', NOW)
    expect(Object.keys(readProcSnaps('h1')[0]).sort()).toEqual(['at', 'procs'])
    const [source, alias, since, step] = fetchFactsHistory.mock.calls[0]
    expect([source, alias, step]).toEqual(['cfg-a', 'mlab', '5s'])
    expect(Date.parse(since as string)).toBe(NOW - 60 * 60_000 - 60_000)
  })

  it('drops what is more than 61 minutes behind the newest, and what is ahead of this clock', async () => {
    fetchFactsHistory.mockResolvedValue(ok([rec(NOW - PROC_KEEP_MS - 30_000), rec(NOW - 20_000), rec(NOW), rec(NOW + 60_000)]))
    await loadProcHistory('cfg-a', 'mlab', 'h1', NOW)
    expect(readProcSnaps('h1').map((s) => s.at)).toEqual([NOW - 20_000, NOW])
  })

  it('an answer that arrives after a newer load started writes nothing', async () => {
    let first: (v: unknown) => void = () => {}
    fetchFactsHistory.mockReturnValueOnce(new Promise((r) => { first = r }))
    const a = loadProcHistory('cfg-a', 'mlab', 'h1', NOW)
    fetchFactsHistory.mockResolvedValueOnce(ok([rec(NOW, 2)]))
    await loadProcHistory('cfg-a', 'mlab', 'h1', NOW)
    first(ok([rec(NOW - 15_000, 1)]))
    await a
    expect(readProcSnaps('h1').map((s) => s.procs[0].pid)).toEqual([2])
  })

  it('an answer that arrives after the page left writes nothing', async () => {
    let answer: (v: unknown) => void = () => {}
    fetchFactsHistory.mockReturnValueOnce(new Promise((r) => { answer = r }))
    const release = holdProcs('h1')
    const p = loadProcHistory('cfg-a', 'mlab', 'h1', NOW)
    release()
    answer(ok([rec(NOW)]))
    await p
    expect(readProcSnaps('h1')).toEqual([])
  })

  it('host_not_local and other failures are told apart, and the records stay empty', async () => {
    fetchFactsHistory.mockRejectedValueOnce(new PoolReadError('/api/pool/facts/history', 409, { code: 'host_not_local', message: '' } as never))
    expect(await loadProcHistory('cfg-a', 'mlab', 'h1', NOW)).toBe('host_not_local')
    fetchFactsHistory.mockRejectedValueOnce(new Error('boom'))
    expect(await loadProcHistory('cfg-a', 'mlab', 'h2', NOW)).toBe('error')
    expect(readProcSnaps('h1')).toEqual([])
    expect(readProcLoad('h2')).toBe('error')
  })
})

describe('appendLiveProcs', () => {
  it('joins the hour only when it is 15 s after the last record', () => {
    for (const dt of [0, 5000, 10_000, 15_000]) appendLiveProcs('h1', rec(NOW + dt), NOW + dt)
    expect(readProcSnaps('h1').map((s) => s.at)).toEqual([NOW, NOW + 15_000])
  })
})

describe('procsFresh', () => {
  it('a read is not repeated within 5 minutes, and is after', async () => {
    fetchFactsHistory.mockResolvedValue(ok([rec(NOW)]))
    expect(procsFresh('h1', NOW)).toBe(false)
    await loadProcHistory('cfg-a', 'mlab', 'h1', NOW)
    expect(procsFresh('h1', NOW + 4 * 60_000)).toBe(true)
    expect(procsFresh('h1', NOW + 6 * 60_000)).toBe(false)
  })
  it('a failed read is not fresh', async () => {
    fetchFactsHistory.mockRejectedValueOnce(new Error('boom'))
    await loadProcHistory('cfg-a', 'mlab', 'h1', NOW)
    expect(procsFresh('h1', NOW)).toBe(false)
  })
})
