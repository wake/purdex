import { describe, it, expect, vi, beforeEach } from 'vitest'
import { ConversationApiError, fetchConversationIncrement, fetchConversationSnapshot, fetchConversationSubagent } from './api'

const fetchMock = vi.hoisted(() => vi.fn())
vi.mock('../host-api', () => ({ pinnedHostFetch: fetchMock }))

const SID = '11111111-2222-4333-8444-555555555555'
const json = (status: number, body: unknown) => Promise.resolve(new Response(JSON.stringify(body), { status, headers: { 'content-type': 'application/json' } }))

beforeEach(() => fetchMock.mockReset())

describe('fetchConversationSnapshot', () => {
  it('asks the pinned host for the conversation, with no query by default', async () => {
    fetchMock.mockImplementation(() => json(200, { cursor: 'e:1' }))
    await expect(fetchConversationSnapshot('h1', SID)).resolves.toEqual({ cursor: 'e:1' })
    expect(fetchMock.mock.calls[0][0]).toBe('h1')
    expect(fetchMock.mock.calls[0][1]).toBe(`/api/conversations/claude/${SID}`)
  })

  it('turns, before and around are query parameters (around is encoded)', async () => {
    fetchMock.mockImplementation(() => json(200, {}))
    await fetchConversationSnapshot('h1', SID, { turns: 30, before: 12 })
    expect(fetchMock.mock.calls[0][1]).toBe(`/api/conversations/claude/${SID}?turns=30&before=12`)
    await fetchConversationSnapshot('h1', SID, { around: 'item/with space' })
    expect(fetchMock.mock.calls[1][1]).toBe(`/api/conversations/claude/${SID}?around=item%2Fwith+space`)
  })

  it('passes the abort signal on', async () => {
    fetchMock.mockImplementation(() => json(200, {}))
    const ac = new AbortController()
    await fetchConversationSnapshot('h1', SID, { signal: ac.signal })
    expect(fetchMock.mock.calls[0][2].signal).toBe(ac.signal)
  })

  it.each([
    [404, 'not_found'], [404, 'provider_unsupported'], [404, 'item_not_found'], [422, 'item_not_shown'],
    [503, 'busy'], [503, 'file_changed'], [500, 'read_failed'], [400, 'bad_session_id'],
  ])('%i %s becomes a ConversationApiError carrying both', async (status, code) => {
    fetchMock.mockImplementation(() => json(status, { error: code }))
    const err = await fetchConversationSnapshot('h1', SID).catch((e) => e)
    expect(err).toBeInstanceOf(ConversationApiError)
    expect(err.status).toBe(status)
    expect(err.code).toBe(code)
  })

  it('an error answer with no JSON body keeps the status and an empty code', async () => {
    fetchMock.mockImplementation(() => Promise.resolve(new Response('gateway down', { status: 502 })))
    const err = await fetchConversationSnapshot('h1', SID).catch((e) => e)
    expect(err).toMatchObject({ status: 502, code: '' })
  })
})

describe('fetchConversationIncrement', () => {
  it('asks with the cursor, encoded', async () => {
    fetchMock.mockImplementation(() => json(200, { changes: [], header: {}, cursor: 'e:2' }))
    await fetchConversationIncrement('h1', SID, 'ep/och:2')
    expect(fetchMock.mock.calls[0][1]).toBe(`/api/conversations/claude/${SID}?after=ep%2Foch%3A2`)
  })

  it('changes come back as changes', async () => {
    const inc = { changes: [], header: {}, cursor: 'e:2' }
    fetchMock.mockImplementation(() => json(200, inc))
    await expect(fetchConversationIncrement('h1', SID, 'e:1')).resolves.toEqual({ kind: 'changes', increment: inc })
  })

  // a stale cursor (a restart, an eviction) is answered with a snapshot marked reset, not with an error
  it('a reset answer is a snapshot', async () => {
    const snap = { reset: true, conversation: { turns: [] }, header: {}, window: {}, cursor: 'f:1' }
    fetchMock.mockImplementation(() => json(200, snap))
    await expect(fetchConversationIncrement('h1', SID, 'old:1')).resolves.toEqual({ kind: 'snapshot', snapshot: snap })
  })

  it('a bad cursor is a 400 bad_cursor error', async () => {
    fetchMock.mockImplementation(() => json(400, { error: 'bad_cursor' }))
    await expect(fetchConversationIncrement('h1', SID, 'junk')).rejects.toMatchObject({ status: 400, code: 'bad_cursor' })
  })
})

describe('fetchConversationSubagent', () => {
  it('returns the items and the partial flag', async () => {
    fetchMock.mockImplementation(() => json(200, { items: [{ id: 'x', type: 'agent_text' }], partial: true }))
    await expect(fetchConversationSubagent('h1', SID, 'abc123')).resolves.toEqual({ items: [{ id: 'x', type: 'agent_text' }], partial: true })
    expect(fetchMock.mock.calls[0][1]).toBe(`/api/conversations/claude/${SID}/subagents/abc123`)
  })

  it('missing fields read as an empty, complete list', async () => {
    fetchMock.mockImplementation(() => json(200, {}))
    await expect(fetchConversationSubagent('h1', SID, 'abc')).resolves.toEqual({ items: [], partial: false })
  })

  it('404 not_found is an error with its code', async () => {
    fetchMock.mockImplementation(() => json(404, { error: 'not_found' }))
    await expect(fetchConversationSubagent('h1', SID, 'abc')).rejects.toMatchObject({ status: 404, code: 'not_found' })
  })
})
