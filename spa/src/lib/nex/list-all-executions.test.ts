import { describe, it, expect, vi, beforeEach } from 'vitest'
import * as api from './nex-api'
import { listAllExecutions, LIST_PAGE_LIMIT, LIST_MAX_PAGES } from './list-all-executions'

vi.mock('./nex-api', () => ({ listExecutions: vi.fn() }))
const row = (id: string) => ({ id, state: 'idle', provider: 'claude', principal_id: 'p', cwd: '/w', mount_kind: 'dir', brief: '', labels: {}, created_at: 1, updated_at: 1, duration_ms: null, event_count: 0, observers: 0, archived: false })

describe('listAllExecutions', () => {
  beforeEach(() => vi.mocked(api.listExecutions).mockReset())

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

  it('stops on a cursor that repeats (never loops) and marks it truncated', async () => {
    vi.mocked(api.listExecutions).mockResolvedValue({ items: [row('a')], next_cursor: 'a' } as never)
    const r = await listAllExecutions('h1', { includeArchived: true })
    expect(api.listExecutions).toHaveBeenCalledTimes(2)
    expect(r!.truncated).toBe(true)
  })

  it('stops after LIST_MAX_PAGES', async () => {
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
})
