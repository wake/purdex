import { describe, it, expect, beforeEach, vi } from 'vitest'

const pinned = vi.fn<(hostId: string, path: string, init?: RequestInit) => Promise<Response>>()
vi.mock('../host-api', () => ({ pinnedHostFetch: (h: string, p: string, i?: RequestInit) => pinned(h, p, i) }))

import { fetchConversation, fetchEntries } from './api'
import { useHostStore } from '../../stores/useHostStore'
import { wireEntry } from './fixtures'

const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })

beforeEach(() => {
  pinned.mockReset()
  useHostStore.setState({ hosts: { h1: { id: 'h1', name: 'h1', ip: '1.2.3.4', port: 7860, order: 0 } }, hostOrder: ['h1'], runtime: {} })
})

describe('fetchConversation', () => {
  it('asks the conversation route with limit and before', async () => {
    pinned.mockResolvedValue(json({ conv_key: 'c1', status: 's', status_at: 1, entries: [wireEntry()] }))
    const r = await fetchConversation('h1', 'claude', 'sess/1', { limit: 20, before: 9 })
    expect(pinned.mock.calls[0][1]).toBe('/api/workbook/conversations/claude/sess%2F1?limit=20&before=9')
    expect(r.kind).toBe('ok')
  })
  it('answers a 404 as not_found, both the daemon\'s JSON and the mux\'s plain text', async () => {
    pinned.mockResolvedValueOnce(json({ error: 'not_found' }, 404))
    expect((await fetchConversation('h1', 'claude', 's')).kind).toBe('not_found')
    pinned.mockResolvedValueOnce(new Response('404 page not found', { status: 404 }))
    expect((await fetchConversation('h1', 'claude', 's')).kind).toBe('not_found')
  })
  it('throws on a 500 and on a broken envelope', async () => {
    pinned.mockResolvedValueOnce(json({ error: 'internal' }, 500))
    await expect(fetchConversation('h1', 'claude', 's')).rejects.toMatchObject({ status: 500 })
    pinned.mockResolvedValueOnce(json({ nope: 1 }))
    await expect(fetchConversation('h1', 'claude', 's')).rejects.toMatchObject({ code: 'bad_response' })
  })
  it('an answer holding another conversation\'s entry is bad_response', async () => {
    pinned.mockResolvedValueOnce(json({ conv_key: 'c1', entries: [wireEntry({ conv_key: 'other' })] }))
    await expect(fetchConversation('h1', 'claude', 's')).rejects.toMatchObject({ code: 'bad_response' })
  })
  it('an unknown host rejects without touching the network (host_removed)', async () => {
    await expect(fetchConversation('ghost', 'claude', 's')).rejects.toMatchObject({ code: 'host_removed' })
    expect(pinned).not.toHaveBeenCalled()
  })
})

describe('fetchEntries', () => {
  it('builds the query and parses the entries', async () => {
    pinned.mockResolvedValue(json({ entries: [wireEntry({ id: 3 }), wireEntry({ id: -1 })] }))
    const es = await fetchEntries('h1', { since: 5, thingDone: true, limit: 10 })
    expect(pinned.mock.calls[0][1]).toBe('/api/workbook/entries?since=5&thing_done=1&limit=10')
    expect(es.map((e) => e.id)).toEqual([3])
  })
})
