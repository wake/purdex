// spa/src/lib/nex/nex-api.test.ts
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { useHostStore } from '../../stores/useHostStore'
import {
  nexFetch, fetchNexCapabilities, listExecutions, fetchExecutionEvents,
  attachObserve, attachControl, sendMessage, releaseLease, archiveExecution, resolveExecutionHostId,
  getExecution, fetchNexHost, renewLease, interruptExecution, terminateExecution,
  delegateExecution, pinnedLeaseRelease, fetchExecutionTasks, uploadWorkerFile, fetchAttachment,
  fetchExecutionPrelude, answerPermission,
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

  it('listExecutions sends session_id encoded and label.<k> params sorted', async () => {
    testGlobal.fetch.mockResolvedValueOnce(json({ items: [], next_cursor: '' }))
    await listExecutions(hostId, { limit: 5, sessionId: 'a b&c', labels: { zeta: '1', alpha: 'x y' } })
    const [url] = testGlobal.fetch.mock.calls[0]
    expect(url).toBe('http://100.64.0.2:7860/api/nex/v1/executions?limit=5&session_id=a+b%26c&label.alpha=x+y&label.zeta=1')
  })

  it('listExecutions adds nothing for an empty sessionId or empty labels', async () => {
    testGlobal.fetch.mockResolvedValueOnce(json({ items: [], next_cursor: '' }))
    await listExecutions(hostId, { sessionId: '', labels: {} })
    expect(testGlobal.fetch.mock.calls[0][0]).toBe('http://100.64.0.2:7860/api/nex/v1/executions')
  })

  it('fetchExecutionEvents passes after/limit and encodes the id', async () => {
    testGlobal.fetch.mockResolvedValueOnce(json({ items: [], next_cursor: 0 }))
    await fetchExecutionEvents(hostId, 'exc a', { after: 41, limit: 500 })
    const [url] = testGlobal.fetch.mock.calls[0]
    expect(url).toBe('http://100.64.0.2:7860/api/nex/v1/executions/exc%20a/events?after=41&limit=500')
  })

  it('fetchExecutionEvents hands its signal to the fetch, never to the query', async () => {
    testGlobal.fetch.mockResolvedValueOnce(json({ items: [], next_cursor: 0 }))
    const ctl = new AbortController()
    await fetchExecutionEvents(hostId, 'exc_1', { after: 0, limit: 500, signal: ctl.signal })
    const [url, init] = testGlobal.fetch.mock.calls[0]
    expect(url).toBe('http://100.64.0.2:7860/api/nex/v1/executions/exc_1/events?after=0&limit=500')
    expect(init.signal).toBe(ctl.signal)
  })

  it('fetchExecutionPrelude GETs /prelude with before/limit and sanitises the page', async () => {
    testGlobal.fetch.mockResolvedValueOnce(json({ state: 'ok', items: [], prev_cursor: 'c2', total_bytes: 5 }))
    const page = await fetchExecutionPrelude(hostId, 'exc_1', { before: 'c1', limit: 200 })
    expect(testGlobal.fetch.mock.calls[0][0]).toBe('http://100.64.0.2:7860/api/nex/v1/executions/exc_1/prelude?before=c1&limit=200')
    expect(page).toEqual({ state: 'ok', items: [], prevCursor: 'c2', totalBytes: 5 })
  })

  it('fetchExecutionPrelude sends no query for the first page', async () => {
    testGlobal.fetch.mockResolvedValueOnce(json({ state: 'none', items: [], prev_cursor: null }))
    await fetchExecutionPrelude(hostId, 'exc_1')
    expect(testGlobal.fetch.mock.calls[0][0]).toBe('http://100.64.0.2:7860/api/nex/v1/executions/exc_1/prelude')
  })

  it('fetchExecutionPrelude rejects a body that is not a page', async () => {
    testGlobal.fetch.mockResolvedValueOnce(json({ hello: 1 }))
    await expect(fetchExecutionPrelude(hostId, 'exc_1')).rejects.toMatchObject({ code: 'malformed_response' })
  })

  it('fetchExecutionPrelude rejects a 200 with invalid JSON (e.g. HTML proxy page)', async () => {
    testGlobal.fetch.mockResolvedValueOnce(new Response('<html>oops', { status: 200, headers: { 'Content-Type': 'text/html' } }))
    await expect(fetchExecutionPrelude(hostId, 'exc_1')).rejects.toMatchObject({ code: 'malformed_response', status: 0 })
  })

  it('fetchExecutionPrelude rejects a 404 JSON error with the daemon code, not malformed_response', async () => {
    testGlobal.fetch.mockResolvedValueOnce(json({ error: 'not found', code: 'execution_not_found' }, 404))
    await expect(fetchExecutionPrelude(hostId, 'exc_1')).rejects.toMatchObject({ status: 404, code: 'execution_not_found' })
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

  it('sendMessage carries attachments only when non-empty (the key is absent otherwise)', async () => {
    const att = [{ type: 'image' as const, media_type: 'image/png', data: 'iVBORw0KGgo=' }]
    testGlobal.fetch.mockImplementation(async () => json({ turn_id: 'trn_1', delivery: 'delivered' }))
    await sendMessage(hostId, 'exc_1', 'ls_1', '', att)
    await sendMessage(hostId, 'exc_1', 'ls_1', 'hi', [])
    await sendMessage(hostId, 'exc_1', 'ls_1', 'hi')
    const bodies = testGlobal.fetch.mock.calls.map((c: unknown[]) => (c[1] as RequestInit).body as string)
    expect(JSON.parse(bodies[0])).toEqual({ lease_id: 'ls_1', text: '', attachments: att })
    expect(Object.keys(JSON.parse(bodies[1]))).toEqual(['lease_id', 'text'])
    expect(Object.keys(JSON.parse(bodies[2]))).toEqual(['lease_id', 'text'])
    // The key order requestBytes measures.
    expect(Object.keys(JSON.parse(bodies[0]))).toEqual(['lease_id', 'text', 'attachments'])
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

  it('R4 T4.1b: getExecution coerces the rollup fields (and only those) like list rows', async () => {
    testGlobal.fetch.mockResolvedValueOnce(json({
      id: 'exc_1', state: 'running', brief: 7,
      cost_usd: 'x', running_tasks: 1.5, turn_count: -2, last_tool: 'Bash', activity: { phase: 'tool', open_tools: -1 },
    }))
    const s = await getExecution(hostId, 'exc_1')
    expect(s.cost_usd).toBeNull()
    for (const k of ['running_tasks', 'turn_count', 'last_tool', 'activity'] as const) expect(k in s).toBe(false)
    expect(s.state).toBe('running')
    expect(s.brief).toBe(7) // not the rollup: passed through untouched, never rejected
  })

  it('fetchExecutionTasks GETs /tasks?state=running by default, parses items, drops invalid ones', async () => {
    testGlobal.fetch.mockResolvedValueOnce(json({
      items: [
        { task_id: 't1', turn_id: 'u1', kind: 'shell', task_type: 'local_bash', tool_use_id: 'toolu_1', parent_tool_use_id: null, description: 'd', command: 'sleep 8', backgrounded: true, status: 'running', provider_status: null, closed_by: null, ended_at: null, cost_usd: null, started_at: 5 },
        { turn_id: 'no id' },
        'junk',
      ],
      cursor: 128,
    }))
    const res = await fetchExecutionTasks(hostId, 'exc a')
    const [url, init] = testGlobal.fetch.mock.calls[0]
    expect(url).toBe('http://100.64.0.2:7860/api/nex/v1/executions/exc%20a/tasks?state=running')
    expect(init.method ?? 'GET').toBe('GET')
    expect(res.cursor).toBe(128)
    expect(res.items).toHaveLength(1)
    expect(res.items[0]).toMatchObject({ task_id: 't1', status: 'running', command: 'sleep 8', startSeq: 128 })
  })

  it('fetchExecutionTasks passes state=all; a malformed body is an empty snapshot at cursor 0', async () => {
    testGlobal.fetch.mockResolvedValueOnce(json({ items: 'nope', cursor: 'x' }))
    const res = await fetchExecutionTasks(hostId, 'exc_1', 'all')
    const [url] = testGlobal.fetch.mock.calls[0]
    expect(new URL(url).searchParams.get('state')).toBe('all')
    expect(res).toEqual({ items: [], cursor: 0 })
  })

  it('fetchExecutionTasks: a cursor that is not a safe non-negative integer is a malformed body (empty snapshot at 0)', async () => {
    const item = { task_id: 't1', status: 'running', started_at: 5 }
    for (const cursor of [1.5, -1, 2 ** 53, Number.MAX_VALUE, '7', null]) {
      testGlobal.fetch.mockResolvedValueOnce(json({ items: [item], cursor }))
      expect(await fetchExecutionTasks(hostId, 'exc_1')).toEqual({ items: [], cursor: 0 })
    }
    testGlobal.fetch.mockResolvedValueOnce(json({ items: [item], cursor: 0 }))
    const ok = await fetchExecutionTasks(hostId, 'exc_1')
    expect(ok.cursor).toBe(0)
    expect(ok.items).toHaveLength(1)
  })

  it('fetchExecutionTasks throws the structured error (old daemon: 404)', async () => {
    testGlobal.fetch.mockResolvedValueOnce(json({ error: 'not found', code: 'not_found' }, 404))
    await expect(fetchExecutionTasks(hostId, 'exc_1')).rejects.toMatchObject({ status: 404, code: 'not_found' })
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

  describe('fetchAttachment (contract §1.9, consumer-guide §9.5)', () => {
    const SHA = 'ab'.repeat(32)
    const route = { method: 'GET', path: '/api/nex/v1/executions/{id}/attachments/{sha256}' }
    const png = () => new Response(new Uint8Array([0x89, 0x50]), { status: 200, headers: { 'Content-Type': 'image/png' } })

    it('GETs the capability path against the daemon origin, once prefixed, with the host auth, and returns the bytes', async () => {
      testGlobal.fetch.mockResolvedValueOnce(png())
      const blob = await fetchAttachment(hostId, 'exc 1', SHA, route)
      expect(blob.size).toBe(2)
      const [url, init] = testGlobal.fetch.mock.calls[0]
      expect(url).toBe(`http://100.64.0.2:7860/api/nex/v1/executions/exc%201/attachments/${SHA}`)
      expect(url).not.toContain('/api/nex/api/nex')
      expect(init.method).toBe('GET')
      expect(new Headers(init.headers).get('Authorization')).toBe('Bearer tok-1')
    })

    it('reads the template from the capability, not a hard-coded route', async () => {
      testGlobal.fetch.mockResolvedValueOnce(png())
      await fetchAttachment(hostId, 'exc_1', SHA, { method: 'GET', path: '/elsewhere/{sha256}/of/{id}' })
      expect(testGlobal.fetch.mock.calls[0][0]).toBe(`http://100.64.0.2:7860/elsewhere/${SHA}/of/exc_1`)
    })

    it('rejects a 404 with the daemon code', async () => {
      testGlobal.fetch.mockResolvedValueOnce(json({ error: 'no such attachment', code: 'attachment_not_found' }, 404))
      await expect(fetchAttachment(hostId, 'exc_1', SHA, route)).rejects.toMatchObject({ status: 404, code: 'attachment_not_found' })
    })

    it('maps a fetch that never reached the daemon to `network`', async () => {
      testGlobal.fetch.mockRejectedValueOnce(new TypeError('Failed to fetch'))
      await expect(fetchAttachment(hostId, 'exc_1', SHA, route)).rejects.toMatchObject({ code: 'network' })
    })

    it('forwards an optional AbortSignal to the underlying fetch (A1: releases the concurrency slot promptly on abort)', async () => {
      testGlobal.fetch.mockResolvedValueOnce(png())
      const controller = new AbortController()
      await fetchAttachment(hostId, 'exc_1', SHA, route, controller.signal)
      const [, init] = testGlobal.fetch.mock.calls[0]
      expect(init.signal).toBe(controller.signal)
    })

    it('sends nothing for an unknown host, a route that is not origin-relative, or a method other than GET', async () => {
      await expect(fetchAttachment('nope', 'exc_1', SHA, route)).rejects.toMatchObject({ code: 'host_removed' })
      for (const bad of [
        { method: 'GET', path: 'https://evil.example/{sha256}' },
        { method: 'GET', path: '//evil.example/{sha256}' },
        { method: 'GET', path: 'v1/executions/{id}/attachments/{sha256}' },
        { method: 'POST', path: route.path },
      ]) {
        await expect(fetchAttachment(hostId, 'exc_1', SHA, bad)).rejects.toMatchObject({ code: 'attachment_route_invalid' })
      }
      expect(testGlobal.fetch).not.toHaveBeenCalled()
    })
  })

  describe('answerPermission (contract §1.14, consumer-guide §9.8)', () => {
    // The real shape, read from GET /api/nex/v1/capabilities on mlab (alpha.541, Nexen v0.19.0) on 2026-10-07: the path
    // already carries the daemon's RoutePrefix (= Nexen's PublicPrefix), like `lease.renew` and the attachment fetch.
    const MLAB_PERMISSIONS = {
      profiles: ['handoff_ask'],
      answer: { method: 'POST', path: '/api/nex/v1/executions/{id}/permissions/{request_id}' },
      timeout: { max_s: 86400 },
    }
    const caps = { permissions: MLAB_PERMISSIONS }
    const allowed = { request_id: 'req/1', outcome: 'allowed' }

    it('POSTs to the capability path against the daemon origin (the /api/nex prefix once, ids encoded) with auth and client id', async () => {
      testGlobal.fetch.mockResolvedValueOnce(json(allowed))
      const r = await answerPermission(hostId, 'exc 1', 'req/1', { decision: 'allow', leaseId: 'L1' }, caps)
      expect(r).toEqual(allowed)
      expect(testGlobal.fetch).toHaveBeenCalledTimes(1)
      const [url, init] = testGlobal.fetch.mock.calls[0]
      expect(url).toBe('http://100.64.0.2:7860/api/nex/v1/executions/exc%201/permissions/req%2F1')
      expect(url).not.toContain('/api/nex/api/nex')
      expect(init.method).toBe('POST')
      expect(JSON.parse(init.body)).toEqual({ decision: 'allow', lease_id: 'L1' })
      const h = new Headers(init.headers)
      expect(h.get('Authorization')).toBe('Bearer tok-1')
      expect(h.get('X-Pdx-Client')).toMatch(NEX_CLIENT_ID_RE)
      expect(h.get('Content-Type')).toBe('application/json')
    })

    it('reads the template from the capability, not a hard-coded route', async () => {
      testGlobal.fetch.mockResolvedValueOnce(json(allowed))
      await answerPermission(hostId, 'exc_1', 'r1', { decision: 'allow', leaseId: 'L1' }, {
        permissions: { ...MLAB_PERMISSIONS, answer: { method: 'POST', path: '/elsewhere/{request_id}/of/{id}' } },
      })
      expect(testGlobal.fetch.mock.calls[0][0]).toBe('http://100.64.0.2:7860/elsewhere/r1/of/exc_1')
    })

    it('a deny carries its message; the body is exactly {decision, message, lease_id}', async () => {
      testGlobal.fetch.mockResolvedValueOnce(json({ request_id: 'r1', outcome: 'denied' }))
      const r = await answerPermission(hostId, 'exc_1', 'r1', { decision: 'deny', message: '不要動 prod', leaseId: 'L2' }, caps)
      expect(r).toEqual({ request_id: 'r1', outcome: 'denied' })
      expect(JSON.parse(testGlobal.fetch.mock.calls[0][1].body)).toEqual({ decision: 'deny', message: '不要動 prod', lease_id: 'L2' })
    })

    it.each([
      [409, 'permission_not_pending'],
      [404, 'permission_not_found'],
      [400, 'invalid_permission_answer'],
      [409, 'lease_expired'],
      [409, 'lease_mismatch'],
      [409, 'lease_required'],
    ])('a %s %s keeps its status and code', async (status, code) => {
      testGlobal.fetch.mockResolvedValueOnce(json({ error: 'nope', code, field: 'message' }, status))
      await expect(answerPermission(hostId, 'exc_1', 'r1', { decision: 'allow', leaseId: 'L1' }, caps))
        .rejects.toMatchObject({ status, code })
    })

    it('maps a fetch that never reached the daemon to `network`', async () => {
      testGlobal.fetch.mockRejectedValueOnce(new TypeError('Failed to fetch'))
      await expect(answerPermission(hostId, 'exc_1', 'r1', { decision: 'allow', leaseId: 'L1' }, caps)).rejects.toMatchObject({ code: 'network' })
    })

    it('a host without permissions.answer (older build, or capabilities not fetched) is refused locally; nothing is sent', async () => {
      for (const c of [null, undefined, {}, { permissions: undefined }, { permissions: { profiles: ['handoff_ask'] } }]) {
        await expect(answerPermission(hostId, 'exc_1', 'r1', { decision: 'allow', leaseId: 'L1' }, c as never))
          .rejects.toMatchObject({ status: 0, code: 'permission_unsupported' })
      }
      expect(testGlobal.fetch).not.toHaveBeenCalled()
    })

    it('a route that is not an origin-relative POST is refused unsent; so is an unknown host', async () => {
      for (const bad of [
        { method: 'POST', path: 'https://evil.example/{id}/{request_id}' },
        { method: 'POST', path: '//evil.example/{id}/{request_id}' },
        { method: 'POST', path: 'v1/executions/{id}/permissions/{request_id}' },
        { method: 'GET', path: MLAB_PERMISSIONS.answer.path },
        { method: 1, path: MLAB_PERMISSIONS.answer.path },
      ]) {
        await expect(answerPermission(hostId, 'exc_1', 'r1', { decision: 'allow', leaseId: 'L1' }, { permissions: { ...MLAB_PERMISSIONS, answer: bad } } as never))
          .rejects.toMatchObject({ code: 'permission_route_invalid' })
      }
      await expect(answerPermission('nope', 'exc_1', 'r1', { decision: 'allow', leaseId: 'L1' }, caps)).rejects.toMatchObject({ code: 'host_removed' })
      expect(testGlobal.fetch).not.toHaveBeenCalled()
    })
  })

  describe('uploadWorkerFile', () => {
    const file = () => new File(['hello'], 'a b.txt', { type: 'text/plain' })

    it('POSTs multipart `file` to /api/nex/executions/{id}/uploads with Bearer and returns the saved path', async () => {
      testGlobal.fetch.mockResolvedValueOnce(json({ path: '/w/.purdex-uploads/exc 1/a b.txt', name: 'a b.txt', size: 5 }))
      const r = await uploadWorkerFile(hostId, 'exc 1', file())
      expect(r).toEqual({ path: '/w/.purdex-uploads/exc 1/a b.txt', name: 'a b.txt', size: 5 })
      const [url, init] = testGlobal.fetch.mock.calls[0]
      expect(url).toBe('http://100.64.0.2:7860/api/nex/executions/exc%201/uploads')
      expect(init.method).toBe('POST')
      expect(init.body).toBeInstanceOf(FormData)
      expect((init.body as FormData).get('file')).toBeInstanceOf(File)
      const h = new Headers(init.headers)
      expect(h.get('Authorization')).toBe('Bearer tok-1')
      // The browser sets the multipart boundary; a JSON content type would break the body.
      expect(h.get('Content-Type')).toBeNull()
    })

    it('rejects with the daemon error code', async () => {
      testGlobal.fetch.mockResolvedValueOnce(json({ error: 'file exceeds the upload limit', code: 'file_too_large' }, 413))
      await expect(uploadWorkerFile(hostId, 'exc_1', file())).rejects.toMatchObject({ status: 413, code: 'file_too_large' })
    })

    it('maps a fetch that never reached the daemon to `network`', async () => {
      testGlobal.fetch.mockRejectedValueOnce(new TypeError('Failed to fetch'))
      await expect(uploadWorkerFile(hostId, 'exc_1', file())).rejects.toMatchObject({ code: 'network' })
    })

    it('refuses a host this device does not have without sending anything', async () => {
      await expect(uploadWorkerFile('nope', 'exc_1', file())).rejects.toBeInstanceOf(NexApiError)
      expect(testGlobal.fetch).not.toHaveBeenCalled()
    })

    it('preserves `host_removed` when the host disappears between this function\'s own check and pinnedHostFetch\'s', async () => {
      const present = useHostStore.getState()
      let calls = 0
      const spy = vi.spyOn(useHostStore, 'getState').mockImplementation(() => {
        calls += 1
        // uploadWorkerFile's own guard (call 1) still sees the host; every
        // later read — pinnedHostFetch's own check, and this fix's re-check
        // in the catch — sees it gone.
        return calls === 1 ? present : ({ ...present, hosts: {} } as typeof present)
      })
      try {
        const err = await uploadWorkerFile(hostId, 'exc_1', file()).catch((e) => e)
        expect(err).toBeInstanceOf(NexApiError)
        expect(err).toMatchObject({ status: 0, code: 'host_removed' })
        expect(testGlobal.fetch).not.toHaveBeenCalled()
      } finally {
        spy.mockRestore()
      }
    })

    it('rejects a 200 whose body has no path', async () => {
      testGlobal.fetch.mockResolvedValueOnce(json({ name: 'x' }))
      await expect(uploadWorkerFile(hostId, 'exc_1', file())).rejects.toMatchObject({ code: 'bad_response' })
    })
  })
})
