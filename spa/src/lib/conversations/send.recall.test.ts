// K-2b: POST /api/sessions/{code}/recall — what each answer means (back / not run / may have popped), the one same-id retry.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { recallTyped, recallMessage, typedRecall } from './send'

const fetchMock = vi.hoisted(() => vi.fn())
vi.mock('../host-api', () => ({ pinnedHostFetch: fetchMock }))

const json = (status: number, body: unknown) => Promise.resolve(new Response(JSON.stringify(body), { status }))
const T = { code: 'dev001', instance: 'g1' }
beforeEach(() => fetchMock.mockReset())

describe('recallTyped', () => {
  it('posts client_msg_id + expected_tmux_instance to /recall; 200 {text} = back', async () => {
    fetchMock.mockImplementation(() => json(200, { text: 'a\nb' }))
    await expect(recallTyped('h', T, 'c1')).resolves.toEqual({ kind: 'back', text: 'a\nb' })
    expect(fetchMock.mock.calls[0][1]).toBe('/api/sessions/dev001/recall')
    expect(JSON.parse(fetchMock.mock.calls[0][2].body)).toEqual({ client_msg_id: 'c1', expected_tmux_instance: 'g1' })
  })

  it.each(['recall_empty', 'read_failed', 'clear_unknown'])('200 unknown (%s) = may have popped', async (reason) => {
    fetchMock.mockImplementation(() => json(200, { outcome: 'unknown', reason }))
    await expect(recallTyped('h', T, 'c1')).resolves.toEqual({ kind: 'may_have_popped', reason })
  })

  it('a 200 we cannot read (no text, no outcome) may have popped', async () => {
    fetchMock.mockImplementation(() => json(200, {}))
    await expect(recallTyped('h', T, 'c1')).resolves.toEqual({ kind: 'may_have_popped', reason: 'unreadable' })
  })

  it.each([
    'nothing_queued', 'recall_unsupported', 'recall_too_long', 'input_not_empty', 'recall_mismatch', 'input_not_cleared',
    'dialog_open', 'pane_busy', 'generation', 'owner_changed', 'no_owner',
  ])('409 refused %s = not run', async (reason) => {
    fetchMock.mockImplementation(() => json(409, { outcome: 'refused', reason }))
    await expect(recallTyped('h', T, 'c1')).resolves.toEqual({ kind: 'not_run', reason, status: 409 })
  })

  it('429 busy = not run', async () => {
    fetchMock.mockImplementation(() => json(429, { outcome: 'refused', reason: 'busy' }))
    await expect(recallTyped('h', T, 'c1')).resolves.toEqual({ kind: 'not_run', reason: 'busy', status: 429 })
  })

  it.each([[400, 'bad_client_msg_id'], [404, 'session_not_found'], [500, 'internal'], [503, 'unavailable']])('%s %s without an outcome = not run', async (status, error) => {
    fetchMock.mockImplementation(() => json(status, { error }))
    await expect(recallTyped('h', T, 'c1')).resolves.toEqual({ kind: 'not_run', reason: error, status })
  })

  it('503 keys_failed may have pressed Up = may have popped', async () => {
    fetchMock.mockImplementation(() => json(503, { error: 'keys_failed' }))
    await expect(recallTyped('h', T, 'c1')).resolves.toEqual({ kind: 'may_have_popped', reason: 'keys_failed' })
  })

  it('a plain-text 404 (a daemon with no /recall) = not run, daemon_outdated', async () => {
    fetchMock.mockImplementation(() => Promise.resolve(new Response('404 page not found', { status: 404 })))
    await expect(recallTyped('h', T, 'c1')).resolves.toEqual({ kind: 'not_run', reason: 'daemon_outdated', status: 404 })
  })

  it('network failure: retried once with the SAME id; the retry answering is the answer', async () => {
    fetchMock.mockImplementationOnce(() => Promise.reject(new TypeError('offline')))
    fetchMock.mockImplementationOnce(() => json(200, { text: 'x' }))
    await expect(recallTyped('h', T, 'c1')).resolves.toEqual({ kind: 'back', text: 'x' })
    expect(fetchMock).toHaveBeenCalledTimes(2)
    expect(JSON.parse(fetchMock.mock.calls[1][2].body).client_msg_id).toBe('c1')
  })

  it('network failure twice = may have popped (never a third try)', async () => {
    fetchMock.mockRejectedValueOnce(new TypeError('offline')).mockRejectedValueOnce(new TypeError('offline'))
    await expect(recallTyped('h', T, 'c1')).resolves.toEqual({ kind: 'may_have_popped', reason: 'network' })
    expect(fetchMock).toHaveBeenCalledTimes(2)
  })
})

describe('typedRecall (the adapter side)', () => {
  it('no target = not run, no request', async () => {
    await expect(typedRecall('h', () => null)()).resolves.toEqual({ kind: 'not_run', reason: 'no_instance', status: 0 })
    expect(fetchMock).not.toHaveBeenCalled()
  })

  it('one NEW client_msg_id per user action', async () => {
    fetchMock.mockImplementation(() => json(409, { outcome: 'refused', reason: 'nothing_queued' }))
    const run = typedRecall('h', () => T)
    await run(); await run()
    const ids = fetchMock.mock.calls.map((c) => JSON.parse(c[2].body).client_msg_id)
    expect(ids[0]).not.toBe(ids[1])
  })
})

describe('recallMessage', () => {
  it.each([
    ['input_not_empty', 'deck.recall.input_not_empty'], ['recall_mismatch', 'deck.recall.recall_mismatch'], ['nothing_queued', 'deck.recall.nothing_queued'],
    ['recall_unsupported', 'deck.recall.recall_unsupported'], ['recall_too_long', 'deck.recall.recall_too_long'], ['input_not_cleared', 'deck.recall.input_not_cleared'],
    ['busy', 'deck.recall.busy'],
  ])('not run %s', (reason, key) => {
    expect(recallMessage({ kind: 'not_run', reason, status: 409 })).toEqual({ key, tone: reason === 'busy' ? 'warn' : 'error' })
  })
  it('any other not-run carries its code', () => {
    expect(recallMessage({ kind: 'not_run', reason: 'dialog_open', status: 409 })).toEqual({ key: 'deck.recall.other', params: { code: 'dialog_open' }, tone: 'error' })
    expect(recallMessage({ kind: 'not_run', reason: '', status: 500 })).toEqual({ key: 'deck.recall.other', params: { code: '500' }, tone: 'error' })
  })
  it('may have popped', () => {
    expect(recallMessage({ kind: 'may_have_popped', reason: 'read_failed' })).toEqual({ key: 'deck.recall.may_have_popped', tone: 'warn' })
  })
})
