// spa/src/lib/team/approval-api.test.ts — the SPA client for POST /api/team/approvals/{id}/decide and
// GET /api/team/approvals?state=open (lead-team spec §6.2, plan preamble "Routes"). A non-2xx body is
// `{error, detail, approval}`; the 409s carry the Approval, so the caller can say who handled it.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { useHostStore } from '../../stores/useHostStore'
import { ApprovalApiError, decideApproval, fetchInflight, listOpenApprovals } from './approval-api'
import { leadPayloadOf, selfRelayPayloadOf, type Approval } from './types'

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

const approval = (over: Partial<Approval> = {}): Approval => ({
  id: 'req-1', kind: 'lead', host_id: 'd1',
  origin: { session_id: 'S1', ref: '_40iueq', name: 'purdex-7c', pid: 4242, proc_start: 'Tue Oct  7 10:00:00 2026', cwd: '/w/purdex', tmux: 'purdex:@1.%2' },
  payload: { reason: '要平行跑三個 PR', max_members: 3, roots: ['/w/purdex'] },
  state: 'open', created_at: 1_000, deadline_at: 541_000, lease_until: 31_000,
  ...over,
})
const client = { kind: 'app' as const, label: 'Purdex.app @ mlab' }

describe('approval-api', () => {
  let hostId: string

  beforeEach(() => {
    useHostStore.getState().reset()
    hostId = useHostStore.getState().addHost({ id: 'host-mlab', name: 'mlab', ip: '100.64.0.2', port: 7860, token: 'tok-1' })
    vi.stubGlobal('fetch', vi.fn())
  })
  afterEach(() => vi.unstubAllGlobals())

  describe('decideApproval', () => {
    it('POSTs the DecideRequest as JSON to /api/team/approvals/{id}/decide with the Bearer token', async () => {
      testGlobal.fetch.mockResolvedValueOnce(json(approval({ state: 'approved', decided_by: client, decided_at: 2_000, grant: { max_members: 3, roots: ['/w/purdex'] } })))
      const r = await decideApproval(hostId, 'req-1', { decision: 'approve', grant: { max_members: 3, roots: ['/w/purdex'] }, client })
      expect(testGlobal.fetch).toHaveBeenCalledTimes(1)
      const [url, init] = testGlobal.fetch.mock.calls[0]
      expect(url).toBe('http://100.64.0.2:7860/api/team/approvals/req-1/decide')
      expect(init.method).toBe('POST')
      expect(JSON.parse(init.body)).toEqual({ decision: 'approve', grant: { max_members: 3, roots: ['/w/purdex'] }, client })
      const h = new Headers(init.headers)
      expect(h.get('Content-Type')).toBe('application/json')
      expect(h.get('Authorization')).toBe('Bearer tok-1')
      expect(r.state).toBe('approved')
      expect(r.decided_by?.label).toBe('Purdex.app @ mlab')
    })

    it('URL-encodes the id', async () => {
      testGlobal.fetch.mockResolvedValueOnce(json(approval({ id: 'a/b c', state: 'denied' })))
      await decideApproval(hostId, 'a/b c', { decision: 'deny', client })
      expect(testGlobal.fetch.mock.calls[0][0]).toBe('http://100.64.0.2:7860/api/team/approvals/a%2Fb%20c/decide')
    })

    it('a 409 already_decided is surfaced as a typed error carrying the closed approval', async () => {
      const closed = approval({ state: 'denied', decided_by: { kind: 'app', label: 'Purdex.app @ air26', addr: '100.64.0.4:51234' }, decided_at: 3_000 })
      testGlobal.fetch.mockResolvedValueOnce(json({ error: 'already_decided', detail: 'closed at 3000', approval: closed }, 409))
      const err = await rejection(decideApproval(hostId, 'req-1', { decision: 'approve', client }))
      expect(err.status).toBe(409)
      expect(err.code).toBe('already_decided')
      expect(err.detail).toBe('closed at 3000')
      expect(err.approval?.state).toBe('denied')
      expect(err.approval?.decided_by?.label).toBe('Purdex.app @ air26')
    })

    it('a JSON 404 not_found keeps the daemon code; a plain-text 404 (old daemon, no route) is `unsupported`', async () => {
      testGlobal.fetch.mockResolvedValueOnce(json({ error: 'not_found' }, 404))
      const a = await rejection(decideApproval(hostId, 'gone', { decision: 'deny', client }))
      expect(a.code).toBe('not_found')
      expect(a.approval).toBeNull()
      testGlobal.fetch.mockResolvedValueOnce(new Response('404 page not found\n', { status: 404 }))
      const b = await rejection(decideApproval(hostId, 'gone', { decision: 'deny', client }))
      expect(b.status).toBe(404)
      expect(b.code).toBe('unsupported')
    })

    it('a non-JSON 5xx falls back to http_<status>', async () => {
      testGlobal.fetch.mockResolvedValueOnce(new Response('<html>boom</html>', { status: 502 }))
      const err = await rejection(decideApproval(hostId, 'req-1', { decision: 'deny', client }))
      expect(err.code).toBe('http_502')
    })

    it('a fetch rejection (socket refused mid-restart) is code `network`, status 0', async () => {
      testGlobal.fetch.mockRejectedValueOnce(new TypeError('Failed to fetch'))
      const err = await rejection(decideApproval(hostId, 'req-1', { decision: 'deny', client }))
      expect(err.status).toBe(0)
      expect(err.code).toBe('network')
      expect(err.detail).toBe('Failed to fetch')
    })

    it('an unconfigured host is refused before any request (`host_removed`)', async () => {
      const err = await rejection(decideApproval('no-such-host', 'req-1', { decision: 'deny', client }))
      expect(err.code).toBe('host_removed')
      expect(testGlobal.fetch).not.toHaveBeenCalled()
    })

    // The pre-check passes, then the host is removed before the transport fails: `network` would make the dialog
    // queue a decision for a daemon this device no longer has. Removing the host inside the stubbed fetch lands
    // after `pinnedHostFetch` has resolved the base URL, so only the catch can see it.
    it('maps a host removed between the check and the fetch to host_removed', async () => {
      testGlobal.fetch.mockImplementationOnce(() => {
        useHostStore.getState().removeHost(hostId)
        return Promise.reject(new TypeError('Failed to fetch'))
      })
      const err = await rejection(decideApproval(hostId, 'req-1', { decision: 'deny', client }))
      expect(testGlobal.fetch).toHaveBeenCalledTimes(1)
      expect(err.status).toBe(0)
      expect(err.code).toBe('host_removed')
      expect(err.code).not.toBe('network')
    })
  })

  describe('listOpenApprovals', () => {
    it('GETs /api/team/approvals?state=open and returns the list', async () => {
      testGlobal.fetch.mockResolvedValueOnce(json({ approvals: [approval(), approval({ id: 'req-2' })] }))
      const list = await listOpenApprovals(hostId)
      const [url, init] = testGlobal.fetch.mock.calls[0]
      expect(url).toBe('http://100.64.0.2:7860/api/team/approvals?state=open')
      expect(init.method).toBe('GET')
      expect(list.map((a) => a.id)).toEqual(['req-1', 'req-2'])
    })

    it('a null or missing `approvals` is an empty list', async () => {
      testGlobal.fetch.mockResolvedValueOnce(json({ approvals: null }))
      expect(await listOpenApprovals(hostId)).toEqual([])
      testGlobal.fetch.mockResolvedValueOnce(json({}))
      expect(await listOpenApprovals(hostId)).toEqual([])
    })
  })

  // GET /api/team/inflight (spec §9.5) feeds the restart confirm, which has a 3 s budget and falls back to its
  // own store on any rejection — so the call must bound itself and must reject (never hang) past the budget.
  describe('fetchInflight', () => {
    it('GETs /api/team/inflight with the Bearer token and an abort signal, and returns both counts', async () => {
      testGlobal.fetch.mockResolvedValueOnce(json({ approvals_open: 2, relays_active: 0 }))
      expect(await fetchInflight(hostId)).toEqual({ approvals_open: 2, relays_active: 0 })
      const [url, init] = testGlobal.fetch.mock.calls[0]
      expect(url).toBe('http://100.64.0.2:7860/api/team/inflight')
      expect(init.method).toBe('GET')
      expect(new Headers(init.headers).get('Authorization')).toBe('Bearer tok-1')
      expect(init.signal).toBeInstanceOf(AbortSignal)
    })

    it('a missing or malformed count reads as 0', async () => {
      testGlobal.fetch.mockResolvedValueOnce(json({ approvals_open: 'two' }))
      expect(await fetchInflight(hostId)).toEqual({ approvals_open: 0, relays_active: 0 })
    })

    it('an older daemon (plain-text 404) is `unsupported`', async () => {
      testGlobal.fetch.mockResolvedValueOnce(new Response('404 page not found\n', { status: 404 }))
      expect((await rejection(fetchInflight(hostId))).code).toBe('unsupported')
    })

    it('gives up after its budget: the fetch is aborted and the rejection is code `network`', async () => {
      vi.useFakeTimers()
      try {
        testGlobal.fetch.mockImplementationOnce((_url: string, init: RequestInit) => new Promise<Response>((_resolve, reject) => {
          init.signal?.addEventListener('abort', () => reject(new DOMException('The operation was aborted.', 'AbortError')))
        }))
        const settled = rejection(fetchInflight(hostId, 50))
        await vi.advanceTimersByTimeAsync(50)
        expect((await settled).code).toBe('network')
      } finally {
        vi.useRealTimers()
      }
    })
  })

  describe('selfRelayPayloadOf', () => {
    it('reads the self_relay payload; model / effort only when non-empty strings', () => {
      expect(selfRelayPayloadOf(approval({ kind: 'self_relay', payload: { op_id: 'op', used_percentage: 72.4, window: 200000, model_id: 'm', effort: 'low' } })))
        .toEqual({ op_id: 'op', used_percentage: 72.4, window: 200000, model_id: 'm', effort: 'low' })
      expect(selfRelayPayloadOf(approval({ kind: 'self_relay', payload: { op_id: 'op', used_percentage: 72.4, window: 200000, model_id: '', effort: 7 } })))
        .toEqual({ op_id: 'op', used_percentage: 72.4, window: 200000 })
    })
    it('defends against a malformed payload: non-finite numbers read 0, non-strings read empty, window truncated', () => {
      expect(selfRelayPayloadOf(approval({ kind: 'self_relay', payload: { op_id: 5, used_percentage: Number.NaN, window: 1.9 } })))
        .toEqual({ op_id: '', used_percentage: 0, window: 1 })
      expect(selfRelayPayloadOf(approval({ kind: 'self_relay', payload: { used_percentage: Infinity, window: '200000' } })))
        .toEqual({ op_id: '', used_percentage: 0, window: 0 })
      expect(selfRelayPayloadOf(approval({ kind: 'self_relay', payload: 'garbage' })))
        .toEqual({ op_id: '', used_percentage: 0, window: 0 })
    })
  })

  describe('leadPayloadOf', () => {
    it('reads the lead payload and normalises it like the daemon does (0 → 3, cap 8, roots default [cwd])', () => {
      expect(leadPayloadOf(approval())).toEqual({ reason: '要平行跑三個 PR', max_members: 3, roots: ['/w/purdex'] })
      expect(leadPayloadOf(approval({ payload: { reason: 'r', max_members: 0, roots: [] } }))).toEqual({ reason: 'r', max_members: 3, roots: ['/w/purdex'] })
      expect(leadPayloadOf(approval({ payload: { reason: 'r', max_members: 99, roots: ['/a', 7, ''] } }))).toEqual({ reason: 'r', max_members: 8, roots: ['/a'] })
      expect(leadPayloadOf(approval({ payload: 'garbage' }))).toEqual({ reason: '', max_members: 3, roots: ['/w/purdex'] })
    })

    it('team_name is set only when the payload has a string one ("" counts); otherwise the key is absent', () => {
      const of = (payload: unknown) => leadPayloadOf(approval({ payload }))
      expect(of({ reason: 'r', team_name: '驗收 team' }).team_name).toBe('驗收 team')
      expect(of({ reason: 'r', team_name: '' }).team_name).toBe('')
      for (const payload of [{ reason: 'r' }, { reason: 'r', team_name: null }, { reason: 'r', team_name: 7 }, 'garbage']) {
        expect(Object.hasOwn(of(payload), 'team_name')).toBe(false)
      }
    })
  })
})
