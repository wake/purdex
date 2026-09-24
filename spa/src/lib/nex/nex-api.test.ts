// spa/src/lib/nex/nex-api.test.ts
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { useHostStore } from '../../stores/useHostStore'
import {
  nexFetch, fetchNexCapabilities, listExecutions, fetchExecutionEvents,
  attachObserve, attachControl, sendMessage, releaseLease, archiveExecution, resolveExecutionHostId,
  getExecution, fetchNexHost, renewLease, interruptExecution, terminateExecution,
  delegateExecution, pinnedLeaseRelease,
} from './nex-api'
import { NexApiError } from './types'
import { NEX_CLIENT_ID_RE } from './client-id'

const testGlobal = globalThis as typeof globalThis & { fetch: ReturnType<typeof vi.fn> }

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

describe('nex-api', () => {
  // reset() seeds a default token-less host; add ours explicitly and keep its
  // id — never search hostOrder by name, the default is also called "mlab".
  let hostId: string

  beforeEach(() => {
    useHostStore.getState().reset()
    hostId = useHostStore.getState().addHost({ id: 'host-mlab', name: 'mlab', ip: '100.64.0.2', port: 7860, token: 'tok-1' })
    vi.stubGlobal('fetch', vi.fn())
  })
  afterEach(() => vi.unstubAllGlobals())

  it('nexFetch prefixes /api/nex, sends Bearer and X-Pdx-Client, never a ticket', async () => {
    testGlobal.fetch.mockResolvedValueOnce(json({}))
    await nexFetch(hostId, '/v1/capabilities')
    const [url, init] = testGlobal.fetch.mock.calls[0]
    expect(url).toBe('http://100.64.0.2:7860/api/nex/v1/capabilities')
    expect(url).not.toContain('ticket')
    const h = new Headers(init.headers)
    expect(h.get('Authorization')).toBe('Bearer tok-1')
    expect(h.get('X-Pdx-Client')).toMatch(NEX_CLIENT_ID_RE)
  })

  it('fetchNexCapabilities returns the parsed body', async () => {
    testGlobal.fetch.mockResolvedValueOnce(json({ phase: 'P1a', host_id: 'mlab', verbs: ['attach'], lease: { ttl_seconds: 120 } }))
    const caps = await fetchNexCapabilities(hostId)
    expect(caps.phase).toBe('P1a')
    expect(caps.lease.ttl_seconds).toBe(120)
  })

  it('listExecutions builds the query string', async () => {
    testGlobal.fetch.mockResolvedValueOnce(json({ items: [], next_cursor: '' }))
    await listExecutions(hostId, { state: 'running', includeArchived: true, cursor: 'c1', limit: 20 })
    const [url] = testGlobal.fetch.mock.calls[0]
    const u = new URL(url)
    expect(u.pathname).toBe('/api/nex/v1/executions')
    expect(u.searchParams.get('state')).toBe('running')
    expect(u.searchParams.get('include_archived')).toBe('true')
    expect(u.searchParams.get('cursor')).toBe('c1')
    expect(u.searchParams.get('limit')).toBe('20')
  })

  it('fetchExecutionEvents passes after/limit and encodes the id', async () => {
    testGlobal.fetch.mockResolvedValueOnce(json({ items: [], next_cursor: 0 }))
    await fetchExecutionEvents(hostId, 'exc a', { after: 41, limit: 500 })
    const [url] = testGlobal.fetch.mock.calls[0]
    expect(url).toBe('http://100.64.0.2:7860/api/nex/v1/executions/exc%20a/events?after=41&limit=500')
  })

  it('attachObserve / attachControl post the mode as JSON', async () => {
    testGlobal.fetch.mockResolvedValueOnce(json({ mode: 'observe', stream_url: '/api/nex/v1/events?execution_id=exc_1', cursor: 7, state: 'idle' }))
    const obs = await attachObserve(hostId, 'exc_1')
    expect(obs.cursor).toBe(7)
    let [, init] = testGlobal.fetch.mock.calls[0]
    expect(init.method).toBe('POST')
    expect(JSON.parse(init.body)).toEqual({ mode: 'observe' })
    expect(new Headers(init.headers).get('Content-Type')).toBe('application/json')

    testGlobal.fetch.mockResolvedValueOnce(json({ mode: 'control', lease_id: 'ls_1', expires_at: 1 }))
    const ctl = await attachControl(hostId, 'exc_1')
    expect(ctl.lease_id).toBe('ls_1')
    ;[, init] = testGlobal.fetch.mock.calls[1]
    expect(JSON.parse(init.body)).toEqual({ mode: 'control' })
  })

  it('sendMessage posts lease_id + text; releaseLease sends DELETE with lease_id', async () => {
    testGlobal.fetch.mockResolvedValueOnce(json({ turn_id: 'trn_1', delivery: 'queued' }))
    const r = await sendMessage(hostId, 'exc_1', 'ls_1', 'hello')
    expect(r.delivery).toBe('queued')
    let [url, init] = testGlobal.fetch.mock.calls[0]
    expect(url).toBe('http://100.64.0.2:7860/api/nex/v1/executions/exc_1/messages')
    expect(JSON.parse(init.body)).toEqual({ lease_id: 'ls_1', text: 'hello' })

    testGlobal.fetch.mockResolvedValueOnce(new Response(null, { status: 204 }))
    await releaseLease(hostId, 'exc_1', 'ls_1')
    ;[url, init] = testGlobal.fetch.mock.calls[1]
    expect(url).toBe('http://100.64.0.2:7860/api/nex/v1/executions/exc_1/attach')
    expect(init.method).toBe('DELETE')
    expect(JSON.parse(init.body)).toEqual({ lease_id: 'ls_1' })
  })

  it('releaseLease forwards a RequestInit (keepalive for beforeunload)', async () => {
    testGlobal.fetch.mockResolvedValueOnce(new Response(null, { status: 204 }))
    await releaseLease(hostId, 'exc_1', 'ls_1', { keepalive: true })
    const [, init] = testGlobal.fetch.mock.calls.at(-1)!
    expect(init.keepalive).toBe(true)
    expect(init.method).toBe('DELETE')
    expect(JSON.parse(init.body)).toEqual({ lease_id: 'ls_1' })
  })

  it('pinnedLeaseRelease: endpoint and auth read NOW — the release goes there even after the host left the store', async () => {
    const release = pinnedLeaseRelease(hostId)
    expect(release).not.toBeNull()
    expect(testGlobal.fetch).not.toHaveBeenCalled() // pinning sends nothing
    useHostStore.getState().addHost({ id: 'host-other', name: 'other', ip: '10.9.9.9', port: 7860 })
    useHostStore.getState().removeHost(hostId)
    testGlobal.fetch.mockResolvedValueOnce(new Response(null, { status: 204 }))
    await release!('exc_1', 'ls_1')
    const [url, init] = testGlobal.fetch.mock.calls[0]
    expect(url).toBe('http://100.64.0.2:7860/api/nex/v1/executions/exc_1/attach')
    expect(init.method).toBe('DELETE')
    expect(JSON.parse(init.body)).toEqual({ lease_id: 'ls_1' })
    const h = new Headers(init.headers)
    expect(h.get('Authorization')).toBe('Bearer tok-1')
    expect(h.get('X-Pdx-Client')).toMatch(NEX_CLIENT_ID_RE)
  })

  it('pinnedLeaseRelease: an unknown host pins nothing (never the fallback host)', () => {
    expect(pinnedLeaseRelease('nope')).toBeNull()
  })

  it('pinnedLeaseRelease: a failed request is a NexApiError', async () => {
    const release = pinnedLeaseRelease(hostId)!
    testGlobal.fetch.mockRejectedValueOnce(new TypeError('offline'))
    await expect(release('exc_1', 'ls_1')).rejects.toMatchObject({ code: 'network' })
    testGlobal.fetch.mockResolvedValueOnce(json({ error: { code: 'lease_mismatch', message: 'x' } }, 409))
    await expect(release('exc_1', 'ls_1')).rejects.toBeInstanceOf(NexApiError)
  })

  it('archiveExecution posts the target archived flag (api/interact.go:81)', async () => {
    testGlobal.fetch.mockResolvedValueOnce(json({ archived: true }))
    await archiveExecution(hostId, 'exc_1')
    expect(JSON.parse(testGlobal.fetch.mock.calls.at(-1)![1].body)).toEqual({ archived: true })
    testGlobal.fetch.mockResolvedValueOnce(json({ archived: false }))
    await archiveExecution(hostId, 'exc_1', true)
    expect(JSON.parse(testGlobal.fetch.mock.calls.at(-1)![1].body)).toEqual({ archived: false })
  })

  it('nexFetch refuses an unknown/removed host without calling fetch (never falls back to another daemon)', async () => {
    const err = await nexFetch('unknown-host', '/v1/capabilities').catch((e) => e)
    expect(err).toBeInstanceOf(NexApiError)
    expect(err).toMatchObject({ code: 'host_removed', status: 0 })
    expect(testGlobal.fetch).not.toHaveBeenCalled()
  })

  it('attachControl on an unknown/removed host rejects host_removed without calling fetch', async () => {
    const err = await attachControl('unknown-host', 'exc_1').catch((e) => e)
    expect(err).toBeInstanceOf(NexApiError)
    expect(err).toMatchObject({ code: 'host_removed' })
    expect(testGlobal.fetch).not.toHaveBeenCalled()
  })

  it('wraps a network failure (fetch rejection) as NexApiError(0, "network", message)', async () => {
    testGlobal.fetch.mockRejectedValueOnce(new TypeError('Failed to fetch'))
    const err = await sendMessage(hostId, 'exc_1', 'ls_1', 'hello').catch((e) => e)
    expect(err).toBeInstanceOf(NexApiError)
    expect(err).toMatchObject({ code: 'network', status: 0, message: 'Failed to fetch' })
  })

  it('throws NexApiError with the structured code on non-2xx', async () => {
    testGlobal.fetch.mockResolvedValueOnce(json({ error: 'held', code: 'lease_held' }, 409))
    await expect(attachControl(hostId, 'exc_1')).rejects.toMatchObject({ code: 'lease_held', status: 409 })
    testGlobal.fetch.mockResolvedValueOnce(json({ error: 'gone', code: 'execution_not_found' }, 404))
    const err = await attachObserve(hostId, 'nope').catch((e) => e)
    expect(err).toBeInstanceOf(NexApiError)
    expect(err.code).toBe('execution_not_found')
  })

  it('getExecution GETs the single execution', async () => {
    testGlobal.fetch.mockResolvedValueOnce(json({ id: 'exc_1', state: 'running' }))
    const s = await getExecution(hostId, 'exc_1')
    expect(s.state).toBe('running')
    const [url, init] = testGlobal.fetch.mock.calls[0]
    expect(url).toBe('http://100.64.0.2:7860/api/nex/v1/executions/exc_1')
    expect(init.method ?? 'GET').toBe('GET')
  })

  it('fetchNexHost GETs the host info', async () => {
    testGlobal.fetch.mockResolvedValueOnce(json({ active_account: 'acct-1', quota: null }))
    const h = await fetchNexHost(hostId)
    expect(h.active_account).toBe('acct-1')
    const [url, init] = testGlobal.fetch.mock.calls[0]
    expect(url).toBe('http://100.64.0.2:7860/api/nex/v1/host')
    expect(init.method ?? 'GET').toBe('GET')
  })

  it('renewLease POSTs lease_id to attach/renew and returns the parsed body', async () => {
    testGlobal.fetch.mockResolvedValueOnce(json({ mode: 'control', lease_id: 'ls_1', expires_at: 99 }))
    const r = await renewLease(hostId, 'exc_1', 'ls_1')
    expect(r.expires_at).toBe(99)
    const [url, init] = testGlobal.fetch.mock.calls[0]
    expect(url).toBe('http://100.64.0.2:7860/api/nex/v1/executions/exc_1/attach/renew')
    expect(init.method).toBe('POST')
    expect(JSON.parse(init.body)).toEqual({ lease_id: 'ls_1' })
  })

  it('interruptExecution POSTs lease_id to interrupt and returns the parsed body', async () => {
    testGlobal.fetch.mockResolvedValueOnce(json({ turn_id: 'trn_1', state: 'idle' }))
    const r = await interruptExecution(hostId, 'exc_1', 'ls_1')
    expect(r).toEqual({ turn_id: 'trn_1', state: 'idle' })
    const [url, init] = testGlobal.fetch.mock.calls[0]
    expect(url).toBe('http://100.64.0.2:7860/api/nex/v1/executions/exc_1/interrupt')
    expect(init.method).toBe('POST')
    expect(JSON.parse(init.body)).toEqual({ lease_id: 'ls_1' })
  })

  it('terminateExecution POSTs lease_id to terminate and resolves void', async () => {
    testGlobal.fetch.mockResolvedValueOnce(new Response(null, { status: 204 }))
    await expect(terminateExecution(hostId, 'exc_1', 'ls_1')).resolves.toBeUndefined()
    const [url, init] = testGlobal.fetch.mock.calls[0]
    expect(url).toBe('http://100.64.0.2:7860/api/nex/v1/executions/exc_1/terminate')
    expect(init.method).toBe('POST')
    expect(JSON.parse(init.body)).toEqual({ lease_id: 'ls_1' })
  })

  describe('delegateExecution', () => {
    const supported = { delegate: { resume_session_id: true } }
    const unsupported = { delegate: {} }

    it('maps the minimal request onto the F2 body: provider claude, one writable cwd mount, no optional fields', async () => {
      testGlobal.fetch.mockResolvedValueOnce(json({ id: 'exc_1', state: 'queued' }))
      const r = await delegateExecution(hostId, { brief: 'do it', cwd: '/w/repo' }, unsupported)
      expect(r).toEqual({ id: 'exc_1', state: 'queued' })
      const [url, init] = testGlobal.fetch.mock.calls[0]
      expect(url).toBe('http://100.64.0.2:7860/api/nex/v1/executions')
      expect(init.method).toBe('POST')
      expect(new Headers(init.headers).get('Content-Type')).toBe('application/json')
      expect(JSON.parse(init.body)).toEqual({
        provider: 'claude',
        brief: 'do it',
        mounts: [{ path: '/w/repo', role: 'cwd', writable: true }],
      })
    })

    it('sends sandbox_profile only when profile is given', async () => {
      testGlobal.fetch.mockResolvedValueOnce(json({ id: 'exc_1', state: 'queued' }))
      await delegateExecution(hostId, { brief: 'b', cwd: '/w', profile: 'trusted' }, unsupported)
      expect(JSON.parse(testGlobal.fetch.mock.calls[0][1].body).sandbox_profile).toBe('trusted')

      testGlobal.fetch.mockResolvedValueOnce(json({ id: 'exc_2', state: 'queued' }))
      await delegateExecution(hostId, { brief: 'b', cwd: '/w' }, unsupported)
      expect(JSON.parse(testGlobal.fetch.mock.calls[1][1].body)).not.toHaveProperty('sandbox_profile')
    })

    it('passes labels and origin through verbatim', async () => {
      testGlobal.fetch.mockResolvedValueOnce(json({ id: 'exc_1', state: 'queued' }))
      await delegateExecution(hostId, { brief: 'b', cwd: '/w', labels: { team: 'a', 'x-y': 'z' }, origin: 'purdex/newtab' }, unsupported)
      const body = JSON.parse(testGlobal.fetch.mock.calls[0][1].body)
      expect(body.labels).toEqual({ team: 'a', 'x-y': 'z' })
      expect(body.origin).toBe('purdex/newtab')
    })

    it('puts resume_session_id on the wire when capabilities.delegate.resume_session_id === true', async () => {
      testGlobal.fetch.mockResolvedValueOnce(json({ id: 'exc_1', state: 'queued' }))
      await delegateExecution(hostId, { brief: 'b', cwd: '/w', resume_session_id: 'sid-1' }, supported)
      expect(JSON.parse(testGlobal.fetch.mock.calls[0][1].body).resume_session_id).toBe('sid-1')
    })

    it('rejects resume_unsupported before any fetch when resume_session_id is set but the host does not advertise it', async () => {
      const err = await delegateExecution(hostId, { brief: 'b', cwd: '/w', resume_session_id: 'sid-1' }, unsupported).catch((e) => e)
      expect(err).toBeInstanceOf(NexApiError)
      expect(err).toMatchObject({ status: 0, code: 'resume_unsupported' })
      expect(testGlobal.fetch).not.toHaveBeenCalled()
    })

    it('treats a non-boolean-true advertisement as unsupported (fail-closed)', async () => {
      const err = await delegateExecution(hostId, { brief: 'b', cwd: '/w', resume_session_id: 'sid-1' }, { delegate: { resume_session_id: 'true' as never } }).catch((e) => e)
      expect(err).toMatchObject({ code: 'resume_unsupported' })
      expect(testGlobal.fetch).not.toHaveBeenCalled()
    })

    it('rejects resume_unsupported when capabilities are null (unknown) and resume_session_id is set', async () => {
      const err = await delegateExecution(hostId, { brief: 'b', cwd: '/w', resume_session_id: 'sid-1' }, null).catch((e) => e)
      expect(err).toBeInstanceOf(NexApiError)
      expect(err).toMatchObject({ status: 0, code: 'resume_unsupported' })
      expect(testGlobal.fetch).not.toHaveBeenCalled()
    })

    it('does not require capabilities when no resume_session_id is requested', async () => {
      testGlobal.fetch.mockResolvedValueOnce(json({ id: 'exc_1', state: 'queued' }))
      await expect(delegateExecution(hostId, { brief: 'b', cwd: '/w' }, null)).resolves.toMatchObject({ id: 'exc_1' })
      expect(JSON.parse(testGlobal.fetch.mock.calls[0][1].body)).not.toHaveProperty('resume_session_id')
    })

    it('returns a 200 rejected row as data, not an error', async () => {
      testGlobal.fetch.mockResolvedValueOnce(json({ id: 'exc_1', state: 'rejected', reject_reason: 'cwd_outside_roots' }))
      await expect(delegateExecution(hostId, { brief: 'b', cwd: '/nope' }, unsupported))
        .resolves.toEqual({ id: 'exc_1', state: 'rejected', reject_reason: 'cwd_outside_roots' })
    })

    it('throws NexApiError(400, invalid_brief) from the body code', async () => {
      testGlobal.fetch.mockResolvedValueOnce(json({ error: 'brief too long', code: 'invalid_brief' }, 400))
      const err = await delegateExecution(hostId, { brief: 'b', cwd: '/w' }, unsupported).catch((e) => e)
      expect(err).toBeInstanceOf(NexApiError)
      expect(err).toMatchObject({ status: 400, code: 'invalid_brief', message: 'brief too long' })
    })

    it('throws NexApiError(400, invalid_origin) from the body code', async () => {
      testGlobal.fetch.mockResolvedValueOnce(json({ error: 'origin too long', code: 'invalid_origin' }, 400))
      await expect(delegateExecution(hostId, { brief: 'b', cwd: '/w', origin: 'x' }, unsupported))
        .rejects.toMatchObject({ status: 400, code: 'invalid_origin' })
    })

    it('throws NexApiError(503, nex_unavailable) when pdx has no nex mounted', async () => {
      testGlobal.fetch.mockResolvedValueOnce(json({ error: 'nex not mounted', code: 'nex_unavailable' }, 503))
      await expect(delegateExecution(hostId, { brief: 'b', cwd: '/w' }, unsupported))
        .rejects.toMatchObject({ status: 503, code: 'nex_unavailable' })
    })

    it('rejects host_removed for an unknown host without calling fetch', async () => {
      const err = await delegateExecution('unknown-host', { brief: 'b', cwd: '/w' }, unsupported).catch((e) => e)
      expect(err).toBeInstanceOf(NexApiError)
      expect(err).toMatchObject({ status: 0, code: 'host_removed' })
      expect(testGlobal.fetch).not.toHaveBeenCalled()
    })
  })

  it('resolveExecutionHostId returns a present hint verbatim — even an unknown one — and only falls back when the hint is absent (spec §4.3.2 step 5)', () => {
    expect(resolveExecutionHostId(hostId)).toBe(hostId)
    expect(resolveExecutionHostId('unknown')).toBe('unknown')
    expect(resolveExecutionHostId(undefined)).toBe(useHostStore.getState().hostOrder[0])
  })

  it('resolveExecutionHostId falls back to an empty string when there are no hosts and no hint', () => {
    useHostStore.setState({ hosts: {}, hostOrder: [], activeHostId: null, runtime: {} } as never)
    expect(resolveExecutionHostId(undefined)).toBe('')
  })
})
