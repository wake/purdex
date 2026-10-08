// spa/src/lib/team/unattended-api.test.ts — the SPA client for GET / PUT /api/team/unattended (unattended spec
// D-U23-1, D-U23-6; plan PU-2a). Same transport and error mapping as the approval routes (approval-api.ts).
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { useHostStore } from '../../stores/useHostStore'
import { ApprovalApiError } from './approval-api'
import { __resetClientDescriptorForTests } from './client-label'
import { getUnattended, putUnattended } from './unattended-api'
import type { Approval } from './types'

const testGlobal = globalThis as typeof globalThis & { fetch: ReturnType<typeof vi.fn> }

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

async function rejection(p: Promise<unknown>): Promise<ApprovalApiError> {
  try {
    await p
  } catch (e) {
    expect(e).toBeInstanceOf(ApprovalApiError)
    return e as ApprovalApiError
  }
  throw new Error('expected rejection')
}

const row = (id: string, decided_at: number): Approval => ({
  id, kind: 'lead', host_id: 'd1',
  origin: { session_id: 'S1', ref: '_40iueq', name: 'purdex-7c', pid: 4242, proc_start: 'x', cwd: '/w/purdex', tmux: '' },
  payload: { reason: 'r', max_members: 3, roots: ['/w/purdex'] },
  state: 'approved', created_at: decided_at, deadline_at: decided_at + 540_000, lease_until: decided_at + 30_000,
  decided_by: { kind: 'unattended', label: '無人值守模式' }, decided_at,
})

const view = (over: Record<string, unknown> = {}) => ({
  on: true, since: 1_000, changed_at: 1_000,
  changed_by: { kind: 'app', label: 'Purdex.app', addr: '100.64.0.4:51234' },
  approved: [row('a2', 3_000), row('a1', 2_000)], truncated: false,
  ...over,
})

describe('unattended-api', () => {
  let hostId: string

  beforeEach(() => {
    __resetClientDescriptorForTests()
    useHostStore.getState().reset()
    hostId = useHostStore.getState().addHost({ id: 'host-mlab', name: 'mlab', ip: '100.64.0.2', port: 7860, token: 'tok-1' })
    vi.stubGlobal('fetch', vi.fn())
  })
  afterEach(() => vi.unstubAllGlobals())

  describe('getUnattended', () => {
    it('GETs /api/team/unattended with the Bearer token and returns the view', async () => {
      testGlobal.fetch.mockResolvedValueOnce(json(view({ truncated: true, next_before: 2_000 })))
      const v = await getUnattended(hostId)
      const [url, init] = testGlobal.fetch.mock.calls[0]
      expect(url).toBe('http://100.64.0.2:7860/api/team/unattended')
      expect(init.method).toBe('GET')
      expect(new Headers(init.headers).get('Authorization')).toBe('Bearer tok-1')
      expect(v.on).toBe(true)
      expect(v.since).toBe(1_000)
      expect(v.approved.map((a) => a.id)).toEqual(['a2', 'a1'])
      expect(v.truncated).toBe(true)
      expect(v.next_before).toBe(2_000)
      expect(v.changed_by?.addr).toBe('100.64.0.4:51234')
    })

    it('sends before and limit as query parameters when given', async () => {
      testGlobal.fetch.mockResolvedValueOnce(json(view()))
      await getUnattended(hostId, { before: 2_000, limit: 50 })
      expect(testGlobal.fetch.mock.calls[0][0]).toBe('http://100.64.0.2:7860/api/team/unattended?before=2000&limit=50')
      testGlobal.fetch.mockResolvedValueOnce(json(view()))
      await getUnattended(hostId, { limit: 10 })
      expect(testGlobal.fetch.mock.calls[1][0]).toBe('http://100.64.0.2:7860/api/team/unattended?limit=10')
    })

    it('hands a caller\'s abort signal to the request', async () => {
      testGlobal.fetch.mockResolvedValueOnce(json(view()))
      const ctl = new AbortController()
      await getUnattended(hostId, undefined, ctl.signal)
      expect(testGlobal.fetch.mock.calls[0][1].signal).toBe(ctl.signal)
    })

    it('a plain-text 404 (a daemon without the route) is `unsupported`', async () => {
      testGlobal.fetch.mockResolvedValueOnce(new Response('404 page not found\n', { status: 404 }))
      const err = await rejection(getUnattended(hostId))
      expect(err.status).toBe(404)
      expect(err.code).toBe('unsupported')
    })

    it("a 400's code and detail surface", async () => {
      testGlobal.fetch.mockResolvedValueOnce(json({ error: 'bad_request', detail: 'before and limit must be positive integers' }, 400))
      const err = await rejection(getUnattended(hostId, { before: 2_000 }))
      expect(err.status).toBe(400)
      expect(err.code).toBe('bad_request')
      expect(err.detail).toBe('before and limit must be positive integers')
    })

    it('an answer whose state is not the wire shape is rejected, not read as off', async () => {
      testGlobal.fetch.mockResolvedValueOnce(json(view({ on: 'true' })))
      const err = await rejection(getUnattended(hostId))
      expect(err.code).toBe('bad_response')
    })

    // The whole envelope is the trust boundary (PU-2a review): a broken one is `bad_response`, never an empty page.
    const { approved: _a, ...withoutApproved } = view()
    const { truncated: _t, ...withoutTruncated } = view()
    it.each([
      ['approved is null', view({ approved: null })],
      ['approved is missing', withoutApproved],
      ['approved is an object', view({ approved: {} })],
      ['truncated is missing', withoutTruncated],
      ['changed_by is an empty record', view({ changed_by: {} })],
      ['changed_by.kind is not a string', view({ changed_by: { kind: 1, label: 'Purdex.app' } })],
      ['changed_by.label is empty', view({ changed_by: { kind: 'app', label: '' } })],
      ['changed_by.addr is a number', view({ changed_by: { kind: 'app', label: 'Purdex.app', addr: 51234 } })],
      ['truncated is a string', view({ truncated: 'true' })],
      ['truncated is true without next_before', view({ truncated: true })],
      ['next_before is 0', view({ truncated: true, next_before: 0 })],
      ['next_before is negative', view({ truncated: true, next_before: -5 })],
      ['next_before is a string', view({ truncated: true, next_before: '2000' })],
      ['next_before is 0 on a last page', view({ next_before: 0 })],
      ['swept is negative', view({ swept: -1 })],
      ['swept is a string', view({ swept: '2' })],
      ['pending is negative', view({ pending: -1 })],
      ['pending is null', view({ pending: null })],
      ['list_failed is a string', view({ list_failed: 'yes' })],
      ['a lead row is malformed', view({ approved: [row('a1', 2_000), { ...row('a0', 1_500), origin: null }] })],
      ['a self_relay row is malformed', view({ approved: [{ ...row('a1', 2_000), kind: 'self_relay', created_at: 'x' }] })],
      ['a lead row has no origin.proc_start', view({ approved: [{ ...row('a1', 2_000), origin: { session_id: 'S1', ref: '_40iueq', name: 'purdex-7c', pid: 4242, cwd: '/w/purdex', tmux: '' } }] })],
      ['a row has no kind', view({ approved: [row('a1', 2_000), { id: 'x' }] })],
      ['a row is not a record', view({ approved: [row('a1', 2_000), 'a0'] })],
    ])('an answer where %s is `bad_response`, not a page', async (_what, body) => {
      testGlobal.fetch.mockResolvedValueOnce(json(body))
      expect((await rejection(getUnattended(hostId))).code).toBe('bad_response')
    })

    it('a row of a kind this build does not know (a later daemon\'s) is skipped, the page still shown', async () => {
      testGlobal.fetch.mockResolvedValueOnce(json(view({ approved: [row('a2', 3_000), { ...row('a1', 2_000), kind: 'adopt' }] })))
      expect((await getUnattended(hostId)).approved.map((a) => a.id)).toEqual(['a2'])
    })

    it('an empty last page is a valid answer', async () => {
      testGlobal.fetch.mockResolvedValueOnce(json(view({ approved: [] })))
      const v = await getUnattended(hostId)
      expect(v.approved).toEqual([])
      expect(v.truncated).toBe(false)
      expect(v.next_before).toBeUndefined()
    })

    it('an unconfigured host is refused before any request (`host_removed`)', async () => {
      const err = await rejection(getUnattended('no-such-host'))
      expect(err.code).toBe('host_removed')
      expect(testGlobal.fetch).not.toHaveBeenCalled()
    })
  })

  describe('putUnattended', () => {
    it('PUTs {on, client} with this app\'s client descriptor', async () => {
      testGlobal.fetch.mockResolvedValueOnce(json(view({ swept: 2, pending: 0 })))
      const v = await putUnattended(hostId, true)
      const [url, init] = testGlobal.fetch.mock.calls[0]
      expect(url).toBe('http://100.64.0.2:7860/api/team/unattended')
      expect(init.method).toBe('PUT')
      expect(new Headers(init.headers).get('Content-Type')).toBe('application/json')
      expect(JSON.parse(init.body)).toEqual({ on: true, client: { kind: 'app', label: 'Purdex.app' } })
      expect(v.on).toBe(true)
      expect(v.swept).toBe(2)
      expect(v.pending).toBe(0)
    })

    it('sends on:false to turn it off', async () => {
      testGlobal.fetch.mockResolvedValueOnce(json(view({ on: false, approved: [] })))
      const v = await putUnattended(hostId, false)
      expect(JSON.parse(testGlobal.fetch.mock.calls[0][1].body).on).toBe(false)
      expect(v.on).toBe(false)
    })

    it('a 200 with list_failed is still a success: the state is what is stored', async () => {
      testGlobal.fetch.mockResolvedValueOnce(json(view({ approved: [], list_failed: true })))
      const v = await putUnattended(hostId, true)
      expect(v.on).toBe(true)
      expect(v.list_failed).toBe(true)
      expect(v.approved).toEqual([])
    })

    it("a 400's detail surfaces; a 503 keeps its code", async () => {
      testGlobal.fetch.mockResolvedValueOnce(json({ error: 'bad_request', detail: 'client must be {"kind":"app","label":…}' }, 400))
      const a = await rejection(putUnattended(hostId, true))
      expect(a.code).toBe('bad_request')
      expect(a.detail).toBe('client must be {"kind":"app","label":…}')
      testGlobal.fetch.mockResolvedValueOnce(json({ error: 'not_ready', detail: 'daemon is stopping' }, 503))
      const b = await rejection(putUnattended(hostId, false))
      expect(b.status).toBe(503)
      expect(b.code).toBe('not_ready')
    })

    it('a plain-text 404 is `unsupported`', async () => {
      testGlobal.fetch.mockResolvedValueOnce(new Response('404 page not found\n', { status: 404 }))
      expect((await rejection(putUnattended(hostId, true))).code).toBe('unsupported')
    })
  })
})
