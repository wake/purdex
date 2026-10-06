import { describe, it, expect, vi, beforeEach } from 'vitest'
import * as api from './nex-api'
import { listAllExecutions, LIST_PAGE_LIMIT, LIST_MAX_PAGES } from './list-all-executions'

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

  it('counts malformed rows and keeps going', async () => {
    vi.mocked(api.listExecutions).mockResolvedValueOnce({ items: [row('a'), { nope: 1 }], next_cursor: '' } as never)
    const r = await listAllExecutions('h1', { includeArchived: false })
    expect(r!.items).toHaveLength(1)
    expect(r!.dropped).toBe(1)
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
