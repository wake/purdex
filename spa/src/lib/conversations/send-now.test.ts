// spa/src/lib/conversations/send-now.test.ts — POST /api/sessions/{code}/send-now (message-stages plan §1.3): what each answer means.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { sendNowMessage, sendNowTyped, typedSendNow } from './send'

const fetchMock = vi.hoisted(() => vi.fn())
vi.mock('../host-api', () => ({ pinnedHostFetch: fetchMock }))

const json = (status: number, body: unknown) => Promise.resolve(new Response(JSON.stringify(body), { status }))
const T = { code: 'dev001', instance: 'g1' }
beforeEach(() => fetchMock.mockReset())

describe('sendNowTyped', () => {
  it('posts client_msg_id + expected_tmux_instance; 200 sent_now', async () => {
    fetchMock.mockImplementation(() => json(200, { outcome: 'sent_now' }))
    await expect(sendNowTyped('h', T, 'c1')).resolves.toEqual({ kind: 'sent_now' })
    expect(fetchMock.mock.calls[0][1]).toBe('/api/sessions/dev001/send-now')
    expect(JSON.parse(fetchMock.mock.calls[0][2].body)).toEqual({ client_msg_id: 'c1', expected_tmux_instance: 'g1' })
  })
  it.each(['waiting', 'nothing_queued', 'input_not_empty', 'no_owner', 'generation'])('409 refused %s = nothing was sent', async (reason) => {
    fetchMock.mockImplementation(() => json(409, { outcome: 'refused', reason }))
    await expect(sendNowTyped('h', T, 'c1')).resolves.toEqual({ kind: 'refused', reason })
  })
  it('429 busy is a refusal too', async () => {
    fetchMock.mockImplementation(() => json(429, { outcome: 'refused', reason: 'busy' }))
    await expect(sendNowTyped('h', T, 'c1')).resolves.toEqual({ kind: 'refused', reason: 'busy' })
  })
  it('200 unknown, and a 200 we cannot read, may have run', async () => {
    fetchMock.mockImplementation(() => json(200, { outcome: 'unknown', reason: 'keys_failed' }))
    await expect(sendNowTyped('h', T, 'c1')).resolves.toEqual({ kind: 'unknown', reason: 'keys_failed' })
    fetchMock.mockImplementation(() => json(200, {}))
    await expect(sendNowTyped('h', T, 'c1')).resolves.toEqual({ kind: 'unknown', reason: 'unreadable' })
  })
  it.each([500, 502, 503])('a %i may have come after the keys: unknown, not 「nothing was sent」', async (status) => {
    fetchMock.mockImplementation(() => json(status, { error: 'keys_failed' }))
    await expect(sendNowTyped('h', T, 'c1')).resolves.toEqual({ kind: 'unknown', reason: 'keys_failed' })
    fetchMock.mockImplementation(() => Promise.resolve(new Response('bad gateway', { status })))
    await expect(sendNowTyped('h', T, 'c1')).resolves.toEqual({ kind: 'unknown', reason: `http_${status}` })
  })
  it('a 400 / a 404 with an error code is the daemon declining before any keys: refused', async () => {
    fetchMock.mockImplementation(() => json(400, { error: 'bad_request' }))
    await expect(sendNowTyped('h', T, 'c1')).resolves.toEqual({ kind: 'refused', reason: 'bad_request' })
    fetchMock.mockImplementation(() => json(404, { error: 'session_not_found' }))
    await expect(sendNowTyped('h', T, 'c1')).resolves.toEqual({ kind: 'refused', reason: 'session_not_found' })
  })
  it.each([408, 418, 422])('a %i (a proxy or an answer outside the contract) may have come after the keys: unknown', async (status) => {
    fetchMock.mockImplementation(() => Promise.resolve(new Response('', { status })))
    await expect(sendNowTyped('h', T, 'c1')).resolves.toEqual({ kind: 'unknown', reason: `http_${status}` })
  })
  it('a 400 / 404 without a reason is not the contract either: unknown', async () => {
    fetchMock.mockImplementation(() => Promise.resolve(new Response('{}', { status: 400 })))
    await expect(sendNowTyped('h', T, 'c1')).resolves.toEqual({ kind: 'unknown', reason: 'http_400' })
  })
  it('a lost connection may have run; a daemon with no route (404 in plain text) is outdated', async () => {
    fetchMock.mockImplementation(() => Promise.reject(new Error('net')))
    await expect(sendNowTyped('h', T, 'c1')).resolves.toEqual({ kind: 'unknown', reason: 'network' })
    fetchMock.mockImplementation(() => Promise.resolve(new Response('404 page not found', { status: 404 })))
    await expect(sendNowTyped('h', T, 'c1')).resolves.toEqual({ kind: 'refused', reason: 'daemon_outdated' })
  })
  it('typedSendNow takes a NEW id per call and sends nothing without a target', async () => {
    fetchMock.mockImplementation(() => json(200, { outcome: 'sent_now' }))
    const go = typedSendNow('h', () => T)
    await go(); await go()
    const ids = fetchMock.mock.calls.map((c) => JSON.parse(c[2].body).client_msg_id)
    expect(new Set(ids).size).toBe(2)
    fetchMock.mockClear()
    await expect(typedSendNow('h', () => null)()).resolves.toEqual({ kind: 'refused', reason: 'no_instance' })
    expect(fetchMock).not.toHaveBeenCalled()
  })
})

describe('sendNowMessage', () => {
  it('sent_now says the reply in progress may be interrupted', () => {
    expect(sendNowMessage({ kind: 'sent_now' })).toEqual({ key: 'deck.sendnow.done', tone: 'ok' })
  })
  it('a known refusal has its own text, another is named by its code, unknown is a warning', () => {
    expect(sendNowMessage({ kind: 'refused', reason: 'input_not_empty' })).toEqual({ key: 'deck.sendnow.refused.input_not_empty', tone: 'error' })
    expect(sendNowMessage({ kind: 'refused', reason: 'weird' })).toEqual({ key: 'deck.sendnow.refused.other', params: { code: 'weird' }, tone: 'error' })
    expect(sendNowMessage({ kind: 'refused', reason: 'busy' }).tone).toBe('warn')
    expect(sendNowMessage({ kind: 'unknown', reason: 'x' })).toEqual({ key: 'deck.sendnow.unknown', tone: 'warn' })
  })
})
