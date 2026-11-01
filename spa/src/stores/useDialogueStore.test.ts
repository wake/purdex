// spa/src/stores/useDialogueStore.test.ts — the dialogue data layer's store (plan T14a): recaps and turn summaries cached per
// (host, session), the "generating" mark, the host event, support gating, forgetHost.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import type { Recap, RecapsResponse, TurnSummary } from '../lib/dialogue/types'

const fetchRecaps = vi.fn<(h: string, s: string, limit?: number) => Promise<RecapsResponse>>()
const fetchTurnSummaries = vi.fn<(h: string, s: string, r?: { from?: number; to?: number; limit?: number }) => Promise<TurnSummary[]>>()
const requestRecap = vi.fn()
vi.mock('../lib/dialogue/api', () => ({
  fetchRecaps: (...a: [string, string, number?]) => fetchRecaps(...a),
  fetchTurnSummaries: (...a: [string, string, { from?: number; to?: number; limit?: number }?]) => fetchTurnSummaries(...a),
  requestRecap: (...a: unknown[]) => requestRecap(...a),
}))

import { selectDialogueSupport, selectDialogueEntry, summariesInRange, useDialogueStore } from './useDialogueStore'

const recap = (id: number, over: Partial<Recap> = {}): Recap =>
  ({ id, at: id * 100, from_no: 1, to_no: id, trigger: 'manual', headline: `h${id}`, done: [], now: '', decided: [], waiting: [], next: '', ...over })
const resp = (recaps: Recap[], over: Partial<RecapsResponse> = {}): RecapsResponse =>
  ({ recaps, can_generate: true, newest_turn_no: 9, last_recap_to_no: recaps[0]?.to_no ?? 0, ...over })
const turn = (n: number): TurnSummary => ({ turn_no: n, at: n, title: `t${n}`, summary: `s${n}`, push: '' })
const flush = () => new Promise((r) => setTimeout(r, 0))
const st = () => useDialogueStore.getState()
const entry = (h = 'h1', s = 'S1') => selectDialogueEntry(st(), h, s)

beforeEach(() => {
  st().reset()
  fetchRecaps.mockReset()
  fetchTurnSummaries.mockReset()
  requestRecap.mockReset()
  st().setSupport('h1', true)
})

describe('support gate', () => {
  it('is false until set; setSupport(false) turns it off', () => {
    expect(selectDialogueSupport('h2')).toBe(false)
    expect(selectDialogueSupport('h1')).toBe(true)
    st().setSupport('h1', false)
    expect(selectDialogueSupport('h1')).toBe(false)
  })

  it('a host without the capability sends no request from any loader', async () => {
    await st().loadRecaps('h2', 'S1')
    await st().loadTurnSummaries('h2', 'S1', { from: 1, to: 2 })
    await st().loadLatestSummaries('h2', 'S1', 1)
    await st().loadSummariesAfter('h2', 'S1', 3)
    expect(await st().regenerate('h2', 'S1', 'manual')).toBe('unsupported')
    expect(fetchRecaps).not.toHaveBeenCalled()
    expect(fetchTurnSummaries).not.toHaveBeenCalled()
    expect(requestRecap).not.toHaveBeenCalled()
    expect(entry('h2')).toBeUndefined()
  })
})

describe('loadRecaps', () => {
  it('fetches and caches the response by (host, session)', async () => {
    fetchRecaps.mockResolvedValueOnce(resp([recap(2), recap(1)]))
    await st().loadRecaps('h1', 'S1', 20)
    expect(fetchRecaps).toHaveBeenCalledWith('h1', 'S1', 20)
    expect(entry()?.recaps?.recaps.map((r) => r.id)).toEqual([2, 1])
    expect(entry('h1', 'S2')).toBeUndefined()
  })

  it('a failed fetch keeps the old cache and clears loading', async () => {
    fetchRecaps.mockResolvedValueOnce(resp([recap(1)]))
    await st().loadRecaps('h1', 'S1')
    fetchRecaps.mockRejectedValueOnce(new Error('network'))
    await st().loadRecaps('h1', 'S1')
    expect(entry()?.recaps?.recaps).toHaveLength(1)
    expect(entry()?.loading).toBe(false)
  })

  it('a fetch that lands after forgetHost is dropped', async () => {
    let resolve!: (v: RecapsResponse) => void
    fetchRecaps.mockReturnValueOnce(new Promise((r) => { resolve = r }))
    const p = st().loadRecaps('h1', 'S1')
    st().forgetHost('h1')
    resolve(resp([recap(1)]))
    await p
    expect(entry()).toBeUndefined()
  })
})

describe('turn summaries', () => {
  it('loadTurnSummaries sends the range, caches by turn number and returns the page', async () => {
    fetchTurnSummaries.mockResolvedValueOnce([turn(2), turn(3)])
    const r = await st().loadTurnSummaries('h1', 'S1', { from: 2, to: 3 })
    expect(fetchTurnSummaries).toHaveBeenCalledWith('h1', 'S1', { from: 2, to: 3 })
    expect(r.map((t) => t.turn_no)).toEqual([2, 3])
    expect(summariesInRange(entry(), 2, 3).map((t) => t.title)).toEqual(['t2', 't3'])
  })

  it('merges pages: a refetch replaces by turn number, ranges read oldest first', async () => {
    fetchTurnSummaries.mockResolvedValueOnce([turn(5), turn(6)])
    await st().loadTurnSummaries('h1', 'S1', { from: 5, to: 6 })
    fetchTurnSummaries.mockResolvedValueOnce([{ ...turn(6), title: 'new' }, turn(4)])
    await st().loadTurnSummaries('h1', 'S1', { from: 4, to: 6 })
    expect(summariesInRange(entry(), 4, 6).map((t) => t.title)).toEqual(['t4', 't5', 'new'])
    expect(summariesInRange(entry(), 5, 5)).toHaveLength(1)
    expect(summariesInRange(undefined, 1, 9)).toEqual([])
  })

  it('loadLatestSummaries asks for the newest N only (limit alone)', async () => {
    fetchTurnSummaries.mockResolvedValueOnce([turn(8), turn(9)])
    const r = await st().loadLatestSummaries('h1', 'S1', 2)
    expect(fetchTurnSummaries).toHaveBeenCalledWith('h1', 'S1', { limit: 2 })
    expect(r).toHaveLength(2)
  })

  it('loadSummariesAfter(n) asks from n + 1', async () => {
    fetchTurnSummaries.mockResolvedValueOnce([turn(6)])
    await st().loadSummariesAfter('h1', 'S1', 5)
    expect(fetchTurnSummaries).toHaveBeenCalledWith('h1', 'S1', { from: 6 })
  })

  it('a failed fetch returns [] and leaves the cache', async () => {
    fetchTurnSummaries.mockRejectedValueOnce(new Error('x'))
    expect(await st().loadLatestSummaries('h1', 'S1', 1)).toEqual([])
  })
})

describe('regenerate', () => {
  it('queued: marks generating until a recap newer than the one at request time arrives', async () => {
    fetchRecaps.mockResolvedValueOnce(resp([recap(4)]))
    await st().loadRecaps('h1', 'S1')
    requestRecap.mockResolvedValueOnce('queued')
    expect(await st().regenerate('h1', 'S1', 'manual')).toBe('queued')
    expect(requestRecap).toHaveBeenCalledWith('h1', 'S1', 'manual')
    expect(entry()?.generating).toBe(true)
    fetchRecaps.mockResolvedValueOnce(resp([recap(4)]))
    await st().loadRecaps('h1', 'S1')
    expect(entry()?.generating).toBe(true) // nothing newer yet
    fetchRecaps.mockResolvedValueOnce(resp([recap(5), recap(4)]))
    await st().loadRecaps('h1', 'S1')
    expect(entry()?.generating).toBe(false)
  })

  it('queued with no recap loaded yet: any first recap ends generating', async () => {
    requestRecap.mockResolvedValueOnce('queued')
    await st().regenerate('h1', 'S1', 'open')
    expect(entry()?.generating).toBe(true)
    fetchRecaps.mockResolvedValueOnce(resp([recap(1)]))
    await st().loadRecaps('h1', 'S1')
    expect(entry()?.generating).toBe(false)
  })

  it.each(['up_to_date', 'unavailable'] as const)('%s: returns it and does not mark generating', async (state) => {
    requestRecap.mockResolvedValueOnce(state)
    expect(await st().regenerate('h1', 'S1', 'manual')).toBe(state)
    expect(entry()?.generating ?? false).toBe(false)
  })

  it('a failed request returns "error" and does not mark generating', async () => {
    requestRecap.mockRejectedValueOnce(new Error('network'))
    expect(await st().regenerate('h1', 'S1', 'manual')).toBe('error')
    expect(entry()?.generating ?? false).toBe(false)
  })
})

describe('applyRecapEvent', () => {
  const ev = (sid: string) => JSON.stringify({ session_id: sid, recap_id: 7 })

  it('refetches recaps of a session the store holds; generating stays until the recap it announced is read', async () => {
    fetchRecaps.mockResolvedValueOnce(resp([recap(4)]))
    await st().loadRecaps('h1', 'S1')
    requestRecap.mockResolvedValueOnce('queued')
    await st().regenerate('h1', 'S1', 'manual')
    fetchRecaps.mockResolvedValueOnce(resp([recap(7), recap(4)]))
    st().applyRecapEvent('h1', ev('S1'))
    expect(entry()?.generating).toBe(true) // not cleared before the read lands
    await flush()
    expect(fetchRecaps).toHaveBeenCalledTimes(2)
    expect(entry()?.recaps?.recaps[0].id).toBe(7)
    expect(entry()?.generating).toBe(false)
  })

  it('does nothing for a session with no cache', async () => {
    st().applyRecapEvent('h1', ev('other'))
    await flush()
    expect(fetchRecaps).not.toHaveBeenCalled()
    expect(entry('h1', 'other')).toBeUndefined()
  })

  it('ignores a malformed value, and a host without support', async () => {
    fetchRecaps.mockResolvedValueOnce(resp([recap(1)]))
    await st().loadRecaps('h1', 'S1')
    fetchRecaps.mockClear()
    st().applyRecapEvent('h1', 'not json')
    st().applyRecapEvent('h1', JSON.stringify({ recap_id: 1 }))
    st().applyRecapEvent('h1', JSON.stringify([]))
    st().setSupport('h1', false)
    st().applyRecapEvent('h1', ev('S1'))
    await flush()
    expect(fetchRecaps).not.toHaveBeenCalled()
  })
})

describe('forgetHost', () => {
  it('drops the host\'s cache and support, and leaves others', async () => {
    st().setSupport('h2', true)
    fetchRecaps.mockImplementation(async () => resp([recap(1)]))
    await st().loadRecaps('h1', 'S1')
    await st().loadRecaps('h2', 'S1')
    st().forgetHost('h1')
    expect(entry('h1')).toBeUndefined()
    expect(selectDialogueSupport('h1')).toBe(false)
    expect(entry('h2')).toBeDefined()
    expect(selectDialogueSupport('h2')).toBe(true)
  })
})

describe('connection fence (setSupport(false) drops what is still out)', () => {
  const deferred = <T,>() => { let resolve!: (v: T) => void; const promise = new Promise<T>((r) => { resolve = r }); return { promise, resolve } }

  it('a recaps GET that lands after setSupport(false) is dropped; one sent after setSupport(true) lands', async () => {
    const d = deferred<RecapsResponse>()
    fetchRecaps.mockReturnValueOnce(d.promise)
    const p = st().loadRecaps('h1', 'S1')
    st().setSupport('h1', false)
    st().setSupport('h1', true) // reconnected before the old answer came: still the old connection's answer
    d.resolve(resp([recap(1)]))
    await p
    expect(entry()?.recaps ?? null).toBeNull()
    expect(entry()?.loading).toBe(false)
    fetchRecaps.mockResolvedValueOnce(resp([recap(2)]))
    await st().loadRecaps('h1', 'S1')
    expect(entry()?.recaps?.recaps.map((r) => r.id)).toEqual([2])
  })

  it('a turn-summaries GET that lands after setSupport(false) is dropped', async () => {
    const d = deferred<TurnSummary[]>()
    fetchTurnSummaries.mockReturnValueOnce(d.promise)
    const p = st().loadLatestSummaries('h1', 'S1', 5)
    st().setSupport('h1', false)
    st().setSupport('h1', true)
    d.resolve([turn(1)])
    expect(await p).toEqual([])
    expect(entry()?.summaries ?? {}).toEqual({})
  })

  it('a POST that answers queued after setSupport(false) does not mark generating', async () => {
    const d = deferred<string>()
    requestRecap.mockReturnValueOnce(d.promise)
    const p = st().regenerate('h1', 'S1', 'manual')
    st().setSupport('h1', false)
    st().setSupport('h1', true)
    d.resolve('queued')
    await p
    expect(entry()?.generating ?? false).toBe(false)
  })

  it('setSupport(false) clears generating on every entry of the host and leaves other hosts', async () => {
    st().setSupport('h2', true)
    requestRecap.mockResolvedValue('queued')
    await st().regenerate('h1', 'S1', 'manual')
    await st().regenerate('h2', 'S1', 'manual')
    expect(entry()?.generating).toBe(true)
    st().setSupport('h1', false)
    expect(entry()?.generating).toBe(false)
    expect(entry('h2')?.generating).toBe(true)
  })
})

describe('loadRecaps out-of-order answers', () => {
  const deferred = <T,>() => { let resolve!: (v: T) => void; const promise = new Promise<T>((r) => { resolve = r }); return { promise, resolve } }

  it('a limit=1 answer that lands after the full one does not shrink the history, and the counts come from the later request', async () => {
    const slow = deferred<RecapsResponse>()
    const full = deferred<RecapsResponse>()
    fetchRecaps.mockReturnValueOnce(slow.promise).mockReturnValueOnce(full.promise)
    const pSlow = st().loadRecaps('h1', 'S1', 1)
    const pFull = st().loadRecaps('h1', 'S1')
    full.resolve(resp([recap(3), recap(2), recap(1)], { newest_turn_no: 20, can_generate: false }))
    await pFull
    slow.resolve(resp([recap(2)], { newest_turn_no: 10, can_generate: true }))
    await pSlow
    const r = entry()?.recaps
    expect(r?.recaps.map((x) => x.id)).toEqual([3, 2, 1])
    expect(r?.newest_turn_no).toBe(20)
    expect(r?.can_generate).toBe(false)
    expect(r?.last_recap_to_no).toBe(3)
  })

  it('an earlier request answered late still contributes its rows, in id order, without duplicates', async () => {
    const a = deferred<RecapsResponse>()
    const b = deferred<RecapsResponse>()
    fetchRecaps.mockReturnValueOnce(a.promise).mockReturnValueOnce(b.promise)
    const pa = st().loadRecaps('h1', 'S1', 5)
    const pb = st().loadRecaps('h1', 'S1', 1)
    b.resolve(resp([recap(5)], { newest_turn_no: 30 }))
    await pb
    a.resolve(resp([recap(4), recap(3), recap(5)], { newest_turn_no: 25 }))
    await pa
    expect(entry()?.recaps?.recaps.map((x) => x.id)).toEqual([5, 4, 3])
    expect(entry()?.recaps?.newest_turn_no).toBe(30)
  })
})

describe('applyRecapEvent retries (fake timers)', () => {
  const ev = JSON.stringify({ session_id: 'S1', recap_id: 7 })
  beforeEach(() => { vi.useFakeTimers() })
  afterEach(() => { vi.useRealTimers() })
  const seed = async () => {
    fetchRecaps.mockResolvedValueOnce(resp([recap(4)]))
    await st().loadRecaps('h1', 'S1')
    requestRecap.mockResolvedValueOnce('queued')
    await st().regenerate('h1', 'S1', 'manual')
  }

  it('a failed re-read keeps generating, retries, and clears it once the announced recap is read', async () => {
    await seed()
    fetchRecaps.mockRejectedValueOnce(new Error('x')).mockResolvedValueOnce(resp([recap(7), recap(4)]))
    st().applyRecapEvent('h1', ev)
    await vi.advanceTimersByTimeAsync(0)
    expect(entry()?.generating).toBe(true)
    await vi.advanceTimersByTimeAsync(1000)
    expect(entry()?.recaps?.recaps[0].id).toBe(7)
    expect(entry()?.generating).toBe(false)
    expect(entry()?.loadFailed).toBe(false)
  })

  it('every attempt failing clears generating and flags the read as failed; the next good read clears the flag', async () => {
    await seed()
    fetchRecaps.mockRejectedValue(new Error('x'))
    st().applyRecapEvent('h1', ev)
    await vi.advanceTimersByTimeAsync(1000 + 2000 + 4000)
    expect(fetchRecaps).toHaveBeenCalledTimes(1 + 4)
    expect(entry()?.generating).toBe(false)
    expect(entry()?.loadFailed).toBe(true)
    fetchRecaps.mockReset()
    fetchRecaps.mockResolvedValueOnce(resp([recap(7), recap(4)]))
    await st().loadRecaps('h1', 'S1')
    expect(entry()?.loadFailed).toBe(false)
  })

  it('a re-read whose newest recap is older than the announced one stays generating', async () => {
    await seed()
    fetchRecaps.mockResolvedValueOnce(resp([recap(5), recap(4)])).mockResolvedValueOnce(resp([recap(7), recap(5), recap(4)]))
    st().applyRecapEvent('h1', ev)
    await vi.advanceTimersByTimeAsync(0)
    expect(entry()?.recaps?.recaps[0].id).toBe(5)
    expect(entry()?.generating).toBe(true)
    await vi.advanceTimersByTimeAsync(1000)
    expect(entry()?.generating).toBe(false)
  })

  it('an event for a session that was not generating (an automatic recap) shows generating while its re-read is out', async () => {
    fetchRecaps.mockResolvedValueOnce(resp([recap(4)]))
    await st().loadRecaps('h1', 'S1')
    fetchRecaps.mockRejectedValueOnce(new Error('x')).mockResolvedValueOnce(resp([recap(7), recap(4)]))
    st().applyRecapEvent('h1', ev)
    await vi.advanceTimersByTimeAsync(0)
    expect(entry()?.generating).toBe(true)
    await vi.advanceTimersByTimeAsync(1000)
    expect(entry()?.recaps?.recaps[0].id).toBe(7)
    expect(entry()?.generating).toBe(false)
  })

  it('an event for an older recap after a newer one does not lower the recap waited for', async () => {
    await seed()
    fetchRecaps.mockResolvedValue(resp([recap(6), recap(5), recap(4)]))
    st().applyRecapEvent('h1', JSON.stringify({ session_id: 'S1', recap_id: 8 }))
    st().applyRecapEvent('h1', JSON.stringify({ session_id: 'S1', recap_id: 6 }))
    await vi.advanceTimersByTimeAsync(0)
    expect(entry()?.recaps?.recaps[0].id).toBe(6)
    expect(entry()?.generating).toBe(true)
  })

  it('every re-read answering without the announced recap also ends as a failed read', async () => {
    await seed()
    fetchRecaps.mockResolvedValue(resp([recap(5), recap(4)]))
    st().applyRecapEvent('h1', ev)
    await vi.advanceTimersByTimeAsync(1000 + 2000 + 4000)
    expect(fetchRecaps).toHaveBeenCalledTimes(1 + 4)
    expect(entry()?.generating).toBe(false)
    expect(entry()?.loadFailed).toBe(true)
  })

  it('a newer event stops the older one\'s retries: they do not end the newer one\'s wait', async () => {
    await seed()
    fetchRecaps.mockRejectedValue(new Error('x'))
    st().applyRecapEvent('h1', ev)
    await vi.advanceTimersByTimeAsync(5000)
    st().applyRecapEvent('h1', JSON.stringify({ session_id: 'S1', recap_id: 9 }))
    await vi.advanceTimersByTimeAsync(2500) // the first event's last attempt (at 7s) would give up here
    expect(entry()?.generating).toBe(true)
    expect(entry()?.loadFailed).toBe(false)
  })

  it('a retry stops when the connection went away', async () => {
    await seed()
    fetchRecaps.mockRejectedValue(new Error('x'))
    st().applyRecapEvent('h1', ev)
    await vi.advanceTimersByTimeAsync(0)
    st().setSupport('h1', false)
    st().setSupport('h1', true)
    await vi.advanceTimersByTimeAsync(10000)
    expect(fetchRecaps).toHaveBeenCalledTimes(2)
  })
})
