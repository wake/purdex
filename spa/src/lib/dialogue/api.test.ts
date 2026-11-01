// spa/src/lib/dialogue/api.test.ts — the SPA client for the daemon's dialogue routes (plan T11 / T14a).
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { useHostStore } from '../../stores/useHostStore'
import { ApprovalApiError } from '../team/approval-api'
import { fetchRecaps, fetchTurnSummaries, requestRecap } from './api'

const testGlobal = globalThis as typeof globalThis & { fetch: ReturnType<typeof vi.fn> }

const json = (body: unknown, status = 200): Response =>
  new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })

async function rejection(p: Promise<unknown>): Promise<ApprovalApiError> {
  try { await p } catch (e) { expect(e).toBeInstanceOf(ApprovalApiError); return e as ApprovalApiError }
  throw new Error('expected rejection')
}

const recap = (over: Record<string, unknown> = {}) => ({
  id: 3, at: 1000, from_no: 1, to_no: 5, trigger: 'manual', headline: 'h', done: ['a'], now: 'n', decided: [], waiting: [], next: 'x', ...over,
})

describe('dialogue api', () => {
  let hostId: string
  beforeEach(() => {
    useHostStore.getState().reset()
    hostId = useHostStore.getState().addHost({ id: 'host-mlab', name: 'mlab', ip: '100.64.0.2', port: 7860, token: 'tok-1' })
    vi.stubGlobal('fetch', vi.fn())
  })
  afterEach(() => vi.unstubAllGlobals())

  describe('fetchRecaps', () => {
    it('GETs /api/dialogue/{sid}/recaps with the Bearer token and the limit, and returns the response', async () => {
      testGlobal.fetch.mockResolvedValueOnce(json({ recaps: [recap()], can_generate: true, newest_turn_no: 7, last_recap_to_no: 5 }))
      const r = await fetchRecaps(hostId, 'S 1', 10)
      const [url, init] = testGlobal.fetch.mock.calls[0]
      expect(url).toBe('http://100.64.0.2:7860/api/dialogue/S%201/recaps?limit=10')
      expect(init.method).toBe('GET')
      expect(new Headers(init.headers).get('Authorization')).toBe('Bearer tok-1')
      expect(r).toEqual({ recaps: [recap()], can_generate: true, newest_turn_no: 7, last_recap_to_no: 5 })
    })

    it('omits the query without a limit', async () => {
      testGlobal.fetch.mockResolvedValueOnce(json({ recaps: [], can_generate: false, newest_turn_no: 0, last_recap_to_no: 0 }))
      await fetchRecaps(hostId, 'S1')
      expect(testGlobal.fetch.mock.calls[0][0]).toBe('http://100.64.0.2:7860/api/dialogue/S1/recaps')
    })

    it('throws an error carrying the daemon code on a non-2xx answer', async () => {
      testGlobal.fetch.mockResolvedValueOnce(json({ error: 'bad_session_id' }, 400))
      const e = await rejection(fetchRecaps(hostId, 'x'))
      expect(e.status).toBe(400)
      expect(e.code).toBe('bad_session_id')
    })

    it('never hands the UI undefined arrays: a missing recaps list is [], missing lists of a recap are [], missing counters 0', async () => {
      testGlobal.fetch.mockResolvedValueOnce(json({}))
      expect(await fetchRecaps(hostId, 'S1')).toEqual({ recaps: [], can_generate: false, newest_turn_no: 0, last_recap_to_no: 0 })
      testGlobal.fetch.mockResolvedValueOnce(json({ recaps: [{ id: 9, headline: 'only' }] }))
      const r = await fetchRecaps(hostId, 'S1')
      expect(r.recaps[0]).toMatchObject({ id: 9, headline: 'only', done: [], decided: [], waiting: [], now: '', next: '', from_no: 0, to_no: 0 })
    })

    it('drops recap entries that are not objects with a numeric id, and non-string list items', async () => {
      testGlobal.fetch.mockResolvedValueOnce(json({ recaps: [null, 5, { headline: 'no id' }, recap({ done: ['ok', 3, null] })] }))
      const r = await fetchRecaps(hostId, 'S1')
      expect(r.recaps).toHaveLength(1)
      expect(r.recaps[0].done).toEqual(['ok'])
    })

    it('a body that is not an object is a bad_response error', async () => {
      testGlobal.fetch.mockResolvedValueOnce(json([1, 2]))
      expect((await rejection(fetchRecaps(hostId, 'S1'))).code).toBe('bad_response')
    })
  })

  describe('fetchTurnSummaries', () => {
    it('sends from, to and limit as query params', async () => {
      testGlobal.fetch.mockResolvedValueOnce(json({ turn_summaries: [{ turn_no: 2, at: 5, title: 't', summary: 's', push: 'p' }] }))
      const r = await fetchTurnSummaries(hostId, 'S1', { from: 3, to: 9, limit: 4 })
      expect(testGlobal.fetch.mock.calls[0][0]).toBe('http://100.64.0.2:7860/api/dialogue/S1/turn-summaries?from=3&to=9&limit=4')
      expect(r).toEqual([{ turn_no: 2, at: 5, title: 't', summary: 's', push: 'p' }])
    })

    it('from 0 is sent (it is a value, not absence); no range sends no query', async () => {
      testGlobal.fetch.mockImplementation(async () => json({ turn_summaries: [] }))
      await fetchTurnSummaries(hostId, 'S1', { from: 0 })
      expect(testGlobal.fetch.mock.calls[0][0]).toContain('?from=0')
      await fetchTurnSummaries(hostId, 'S1')
      expect(testGlobal.fetch.mock.calls[1][0]).toBe('http://100.64.0.2:7860/api/dialogue/S1/turn-summaries')
    })

    it('throws with the code on an error answer', async () => {
      testGlobal.fetch.mockResolvedValueOnce(json({ error: 'not_ready' }, 503))
      expect((await rejection(fetchTurnSummaries(hostId, 'S1'))).code).toBe('not_ready')
    })

    it('a missing list is [], malformed entries are dropped, missing fields are defaulted', async () => {
      testGlobal.fetch.mockResolvedValueOnce(json({}))
      expect(await fetchTurnSummaries(hostId, 'S1')).toEqual([])
      testGlobal.fetch.mockResolvedValueOnce(json({ turn_summaries: [null, { title: 'no no' }, { turn_no: 4 }] }))
      expect(await fetchTurnSummaries(hostId, 'S1')).toEqual([{ turn_no: 4, at: 0, title: '', summary: '', push: '' }])
    })
  })

  describe('requestRecap', () => {
    it('POSTs the trigger and returns the state', async () => {
      testGlobal.fetch.mockResolvedValueOnce(json({ state: 'queued' }))
      expect(await requestRecap(hostId, 'S1', 'open')).toBe('queued')
      const [url, init] = testGlobal.fetch.mock.calls[0]
      expect(url).toBe('http://100.64.0.2:7860/api/dialogue/S1/recap')
      expect(init.method).toBe('POST')
      expect(JSON.parse(init.body)).toEqual({ trigger: 'open' })
      expect(new Headers(init.headers).get('Content-Type')).toBe('application/json')
    })

    it.each(['up_to_date', 'unavailable'] as const)('returns %s', async (state) => {
      testGlobal.fetch.mockResolvedValueOnce(json({ state }))
      expect(await requestRecap(hostId, 'S1', 'manual')).toBe(state)
    })

    it('an unknown state is a bad_response error', async () => {
      testGlobal.fetch.mockResolvedValueOnce(json({ state: 'weird' }))
      expect((await rejection(requestRecap(hostId, 'S1', 'manual'))).code).toBe('bad_response')
    })

    it('throws with the code on an error answer', async () => {
      testGlobal.fetch.mockResolvedValueOnce(json({ error: 'not_ready' }, 503))
      expect((await rejection(requestRecap(hostId, 'S1', 'manual'))).code).toBe('not_ready')
    })
  })
})
