import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { useHostStore } from '../../stores/useHostStore'
import { HandoffApiError } from './handoff-api'
import { listConversations } from './conversations-api'

const testGlobal = globalThis as typeof globalThis & { fetch: ReturnType<typeof vi.fn> }
const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })

async function rejection(p: Promise<unknown>): Promise<HandoffApiError> {
  try { await p } catch (e) { expect(e).toBeInstanceOf(HandoffApiError); return e as HandoffApiError }
  throw new Error('expected rejection')
}

const page = { state: 'ended', scanned_at: 1, home: '/h', total: 1, truncated: false, unknown_owner: 0,
  conversations: [{ session_id: 's', title: 't', title_source: 'ai', cwd_exists: true, last_activity_at: 2, last_in: 'terminal' }] }

describe('listConversations', () => {
  let hostId: string
  beforeEach(() => {
    useHostStore.getState().reset()
    hostId = useHostStore.getState().addHost({ id: 'host-mlab', name: 'mlab', ip: '100.64.0.2', port: 7860, token: 'tok-1' })
    vi.stubGlobal('fetch', vi.fn())
  })
  afterEach(() => vi.unstubAllGlobals())

  it('GETs the path with state query, Bearer and X-Pdx-Client, and parses 200', async () => {
    testGlobal.fetch.mockResolvedValueOnce(json(page))
    const got = await listConversations(hostId, 'ended')
    const [url, init] = testGlobal.fetch.mock.calls[0]
    expect(url).toBe('http://100.64.0.2:7860/api/nex/conversations?state=ended')
    expect(init.method).toBe('GET')
    expect(init.body).toBeUndefined()
    const h = new Headers(init.headers)
    expect(h.get('Authorization')).toBe('Bearer tok-1')
    expect(h.get('X-Pdx-Client')).toBeTruthy()
    expect(got).toEqual(page)
  })

  it('uses state=gone', async () => {
    testGlobal.fetch.mockResolvedValueOnce(json({ ...page, state: 'gone' }))
    await listConversations(hostId, 'gone')
    expect(testGlobal.fetch.mock.calls[0][0]).toContain('?state=gone')
  })

  it('maps 404 to http_404', async () => {
    testGlobal.fetch.mockResolvedValueOnce(new Response('not found', { status: 404 }))
    const e = await rejection(listConversations(hostId, 'ended'))
    expect(e.status).toBe(404)
    expect(e.code).toBe('http_404')
  })

  it('maps 503 to conversations_unavailable', async () => {
    testGlobal.fetch.mockResolvedValueOnce(json({ error: 'engine down', code: 'conversations_unavailable' }, 503))
    const e = await rejection(listConversations(hostId, 'ended'))
    expect(e.code).toBe('conversations_unavailable')
    expect(e.message).toBe('engine down')
  })

  it('rejects a malformed body', async () => {
    testGlobal.fetch.mockResolvedValueOnce(json({ state: 'ended' }))
    const e = await rejection(listConversations(hostId, 'ended'))
    expect(e.code).toBe('bad_response')
  })

  it('rejects a non-object body', async () => {
    testGlobal.fetch.mockResolvedValueOnce(json([1]))
    await rejection(listConversations(hostId, 'ended'))
  })

  it('unknown host is host_removed without fetching', async () => {
    const e = await rejection(listConversations('nope', 'ended'))
    expect(e.code).toBe('host_removed')
    expect(testGlobal.fetch).not.toHaveBeenCalled()
  })
})
