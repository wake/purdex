import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import * as api from './nex-api'
import { listAllExecutions, LIST_PAGE_LIMIT, LIST_MAX_PAGES, DELTA_PAGE_LIMIT, UP_TO_END } from './list-all-executions'
import { NexApiError } from './types'

vi.mock('./nex-api', () => ({ listExecutions: vi.fn() }))
const row = (id: string) => ({ id, state: 'idle', provider: 'claude', principal_id: 'p', cwd: '/w', mount_kind: 'dir', brief: '', labels: {}, created_at: 1, updated_at: 1, duration_ms: null, event_count: 0, observers: 0, archived: false })

describe('listAllExecutions', () => {
  beforeEach(() => vi.mocked(api.listExecutions).mockReset())

  it('passes sessionId and labels on every page', async () => {
    vi.mocked(api.listExecutions)
      .mockResolvedValueOnce({ items: [row('a')], next_cursor: 'a' } as never)
      .mockResolvedValueOnce({ items: [row('b')], next_cursor: '' } as never)
    await listAllExecutions('h1', { includeArchived: true, sessionId: 'S', labels: { k: 'v' } })
    expect(api.listExecutions).toHaveBeenNthCalledWith(1, 'h1', { includeArchived: true, limit: LIST_PAGE_LIMIT, sessionId: 'S', labels: { k: 'v' } })
    expect(api.listExecutions).toHaveBeenNthCalledWith(2, 'h1', { includeArchived: true, limit: LIST_PAGE_LIMIT, sessionId: 'S', labels: { k: 'v' }, cursor: 'a' })
  })

  it('concatenates pages in order and passes the cursor and limit', async () => {
    vi.mocked(api.listExecutions)
      .mockResolvedValueOnce({ items: [row('a'), row('b')], next_cursor: 'b' } as never)
      .mockResolvedValueOnce({ items: [row('c')], next_cursor: '' } as never)
    const r = await listAllExecutions('h1', { includeArchived: false })
    expect(r!.items.map((i) => i.id)).toEqual(['a', 'b', 'c'])
    expect(r!.truncated).toBe(false)
    expect(api.listExecutions).toHaveBeenNthCalledWith(1, 'h1', { includeArchived: false, limit: LIST_PAGE_LIMIT })
    expect(api.listExecutions).toHaveBeenNthCalledWith(2, 'h1', { includeArchived: false, limit: LIST_PAGE_LIMIT, cursor: 'b' })
  })

  it('stops on a cursor that repeats (never loops), resolving stuck and not truncated', async () => {
    vi.mocked(api.listExecutions).mockResolvedValue({ items: [row('a')], next_cursor: 'a' } as never)
    const r = await listAllExecutions('h1', { includeArchived: true })
    expect(api.listExecutions).toHaveBeenCalledTimes(2)
    expect(r).toMatchObject({ stuck: true, stuckPage: 2, truncated: false })
    expect(r!.items.map((i) => i.id)).toEqual(['a'])
  })

  it('a stuck page (same cursor, same rows) resolves de-duplicated, stuck, not truncated', async () => {
    vi.mocked(api.listExecutions).mockResolvedValue({ items: [row('1'), row('2'), row('3')], next_cursor: '3' } as never)
    const r = await listAllExecutions('h1', { includeArchived: false })
    expect(r!.items.map((i) => i.id)).toEqual(['1', '2', '3'])
    expect(r).toMatchObject({ stuck: true, truncated: false })
  })

  it('only the page cap sets truncated (LIST_MAX_PAGES)', async () => {
    let n = 0
    vi.mocked(api.listExecutions).mockImplementation((async () => { n += 1; return { items: [row(`r${n}`)], next_cursor: `r${n}` } }) as never)
    const r = await listAllExecutions('h1', { includeArchived: false })
    expect(api.listExecutions).toHaveBeenCalledTimes(LIST_MAX_PAGES)
    expect(r!.truncated).toBe(true)
  })

  it('resolves null and asks for nothing more once the caller is stale', async () => {
    let current = true
    vi.mocked(api.listExecutions).mockImplementationOnce((async () => { current = false; return { items: [row('a')], next_cursor: 'a' } }) as never)
    expect(await listAllExecutions('h1', { includeArchived: false }, () => current)).toBeNull()
    expect(api.listExecutions).toHaveBeenCalledTimes(1)
  })

  it('a rejection mid-walk (page 2) rejects the walk', async () => {
    vi.mocked(api.listExecutions)
      .mockResolvedValueOnce({ items: [row('a')], next_cursor: 'a' } as never)
      .mockRejectedValueOnce(new Error('down'))
    await expect(listAllExecutions('h1', { includeArchived: true })).rejects.toThrow('down')
    expect(api.listExecutions).toHaveBeenCalledTimes(2)
  })

  it('the caller going stale during page 2 resolves null and asks for no page 3', async () => {
    let current = true
    vi.mocked(api.listExecutions)
      .mockResolvedValueOnce({ items: [row('a')], next_cursor: 'a' } as never)
      .mockImplementationOnce((async () => { current = false; return { items: [row('b')], next_cursor: 'b' } }) as never)
    expect(await listAllExecutions('h1', { includeArchived: true }, () => current)).toBeNull()
    expect(api.listExecutions).toHaveBeenCalledTimes(2)
  })

  it('counts malformed rows and keeps going', async () => {
    vi.mocked(api.listExecutions).mockResolvedValueOnce({ items: [row('a'), { nope: 1 }], next_cursor: '' } as never)
    const r = await listAllExecutions('h1', { includeArchived: false })
    expect(r!.items).toHaveLength(1)
    expect(r!.dropped).toBe(1)
  })

  it('sums dropped malformed rows across pages', async () => {
    vi.mocked(api.listExecutions)
      .mockResolvedValueOnce({ items: [row('a'), { nope: 1 }], next_cursor: 'a' } as never)
      .mockResolvedValueOnce({ items: [row('b'), { nope: 2 }, { nope: 3 }], next_cursor: '' } as never)
    const r = await listAllExecutions('h1', { includeArchived: false })
    expect(r!.items.map((i) => i.id)).toEqual(['a', 'b'])
    expect(r!.dropped).toBe(3)
  })

  it('resolves a multi-step cursor cycle A -> B -> A after at most three requests as stuck', async () => {
    vi.mocked(api.listExecutions)
      .mockResolvedValueOnce({ items: [row('1')], next_cursor: 'A' } as never)
      .mockResolvedValueOnce({ items: [row('2')], next_cursor: 'B' } as never)
      .mockResolvedValueOnce({ items: [row('3')], next_cursor: 'A' } as never)
      .mockResolvedValue({ items: [row('x')], next_cursor: 'B' } as never)
    const r = await listAllExecutions('h1', { includeArchived: false })
    expect(vi.mocked(api.listExecutions).mock.calls.length).toBeLessThanOrEqual(3)
    expect(r).toMatchObject({ stuck: true, stuckPage: 3, truncated: false })
    expect(r!.items.map((i) => i.id)).toEqual(['1', '2', '3'])
  })

  it('de-dupes ids by first occurrence across pages', async () => {
    vi.mocked(api.listExecutions)
      .mockResolvedValueOnce({ items: [row('1'), row('2'), row('3')], next_cursor: '3' } as never)
      .mockResolvedValueOnce({ items: [row('3'), row('4')], next_cursor: '' } as never)
    const r = await listAllExecutions('h1', { includeArchived: false })
    expect(r!.items.map((i) => i.id)).toEqual(['1', '2', '3', '4'])
    expect(r!.truncated).toBe(false)
  })

  it('rejects when a later page is malformed, naming the page', async () => {
    vi.mocked(api.listExecutions)
      .mockResolvedValueOnce({ items: [row('a')], next_cursor: 'a' } as never)
      .mockResolvedValueOnce({ items: {} } as never)
    await expect(listAllExecutions('h1', { includeArchived: false })).rejects.toThrow('nex: malformed executions page 2')
  })

  it('rejects when the first page is malformed', async () => {
    vi.mocked(api.listExecutions).mockResolvedValueOnce(null as never)
    await expect(listAllExecutions('h1', { includeArchived: false })).rejects.toThrow('nex: malformed executions page 1')
  })
})

describe('listAllExecutions in delta mode (#1866)', () => {
  const stamp = (ver: number, epoch = 'E1') => ({ epoch, ver, bseq: 0 })
  const busy = () => new NexApiError(503, 'nex_busy', 'busy')
  beforeEach(() => { vi.mocked(api.listExecutions).mockReset(); vi.useFakeTimers() })
  afterEach(() => vi.useRealTimers())

  it('walks with limit 100 and pdx=retry, recording each page ver and upTo (infinity for the final page)', async () => {
    vi.mocked(api.listExecutions)
      .mockResolvedValueOnce({ items: [row('a'), row('b')], next_cursor: 'b', pdx: stamp(5) } as never)
      .mockResolvedValueOnce({ items: [row('c')], next_cursor: '', pdx: stamp(9) } as never)
    const r = await listAllExecutions('h1', { includeArchived: false, delta: true })
    expect(api.listExecutions).toHaveBeenNthCalledWith(1, 'h1', { includeArchived: false, limit: DELTA_PAGE_LIMIT, pdxRetry: true })
    expect(DELTA_PAGE_LIMIT).toBe(100)
    expect(r!.pages).toEqual([{ ver: 5, upTo: 'b' }, { ver: 9, upTo: UP_TO_END }])
    expect(r!.epoch).toBe('E1')
  })

  it('upTo is the trusted next_cursor even when the page tail row is dropped as malformed', async () => {
    vi.mocked(api.listExecutions)
      .mockResolvedValueOnce({ items: [row('a'), { id: 'b' }], next_cursor: 'b', pdx: stamp(10) } as never)
      .mockResolvedValueOnce({ items: [row('c')], next_cursor: '', pdx: stamp(20) } as never)
    const r = await listAllExecutions('h1', { includeArchived: false, delta: true })
    expect(r!.dropped).toBe(1)
    expect(r!.pages[0]).toEqual({ ver: 10, upTo: 'b' })
  })

  it('an empty final page still ends the walk at infinity', async () => {
    vi.mocked(api.listExecutions)
      .mockResolvedValueOnce({ items: [row('a')], next_cursor: 'a', pdx: stamp(1) } as never)
      .mockResolvedValueOnce({ items: [], next_cursor: '', pdx: stamp(2) } as never)
    const r = await listAllExecutions('h1', { includeArchived: false, delta: true })
    expect(r!.pages).toEqual([{ ver: 1, upTo: 'a' }, { ver: 2, upTo: UP_TO_END }])
  })

  it('a page without a valid pdx makes its version 0 and warns', async () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    vi.mocked(api.listExecutions).mockResolvedValueOnce({ items: [row('a')], next_cursor: '' } as never)
    const r = await listAllExecutions('h1', { includeArchived: false, delta: true })
    expect(r!.pages).toEqual([{ ver: 0, upTo: UP_TO_END }])
    expect(r!.epoch).toBeUndefined()
    expect(warn).toHaveBeenCalled()
    warn.mockRestore()
  })

  it('the legacy walk keeps limit 500, no pdx=retry and version 0', async () => {
    vi.mocked(api.listExecutions).mockResolvedValueOnce({ items: [row('a')], next_cursor: '', pdx: stamp(4) } as never)
    const r = await listAllExecutions('h1', { includeArchived: false })
    expect(api.listExecutions).toHaveBeenCalledWith('h1', { includeArchived: false, limit: 500 })
    expect(r!.pages).toEqual([{ ver: 0, upTo: UP_TO_END }])
  })

  it('an epoch change between pages discards the walk and starts over', async () => {
    vi.mocked(api.listExecutions)
      .mockResolvedValueOnce({ items: [row('a')], next_cursor: 'a', pdx: stamp(5, 'E1') } as never)
      .mockResolvedValueOnce({ items: [row('b')], next_cursor: '', pdx: stamp(1, 'E2') } as never)
      .mockResolvedValueOnce({ items: [row('a'), row('b')], next_cursor: '', pdx: stamp(2, 'E2') } as never)
    const r = await listAllExecutions('h1', { includeArchived: false, delta: true })
    expect(r!.items.map((i) => i.id)).toEqual(['a', 'b'])
    expect(r!.epoch).toBe('E2')
    expect(r!.pages).toEqual([{ ver: 2, upTo: UP_TO_END }])
    expect(vi.mocked(api.listExecutions).mock.calls[2][1]).not.toHaveProperty('cursor')
  })

  it('retries the same page after nex_busy with 250 ms doubling backoff, then completes', async () => {
    vi.mocked(api.listExecutions)
      .mockRejectedValueOnce(busy()).mockRejectedValueOnce(busy())
      .mockResolvedValueOnce({ items: [row('a')], next_cursor: '', pdx: stamp(1) } as never)
    const p = listAllExecutions('h1', { includeArchived: false, delta: true })
    await vi.advanceTimersByTimeAsync(0)
    expect(api.listExecutions).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(249)
    expect(api.listExecutions).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(1)
    expect(api.listExecutions).toHaveBeenCalledTimes(2)
    await vi.advanceTimersByTimeAsync(499)
    expect(api.listExecutions).toHaveBeenCalledTimes(2)
    await vi.advanceTimersByTimeAsync(1)
    expect((await p)!.items.map((i) => i.id)).toEqual(['a'])
  })

  it('gives up after five retries', async () => {
    vi.mocked(api.listExecutions).mockRejectedValue(busy())
    const p = listAllExecutions('h1', { includeArchived: false, delta: true })
    const settled = expect(p).rejects.toMatchObject({ code: 'nex_busy' })
    await vi.advanceTimersByTimeAsync(10_000)
    await settled
    expect(api.listExecutions).toHaveBeenCalledTimes(6)
  })

  it('other errors are not retried, and the legacy walk never retries', async () => {
    vi.mocked(api.listExecutions).mockRejectedValue(new NexApiError(500, 'nex_list_panicked', 'x'))
    await expect(listAllExecutions('h1', { includeArchived: false, delta: true })).rejects.toMatchObject({ code: 'nex_list_panicked' })
    vi.mocked(api.listExecutions).mockReset().mockRejectedValue(busy())
    await expect(listAllExecutions('h1', { includeArchived: false })).rejects.toMatchObject({ code: 'nex_busy' })
    expect(api.listExecutions).toHaveBeenCalledTimes(1)
  })

  it('a walk superseded during the backoff makes no further request', async () => {
    vi.mocked(api.listExecutions).mockRejectedValue(busy())
    let current = true
    const p = listAllExecutions('h1', { includeArchived: false, delta: true }, () => current)
    await vi.advanceTimersByTimeAsync(0)
    current = false
    await vi.advanceTimersByTimeAsync(5000)
    expect(await p).toBeNull()
    expect(api.listExecutions).toHaveBeenCalledTimes(1)
  })
})
