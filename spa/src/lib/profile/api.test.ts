import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const hostFetch = vi.fn()
vi.mock('../host-api', () => ({
  hostFetch: (...args: unknown[]) => hostFetch(...args),
}))

import {
  createProfile,
  deleteAttachment,
  deleteProfile,
  deleteSection,
  getProfileSections,
  getSection,
  listProfiles,
  putAttachment,
  putSection,
  renameProfile,
} from './api'
import { useHostStore } from '../../stores/useHostStore'

const HOST = 'host-1'
const PID = 'p_0123456789ab'
const CID = 'c_0123456789ab'
const H1 = 'a'.repeat(64)
const H2 = 'b'.repeat(64)
const FP = 'f'.repeat(64)

function jsonResponse(body: unknown, status = 200, headers: Record<string, string> = {}): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json', ...headers },
  })
}

/** What Go's http.Error writes: text/plain, the message plus a newline. */
function textResponse(text: string, status: number, headers: Record<string, string> = {}): Response {
  return new Response(`${text}\n`, {
    status,
    headers: { 'Content-Type': 'text/plain; charset=utf-8', ...headers },
  })
}

const meta = {
  section: 'settings',
  rev: 3,
  hash: H1,
  fingerprint: FP,
  ordinal: 2,
  writer: CID,
  updatedAt: 2000,
}
const section = { ...meta, payload: { a: 1 } }
const attachment = {
  clientId: CID,
  profileId: PID,
  deviceName: 'mlab',
  attachedAt: 1000,
  lastSeen: 2000,
}
const profile = { id: PID, name: 'Work', createdAt: 1000, updatedAt: 2000 }
const entry = { ...profile, sections: [meta], attachments: [attachment] }

const putBody = {
  clientId: CID,
  baseRev: 3,
  hash: H2,
  fingerprint: FP,
  ordinal: 2,
  payload: { a: 2 },
}

interface Call {
  hostId: string
  path: string
  init: RequestInit | undefined
}

function lastCall(): Call {
  const [hostId, path, init] = hostFetch.mock.calls[hostFetch.mock.calls.length - 1] as [
    string,
    string,
    RequestInit | undefined,
  ]
  return { hostId, path, init }
}

function contentType(init: RequestInit | undefined): string | null {
  return new Headers(init?.headers).get('Content-Type')
}

const hostConfig = (id: string) => ({ id, name: id, ip: '100.64.0.9', port: 7860, order: 0 })

beforeEach(() => {
  hostFetch.mockReset()
  // The real host store: api.ts refuses to send to a host that is not in it.
  useHostStore.setState({ hosts: { [HOST]: hostConfig(HOST) }, hostOrder: [HOST], activeHostId: HOST })
})

afterEach(() => {
  vi.useRealTimers()
  useHostStore.setState({ hosts: {}, hostOrder: [], activeHostId: null })
})

/* ─── an unknown host is never sent to ─── */

describe('unknown host', () => {
  const GONE = 'host-gone'
  const calls: Array<[string, (hostId: string) => Promise<unknown>]> = [
    ['listProfiles', (h) => listProfiles(h)],
    ['createProfile', (h) => createProfile(h, 'Work')],
    ['renameProfile', (h) => renameProfile(h, PID, 'Home')],
    ['deleteProfile', (h) => deleteProfile(h, PID)],
    ['getProfileSections', (h) => getProfileSections(h, PID)],
    ['getSection', (h) => getSection(h, PID, 'settings')],
    ['putSection', (h) => putSection(h, PID, 'settings', putBody)],
    ['deleteSection', (h) => deleteSection(h, PID, 'settings', { baseRev: 3, clientId: CID })],
    ['putAttachment', (h) => putAttachment(h, PID, { clientId: CID, deviceName: 'mlab' })],
    ['deleteAttachment', (h) => deleteAttachment(h, PID, CID)],
  ]

  it.each(calls)('%s: failed/unknown-host, nothing sent (empty store)', async (_name, call) => {
    useHostStore.setState({ hosts: {}, hostOrder: [], activeHostId: null })
    hostFetch.mockResolvedValue(jsonResponse({}))
    const out = await call(GONE)
    expect(out).toMatchObject({ kind: 'failed', reason: 'unknown-host', status: 0 })
    expect((out as { message: string }).message).toContain(GONE)
    expect(hostFetch).not.toHaveBeenCalled()
  })

  // The case getDaemonBase's fallback would misroute: another host exists and is active.
  it.each(calls)('%s: nothing sent when another host is the active one', async (_name, call) => {
    expect(useHostStore.getState().activeHostId).toBe(HOST)
    expect(useHostStore.getState().getDaemonBase(GONE)).toBe('http://100.64.0.9:7860')
    hostFetch.mockResolvedValue(jsonResponse({}))
    const out = await call(GONE)
    expect(out).toMatchObject({ kind: 'failed', reason: 'unknown-host', status: 0 })
    expect(hostFetch).not.toHaveBeenCalled()
  })

  it('a host removed between two calls stops being sent to', async () => {
    // removeHost keeps the last host, so a second one has to exist.
    useHostStore.setState({
      hosts: { [HOST]: hostConfig(HOST), other: hostConfig('other') },
      hostOrder: [HOST, 'other'],
    })
    hostFetch.mockResolvedValue(jsonResponse({ profiles: [] }))
    expect(await listProfiles(HOST)).toEqual({ kind: 'ok', value: [] })
    useHostStore.getState().removeHost(HOST)
    expect(useHostStore.getState().activeHostId).toBe('other')
    hostFetch.mockClear()
    expect(await listProfiles(HOST)).toMatchObject({ kind: 'failed', reason: 'unknown-host' })
    expect(hostFetch).not.toHaveBeenCalled()
  })
})

/* ─── the endpoint pin: the wizard's reads go to the daemon it checked, or nowhere (host-sync-identity §8) ─── */

describe('expectEndpoint on the reads the wizard makes (listProfiles, getSection)', () => {
  const AT = '100.64.0.9:7860'
  const reads: Array<[string, (opts: { expectEndpoint: string }) => Promise<unknown>, unknown, unknown]> = [
    ['listProfiles', (opts) => listProfiles(HOST, opts), { profiles: [entry] }, { kind: 'ok', value: [entry] }],
    ['getSection', (opts) => getSection(HOST, PID, 'settings', opts), section, { kind: 'ok', value: section }],
  ]

  it.each(reads)('%s: the host is at the pinned endpoint → sent, answered as usual', async (_name, call, body, expected) => {
    hostFetch.mockResolvedValue(jsonResponse(body))
    expect(await call({ expectEndpoint: AT })).toEqual(expected)
    expect(hostFetch).toHaveBeenCalledTimes(1)
  })

  it.each(reads)('%s: the host is somewhere else → failed/endpoint-changed, nothing sent', async (_name, call, body) => {
    hostFetch.mockResolvedValue(jsonResponse(body))
    expect(await call({ expectEndpoint: '100.64.0.9:7999' })).toMatchObject({ kind: 'failed', reason: 'endpoint-changed', status: 0 })
    expect(hostFetch).not.toHaveBeenCalled()
  })
})

/* ─── a request that cannot be built is a Failure, not a throw ─── */

describe('unbuildable request', () => {
  // A lone surrogate makes encodeURIComponent throw URIError; a BigInt or a
  // cycle makes JSON.stringify throw TypeError. Such values can come out of
  // corrupted persisted state.
  const LONE = 'bad\uD800'
  const BIG = 1n as unknown as string
  const cyclic: Record<string, unknown> = { a: 1 }
  cyclic.self = cyclic

  const calls: Array<[string, () => Promise<unknown>]> = [
    ['createProfile: BigInt name', () => createProfile(HOST, BIG)],
    ['renameProfile: lone-surrogate profileId', () => renameProfile(HOST, LONE, 'Home')],
    ['renameProfile: BigInt name', () => renameProfile(HOST, PID, BIG)],
    ['deleteProfile: lone-surrogate profileId', () => deleteProfile(HOST, LONE)],
    ['getProfileSections: lone-surrogate profileId', () => getProfileSections(HOST, LONE)],
    ['getSection: lone-surrogate profileId', () => getSection(HOST, LONE, 'settings')],
    ['getSection: lone-surrogate section', () => getSection(HOST, PID, LONE)],
    ['putSection: lone-surrogate profileId', () => putSection(HOST, LONE, 'settings', putBody)],
    ['putSection: lone-surrogate section', () => putSection(HOST, PID, LONE, putBody)],
    ['putSection: cyclic payload', () => putSection(HOST, PID, 'settings', { ...putBody, payload: cyclic })],
    ['putSection: BigInt in payload', () => putSection(HOST, PID, 'settings', { ...putBody, payload: { n: 1n } })],
    ['putSection: null body', () => putSection(HOST, PID, 'settings', null as never)],
    ['deleteSection: lone-surrogate profileId', () => deleteSection(HOST, LONE, 'settings', { baseRev: 3, clientId: CID })],
    ['deleteSection: lone-surrogate section', () => deleteSection(HOST, PID, LONE, { baseRev: 3, clientId: CID })],
    ['deleteSection: lone-surrogate clientId', () => deleteSection(HOST, PID, 'settings', { baseRev: 3, clientId: LONE })],
    ['putAttachment: lone-surrogate profileId', () => putAttachment(HOST, LONE, { clientId: CID, deviceName: 'mlab' })],
    ['putAttachment: BigInt deviceName', () => putAttachment(HOST, PID, { clientId: CID, deviceName: BIG })],
    ['deleteAttachment: lone-surrogate profileId', () => deleteAttachment(HOST, LONE, CID)],
    ['deleteAttachment: lone-surrogate clientId', () => deleteAttachment(HOST, PID, LONE)],
  ]

  it.each(calls)('%s → resolves failed/rejected, nothing sent', async (_name, call) => {
    hostFetch.mockResolvedValue(jsonResponse({}))
    let pending: Promise<unknown> | undefined
    // Not even a synchronous throw: the executor awaits the promise, nothing else.
    expect(() => { pending = call() }).not.toThrow()
    const out = await pending
    expect(out).toMatchObject({ kind: 'failed', reason: 'rejected', status: 0 })
    expect((out as { message: string }).message).not.toBe('')
    expect(hostFetch).not.toHaveBeenCalled()
  })

  it('carries the original error message', async () => {
    const out = await deleteProfile(HOST, LONE)
    let expected = ''
    try { encodeURIComponent(LONE) } catch (err) { expected = (err as Error).message }
    expect(expected).not.toBe('')
    expect((out as { message: string }).message).toContain(expected)
  })

  it('an unknown host still wins over an unbuildable request', async () => {
    expect(await deleteProfile('host-gone', LONE)).toMatchObject({ kind: 'failed', reason: 'unknown-host' })
  })
})

/* ─── the request each function sends ─── */

describe('requests', () => {
  it('listProfiles: GET /api/profiles', async () => {
    hostFetch.mockResolvedValue(jsonResponse({ profiles: [] }))
    await listProfiles(HOST)
    const call = lastCall()
    expect(call.hostId).toBe(HOST)
    expect(call.path).toBe('/api/profiles')
    expect(call.init?.method ?? 'GET').toBe('GET')
    expect(call.init?.body).toBeUndefined()
    expect(call.init?.signal).toBeInstanceOf(AbortSignal)
  })

  it('createProfile: POST /api/profiles {name}', async () => {
    hostFetch.mockResolvedValue(jsonResponse(profile))
    await createProfile(HOST, 'Work')
    const call = lastCall()
    expect(call.path).toBe('/api/profiles')
    expect(call.init?.method).toBe('POST')
    expect(contentType(call.init)).toBe('application/json')
    expect(JSON.parse(call.init?.body as string)).toEqual({ name: 'Work' })
  })

  it('renameProfile: PATCH /api/profiles/{id} {name}', async () => {
    hostFetch.mockResolvedValue(jsonResponse({ id: PID, name: 'Home' }))
    await renameProfile(HOST, PID, 'Home')
    const call = lastCall()
    expect(call.path).toBe(`/api/profiles/${PID}`)
    expect(call.init?.method).toBe('PATCH')
    expect(contentType(call.init)).toBe('application/json')
    expect(JSON.parse(call.init?.body as string)).toEqual({ name: 'Home' })
  })

  it('deleteProfile: DELETE /api/profiles/{id}, no body', async () => {
    hostFetch.mockResolvedValue(jsonResponse({ deleted: true }))
    await deleteProfile(HOST, PID)
    const call = lastCall()
    expect(call.path).toBe(`/api/profiles/${PID}`)
    expect(call.init?.method).toBe('DELETE')
    expect(call.init?.body).toBeUndefined()
  })

  it('getProfileSections: GET /api/profiles/{id}', async () => {
    hostFetch.mockResolvedValue(jsonResponse({ sections: {} }))
    await getProfileSections(HOST, PID)
    const call = lastCall()
    expect(call.path).toBe(`/api/profiles/${PID}`)
    expect(call.init?.method ?? 'GET').toBe('GET')
  })

  it('getSection: GET …/sections/{section}', async () => {
    hostFetch.mockResolvedValue(jsonResponse(section))
    await getSection(HOST, PID, 'tabs.ws_1')
    expect(lastCall().path).toBe(`/api/profiles/${PID}/sections/tabs.ws_1`)
    expect(lastCall().init?.method ?? 'GET').toBe('GET')
  })

  it('putSection: PUT with exactly the daemon’s six JSON keys', async () => {
    hostFetch.mockResolvedValue(jsonResponse({ rev: 4, applied: true }))
    await putSection(HOST, PID, 'settings', putBody)
    const call = lastCall()
    expect(call.path).toBe(`/api/profiles/${PID}/sections/settings`)
    expect(call.init?.method).toBe('PUT')
    expect(contentType(call.init)).toBe('application/json')
    const sent = JSON.parse(call.init?.body as string) as Record<string, unknown>
    expect(Object.keys(sent).sort()).toEqual(
      ['baseRev', 'clientId', 'fingerprint', 'hash', 'ordinal', 'payload'],
    )
    expect(sent).toEqual(putBody)
  })

  it('putSection sends baseRev 0 as 0 (create), never drops it', async () => {
    hostFetch.mockResolvedValue(jsonResponse({ rev: 1, applied: true }))
    await putSection(HOST, PID, 'settings', { ...putBody, baseRev: 0 })
    expect(JSON.parse(lastCall().init?.body as string)).toMatchObject({ baseRev: 0 })
  })

  it('deleteSection: DELETE with baseRev and clientId in the query', async () => {
    hostFetch.mockResolvedValue(jsonResponse({ rev: 4 }))
    await deleteSection(HOST, PID, 'settings', { baseRev: 3, clientId: CID })
    const call = lastCall()
    const [path, query] = call.path.split('?')
    expect(path).toBe(`/api/profiles/${PID}/sections/settings`)
    const params = new URLSearchParams(query)
    expect([...params.keys()].sort()).toEqual(['baseRev', 'clientId'])
    expect(params.get('baseRev')).toBe('3')
    expect(params.get('clientId')).toBe(CID)
    expect(call.init?.method).toBe('DELETE')
    expect(call.init?.body).toBeUndefined()
  })

  it('putAttachment: PUT …/attachment {clientId, deviceName}', async () => {
    hostFetch.mockResolvedValue(jsonResponse({ attached: true }))
    await putAttachment(HOST, PID, { clientId: CID, deviceName: 'mlab' })
    const call = lastCall()
    expect(call.path).toBe(`/api/profiles/${PID}/attachment`)
    expect(call.init?.method).toBe('PUT')
    expect(contentType(call.init)).toBe('application/json')
    expect(JSON.parse(call.init?.body as string)).toEqual({ clientId: CID, deviceName: 'mlab' })
  })

  it('deleteAttachment: DELETE …/attachment?clientId=', async () => {
    hostFetch.mockResolvedValue(jsonResponse({ detached: true }))
    await deleteAttachment(HOST, PID, CID)
    const call = lastCall()
    const [path, query] = call.path.split('?')
    expect(path).toBe(`/api/profiles/${PID}/attachment`)
    const params = new URLSearchParams(query)
    expect([...params.keys()]).toEqual(['clientId'])
    expect(params.get('clientId')).toBe(CID)
    expect(call.init?.method).toBe('DELETE')
  })

  it('path segments and query values are percent-encoded', async () => {
    hostFetch.mockResolvedValue(jsonResponse({ rev: 1 }))
    await deleteSection(HOST, 'p/../x', 'a b?c', { baseRev: 1, clientId: 'c&d=e' })
    expect(lastCall().path).toBe(
      '/api/profiles/p%2F..%2Fx/sections/a%20b%3Fc?baseRev=1&clientId=c%26d%3De',
    )
  })
})

/* ─── PUT …/sections/{s}: status → outcome, one row per test ─── */

describe('putSection outcomes', () => {
  const put = () => putSection(HOST, PID, 'settings', putBody)

  it('200 {rev, applied:true} → applied', async () => {
    hostFetch.mockResolvedValue(jsonResponse({ rev: 4, applied: true }))
    expect(await put()).toEqual({ kind: 'applied', rev: 4 })
  })

  it('200 {rev, applied:false} → converged', async () => {
    hostFetch.mockResolvedValue(jsonResponse({ rev: 3, applied: false }))
    expect(await put()).toEqual({ kind: 'converged', rev: 3 })
  })

  it('409 conflict with the SOT side → conflict{rev, hash, payload}', async () => {
    hostFetch.mockResolvedValue(
      jsonResponse({ reason: 'conflict', rev: 5, hash: H1, payload: { z: 9 } }, 409),
    )
    expect(await put()).toEqual({ kind: 'conflict', rev: 5, hash: H1, payload: { z: 9 } })
  })

  it('409 conflict against an absent section ({reason, rev:0}) → hash null, payload null', async () => {
    hostFetch.mockResolvedValue(jsonResponse({ reason: 'conflict', rev: 0 }, 409))
    expect(await put()).toEqual({ kind: 'conflict', rev: 0, hash: null, payload: null })
  })

  it('409 schema → schema{fingerprint, ordinal}', async () => {
    hostFetch.mockResolvedValue(
      jsonResponse({ reason: 'schema', fingerprint: FP, ordinal: 3 }, 409),
    )
    expect(await put()).toEqual({ kind: 'schema', fingerprint: FP, ordinal: 3 })
  })

  it('409 with an unknown reason → failed/malformed', async () => {
    hostFetch.mockResolvedValue(jsonResponse({ reason: 'attached', attachments: [] }, 409))
    expect(await put()).toMatchObject({ kind: 'failed', reason: 'malformed', status: 409 })
  })

  it('409 whose body is plain text → failed/malformed', async () => {
    hostFetch.mockResolvedValue(textResponse('conflict', 409))
    expect(await put()).toMatchObject({ kind: 'failed', reason: 'malformed', status: 409 })
  })

  it.each([
    ['rev a string', { reason: 'conflict', rev: '5', hash: H1, payload: {} }],
    ['rev negative', { reason: 'conflict', rev: -1 }],
    ['rev missing', { reason: 'conflict' }],
    ['hash not 64 hex', { reason: 'conflict', rev: 5, hash: 'abc', payload: {} }],
    ['hash uppercase', { reason: 'conflict', rev: 5, hash: 'A'.repeat(64), payload: {} }],
    ['payload an array', { reason: 'conflict', rev: 5, hash: H1, payload: [] }],
    ['hash without payload', { reason: 'conflict', rev: 5, hash: H1 }],
    ['payload without hash', { reason: 'conflict', rev: 5, payload: {} }],
    ['schema fingerprint bad', { reason: 'schema', fingerprint: 'nope', ordinal: 2 }],
    ['schema ordinal 0', { reason: 'schema', fingerprint: FP, ordinal: 0 }],
    ['schema ordinal float', { reason: 'schema', fingerprint: FP, ordinal: 1.5 }],
    ['body null', null],
  ])('409 %s → failed/malformed', async (_label, body) => {
    hostFetch.mockResolvedValue(jsonResponse(body, 409))
    expect(await put()).toMatchObject({ kind: 'failed', reason: 'malformed', status: 409 })
  })

  it.each([
    ['a string', '4'],
    ['negative', -1],
    ['a float', 1.5],
    ['unsafe', 2 ** 53],
    ['missing', undefined],
  ])('200 with rev %s → failed/malformed, status kept', async (_label, rev) => {
    hostFetch.mockResolvedValue(jsonResponse({ rev, applied: true }))
    expect(await put()).toMatchObject({ kind: 'failed', reason: 'malformed', status: 200 })
  })

  it.each([
    ['missing', { rev: 4 }],
    ['a string', { rev: 4, applied: 'true' }],
    ['a number', { rev: 4, applied: 1 }],
  ])('200 with applied %s → failed/malformed', async (_label, body) => {
    hostFetch.mockResolvedValue(jsonResponse(body))
    expect(await put()).toMatchObject({ kind: 'failed', reason: 'malformed', status: 200 })
  })

  it('200 that is not JSON → failed/malformed', async () => {
    hostFetch.mockResolvedValue(textResponse('<html>', 200))
    expect(await put()).toMatchObject({ kind: 'failed', reason: 'malformed', status: 200 })
  })

  it('400 text/plain → failed/rejected with the daemon’s message', async () => {
    hostFetch.mockResolvedValue(textResponse('baseRev is required', 400))
    expect(await put()).toEqual({
      kind: 'failed',
      reason: 'rejected',
      status: 400,
      message: 'baseRev is required',
    })
  })

  it('401 → failed/unauthorized', async () => {
    hostFetch.mockResolvedValue(textResponse('unauthorized', 401))
    expect(await put()).toMatchObject({ kind: 'failed', reason: 'unauthorized', status: 401 })
  })

  it('403 → failed/unauthorized', async () => {
    hostFetch.mockResolvedValue(textResponse('forbidden', 403))
    expect(await put()).toMatchObject({ kind: 'failed', reason: 'unauthorized', status: 403 })
  })

  it('404 → failed/not-found', async () => {
    hostFetch.mockResolvedValue(textResponse('profile not found', 404))
    expect(await put()).toMatchObject({
      kind: 'failed',
      reason: 'not-found',
      status: 404,
      message: 'profile not found',
    })
  })

  it('413 → failed/too-large', async () => {
    hostFetch.mockResolvedValue(textResponse('payload exceeds 5 MiB', 413))
    expect(await put()).toMatchObject({ kind: 'failed', reason: 'too-large', status: 413 })
  })

  it('503 + Retry-After: 1 → failed/contended, retryAfterMs 1000', async () => {
    hostFetch.mockResolvedValue(textResponse('section contended', 503, { 'Retry-After': '1' }))
    expect(await put()).toEqual({
      kind: 'failed',
      reason: 'contended',
      status: 503,
      message: 'section contended',
      retryAfterMs: 1000,
    })
  })

  it('503 Retry-After: 7 → retryAfterMs 7000 (seconds → ms)', async () => {
    hostFetch.mockResolvedValue(textResponse('busy', 503, { 'Retry-After': '7' }))
    expect(await put()).toMatchObject({ reason: 'contended', retryAfterMs: 7000 })
  })

  it.each([
    ['absent', undefined],
    ['not a number', 'abc'],
    ['negative', '-3'],
    ['an HTTP date', 'Wed, 21 Oct 2026 07:28:00 GMT'],
    ['empty', ''],
  ])('503 Retry-After %s → retryAfterMs 1000', async (_label, value) => {
    const headers: Record<string, string> = value === undefined ? {} : { 'Retry-After': value }
    hostFetch.mockResolvedValue(textResponse('busy', 503, headers))
    expect(await put()).toMatchObject({ reason: 'contended', retryAfterMs: 1000 })
  })

  it('500 → failed/server, no retryAfterMs', async () => {
    hostFetch.mockResolvedValue(textResponse('internal error', 500))
    const out = await put()
    expect(out).toEqual({
      kind: 'failed',
      reason: 'server',
      status: 500,
      message: 'internal error',
    })
  })

  it('502 → failed/server', async () => {
    hostFetch.mockResolvedValue(textResponse('bad gateway', 502))
    expect(await put()).toMatchObject({ kind: 'failed', reason: 'server', status: 502 })
  })

  it('an error with an empty body falls back to the status line', async () => {
    hostFetch.mockResolvedValue(new Response('', { status: 500, statusText: 'Internal Server Error' }))
    expect(await put()).toMatchObject({ reason: 'server', message: '500 Internal Server Error' })
  })

  it('fetch rejecting (TypeError) → failed/network, status 0', async () => {
    hostFetch.mockRejectedValue(new TypeError('Failed to fetch'))
    expect(await put()).toEqual({
      kind: 'failed',
      reason: 'network',
      status: 0,
      message: 'Failed to fetch',
    })
  })

  it('hostFetch throwing synchronously → failed/network, never a rejection', async () => {
    hostFetch.mockImplementation(() => {
      throw new Error('no such host')
    })
    await expect(put()).resolves.toEqual({
      kind: 'failed',
      reason: 'network',
      status: 0,
      message: 'no such host',
    })
  })

  it('a body that fails mid-read → failed/network with the real status', async () => {
    const res = jsonResponse({ rev: 4, applied: true })
    vi.spyOn(res, 'text').mockRejectedValue(new TypeError('network error'))
    hostFetch.mockResolvedValue(res)
    expect(await put()).toMatchObject({ kind: 'failed', reason: 'network', status: 200 })
  })
})

/* ─── DELETE …/sections/{s} ─── */

describe('deleteSection outcomes', () => {
  const del = () => deleteSection(HOST, PID, 'settings', { baseRev: 3, clientId: CID })

  it('200 {rev} (no `applied` on this route) → applied', async () => {
    hostFetch.mockResolvedValue(jsonResponse({ rev: 4 }))
    expect(await del()).toEqual({ kind: 'applied', rev: 4 })
  })

  it('200 with a bad rev → failed/malformed', async () => {
    hostFetch.mockResolvedValue(jsonResponse({ rev: '4' }))
    expect(await del()).toMatchObject({ kind: 'failed', reason: 'malformed', status: 200 })
  })

  it('409 conflict → conflict', async () => {
    hostFetch.mockResolvedValue(
      jsonResponse({ reason: 'conflict', rev: 5, hash: H1, payload: { z: 9 } }, 409),
    )
    expect(await del()).toEqual({ kind: 'conflict', rev: 5, hash: H1, payload: { z: 9 } })
  })

  it('409 conflict against absent → hash null, payload null', async () => {
    hostFetch.mockResolvedValue(jsonResponse({ reason: 'conflict', rev: 0 }, 409))
    expect(await del()).toEqual({ kind: 'conflict', rev: 0, hash: null, payload: null })
  })

  it('409 schema (the daemon never sends it for a delete) → failed/malformed', async () => {
    hostFetch.mockResolvedValue(jsonResponse({ reason: 'schema', fingerprint: FP, ordinal: 3 }, 409))
    expect(await del()).toMatchObject({ kind: 'failed', reason: 'malformed', status: 409 })
  })

  it('400 → rejected', async () => {
    hostFetch.mockResolvedValue(textResponse('baseRev must be an integer', 400))
    expect(await del()).toMatchObject({ kind: 'failed', reason: 'rejected', status: 400 })
  })

  it('404 → not-found', async () => {
    hostFetch.mockResolvedValue(textResponse('profile not found', 404))
    expect(await del()).toMatchObject({ kind: 'failed', reason: 'not-found', status: 404 })
  })

  it('503 → contended with retryAfterMs', async () => {
    hostFetch.mockResolvedValue(textResponse('section contended', 503, { 'Retry-After': '1' }))
    expect(await del()).toMatchObject({ kind: 'failed', reason: 'contended', retryAfterMs: 1000 })
  })

  it('500 → server', async () => {
    hostFetch.mockResolvedValue(textResponse('internal error', 500))
    expect(await del()).toMatchObject({ kind: 'failed', reason: 'server', status: 500 })
  })
})

/* ─── GET /api/profiles ─── */

describe('listProfiles outcomes', () => {
  it('200 → ok with every row, sections and attachments', async () => {
    hostFetch.mockResolvedValue(jsonResponse({ profiles: [entry] }))
    expect(await listProfiles(HOST)).toEqual({ kind: 'ok', value: [entry] })
  })

  it('200 {profiles: []} → ok, empty', async () => {
    hostFetch.mockResolvedValue(jsonResponse({ profiles: [] }))
    expect(await listProfiles(HOST)).toEqual({ kind: 'ok', value: [] })
  })

  it('unknown extra fields are dropped, not passed through', async () => {
    hostFetch.mockResolvedValue(jsonResponse({ profiles: [{ ...entry, extra: 1 }] }))
    expect(await listProfiles(HOST)).toEqual({ kind: 'ok', value: [entry] })
  })

  it('one bad row makes the whole list malformed — never a shorter list', async () => {
    const { name: _name, ...noName } = entry
    hostFetch.mockResolvedValue(jsonResponse({ profiles: [entry, noName] }))
    expect(await listProfiles(HOST)).toMatchObject({
      kind: 'failed',
      reason: 'malformed',
      status: 200,
    })
  })

  it.each([
    ['profiles missing', {}],
    ['profiles null', { profiles: null }],
    ['a row that is null', { profiles: [null] }],
    ['sections null', { profiles: [{ ...entry, sections: null }] }],
    ['attachments missing', { profiles: [{ ...profile, sections: [] }] }],
    ['a section meta without fingerprint', { profiles: [{ ...entry, sections: [{ ...meta, fingerprint: undefined }] }] }],
    ['a section meta with a bad hash', { profiles: [{ ...entry, sections: [{ ...meta, hash: 'x' }] }] }],
    ['a section meta with ordinal 0', { profiles: [{ ...entry, sections: [{ ...meta, ordinal: 0 }] }] }],
    ['a section meta with a string rev', { profiles: [{ ...entry, sections: [{ ...meta, rev: '3' }] }] }],
    ['a section meta without writer', { profiles: [{ ...entry, sections: [{ ...meta, writer: undefined }] }] }],
    ['a section meta without updatedAt', { profiles: [{ ...entry, sections: [{ ...meta, updatedAt: undefined }] }] }],
    ['an attachment without lastSeen', { profiles: [{ ...entry, attachments: [{ ...attachment, lastSeen: undefined }] }] }],
    ['createdAt a string', { profiles: [{ ...entry, createdAt: '1000' }] }],
    ['the body an array', [entry]],
  ])('200 with %s → failed/malformed', async (_label, body) => {
    hostFetch.mockResolvedValue(jsonResponse(body))
    expect(await listProfiles(HOST)).toMatchObject({ kind: 'failed', reason: 'malformed', status: 200 })
  })

  it('500 → failed/server (a failed list is not an empty list)', async () => {
    hostFetch.mockResolvedValue(textResponse('internal error', 500))
    expect(await listProfiles(HOST)).toEqual({
      kind: 'failed',
      reason: 'server',
      status: 500,
      message: 'internal error',
    })
  })

  it('401 → failed/unauthorized', async () => {
    hostFetch.mockResolvedValue(textResponse('unauthorized', 401))
    expect(await listProfiles(HOST)).toMatchObject({ kind: 'failed', reason: 'unauthorized' })
  })

  it('network failure → failed/network', async () => {
    hostFetch.mockRejectedValue(new TypeError('Failed to fetch'))
    expect(await listProfiles(HOST)).toMatchObject({ kind: 'failed', reason: 'network', status: 0 })
  })

  it('an unexpected 409 → failed/malformed', async () => {
    hostFetch.mockResolvedValue(jsonResponse({ reason: 'conflict', rev: 0 }, 409))
    expect(await listProfiles(HOST)).toMatchObject({ kind: 'failed', reason: 'malformed', status: 409 })
  })
})

/* ─── POST / PATCH / DELETE profile ─── */

describe('createProfile outcomes', () => {
  it('200 → ok with the profile', async () => {
    hostFetch.mockResolvedValue(jsonResponse(profile))
    expect(await createProfile(HOST, 'Work')).toEqual({ kind: 'ok', value: profile })
  })

  it('200 without id → malformed', async () => {
    hostFetch.mockResolvedValue(jsonResponse({ name: 'Work', createdAt: 1, updatedAt: 1 }))
    expect(await createProfile(HOST, 'Work')).toMatchObject({ kind: 'failed', reason: 'malformed' })
  })

  it('400 → rejected', async () => {
    hostFetch.mockResolvedValue(textResponse('name is required', 400))
    expect(await createProfile(HOST, ' ')).toMatchObject({
      kind: 'failed',
      reason: 'rejected',
      message: 'name is required',
    })
  })

  it('413 → too-large', async () => {
    hostFetch.mockResolvedValue(textResponse('body too large', 413))
    expect(await createProfile(HOST, 'x')).toMatchObject({ kind: 'failed', reason: 'too-large' })
  })

  it('500 → server', async () => {
    hostFetch.mockResolvedValue(textResponse('internal error', 500))
    expect(await createProfile(HOST, 'x')).toMatchObject({ kind: 'failed', reason: 'server' })
  })
})

describe('renameProfile outcomes', () => {
  it('200 {id, name} → ok', async () => {
    hostFetch.mockResolvedValue(jsonResponse({ id: PID, name: 'Home' }))
    expect(await renameProfile(HOST, PID, 'Home')).toEqual({
      kind: 'ok',
      value: { id: PID, name: 'Home' },
    })
  })

  it('200 without name → malformed', async () => {
    hostFetch.mockResolvedValue(jsonResponse({ id: PID }))
    expect(await renameProfile(HOST, PID, 'Home')).toMatchObject({ kind: 'failed', reason: 'malformed' })
  })

  it('400 → rejected', async () => {
    hostFetch.mockResolvedValue(textResponse('name too long', 400))
    expect(await renameProfile(HOST, PID, 'x')).toMatchObject({ kind: 'failed', reason: 'rejected' })
  })

  it('404 → not-found', async () => {
    hostFetch.mockResolvedValue(textResponse('profile not found', 404))
    expect(await renameProfile(HOST, PID, 'x')).toMatchObject({ kind: 'failed', reason: 'not-found' })
  })

  it('500 → server', async () => {
    hostFetch.mockResolvedValue(textResponse('internal error', 500))
    expect(await renameProfile(HOST, PID, 'x')).toMatchObject({ kind: 'failed', reason: 'server' })
  })
})

describe('deleteProfile outcomes', () => {
  it('200 {deleted:true} → deleted', async () => {
    hostFetch.mockResolvedValue(jsonResponse({ deleted: true }))
    expect(await deleteProfile(HOST, PID)).toEqual({ kind: 'deleted' })
  })

  it('200 without deleted:true → malformed', async () => {
    hostFetch.mockResolvedValue(jsonResponse({ deleted: false }))
    expect(await deleteProfile(HOST, PID)).toMatchObject({ kind: 'failed', reason: 'malformed' })
  })

  it('409 attached → attached with the attachments', async () => {
    hostFetch.mockResolvedValue(jsonResponse({ reason: 'attached', attachments: [attachment] }, 409))
    expect(await deleteProfile(HOST, PID)).toEqual({ kind: 'attached', attachments: [attachment] })
  })

  it('409 attached with an empty list (they detached in between) → attached, []', async () => {
    hostFetch.mockResolvedValue(jsonResponse({ reason: 'attached', attachments: [] }, 409))
    expect(await deleteProfile(HOST, PID)).toEqual({ kind: 'attached', attachments: [] })
  })

  it('409 attached with a bad attachment → malformed', async () => {
    hostFetch.mockResolvedValue(
      jsonResponse({ reason: 'attached', attachments: [{ clientId: CID }] }, 409),
    )
    expect(await deleteProfile(HOST, PID)).toMatchObject({ kind: 'failed', reason: 'malformed', status: 409 })
  })

  it('409 with another reason → malformed', async () => {
    hostFetch.mockResolvedValue(jsonResponse({ reason: 'conflict', rev: 0 }, 409))
    expect(await deleteProfile(HOST, PID)).toMatchObject({ kind: 'failed', reason: 'malformed', status: 409 })
  })

  it('409 plain text → malformed', async () => {
    hostFetch.mockResolvedValue(textResponse('attached', 409))
    expect(await deleteProfile(HOST, PID)).toMatchObject({ kind: 'failed', reason: 'malformed', status: 409 })
  })

  it('400 → rejected', async () => {
    hostFetch.mockResolvedValue(textResponse('profileId must match p_ + 12 lowercase hex', 400))
    expect(await deleteProfile(HOST, 'x')).toMatchObject({ kind: 'failed', reason: 'rejected' })
  })

  it('404 → not-found', async () => {
    hostFetch.mockResolvedValue(textResponse('profile not found', 404))
    expect(await deleteProfile(HOST, PID)).toMatchObject({ kind: 'failed', reason: 'not-found' })
  })

  it('500 → server', async () => {
    hostFetch.mockResolvedValue(textResponse('internal error', 500))
    expect(await deleteProfile(HOST, PID)).toMatchObject({ kind: 'failed', reason: 'server' })
  })
})

/* ─── GET profile / section ─── */

describe('getProfileSections outcomes', () => {
  it('200 → ok with the sections keyed by name', async () => {
    hostFetch.mockResolvedValue(jsonResponse({ sections: { settings: section } }))
    expect(await getProfileSections(HOST, PID)).toEqual({
      kind: 'ok',
      value: { settings: section },
    })
  })

  it('200 {sections:{}} → ok, empty', async () => {
    hostFetch.mockResolvedValue(jsonResponse({ sections: {} }))
    expect(await getProfileSections(HOST, PID)).toEqual({ kind: 'ok', value: {} })
  })

  it.each([
    ['sections missing', {}],
    ['sections an array', { sections: [] }],
    ['a section without payload', { sections: { settings: meta } }],
    ['a section whose payload is an array', { sections: { settings: { ...section, payload: [] } } }],
    ['a key that is not the section’s own name', { sections: { hosts: section } }],
  ])('200 with %s → malformed', async (_label, body) => {
    hostFetch.mockResolvedValue(jsonResponse(body))
    expect(await getProfileSections(HOST, PID)).toMatchObject({ kind: 'failed', reason: 'malformed', status: 200 })
  })

  it('400 → rejected', async () => {
    hostFetch.mockResolvedValue(textResponse('bad id', 400))
    expect(await getProfileSections(HOST, 'x')).toMatchObject({ kind: 'failed', reason: 'rejected' })
  })

  it('404 → not-found (an unknown profile is NOT an empty one)', async () => {
    hostFetch.mockResolvedValue(textResponse('profile not found', 404))
    expect(await getProfileSections(HOST, PID)).toMatchObject({ kind: 'failed', reason: 'not-found' })
  })

  it('503 → contended (the route answers through writeStoreError)', async () => {
    hostFetch.mockResolvedValue(textResponse('section contended', 503, { 'Retry-After': '1' }))
    expect(await getProfileSections(HOST, PID)).toMatchObject({ kind: 'failed', reason: 'contended', retryAfterMs: 1000 })
  })

  it('500 → server', async () => {
    hostFetch.mockResolvedValue(textResponse('internal error', 500))
    expect(await getProfileSections(HOST, PID)).toMatchObject({ kind: 'failed', reason: 'server' })
  })
})

describe('getSection outcomes', () => {
  it('200 → ok with the section', async () => {
    hostFetch.mockResolvedValue(jsonResponse(section))
    expect(await getSection(HOST, PID, 'settings')).toEqual({ kind: 'ok', value: section })
  })

  it('404 → ok/null (tombstone and unknown profile are indistinguishable)', async () => {
    hostFetch.mockResolvedValue(textResponse('section not found', 404))
    expect(await getSection(HOST, PID, 'settings')).toEqual({ kind: 'ok', value: null })
  })

  it.each([
    ['no payload', meta],
    ['payload null', { ...section, payload: null }],
    ['payload a string', { ...section, payload: 'x' }],
    ['rev negative', { ...section, rev: -1 }],
    ['hash bad', { ...section, hash: 'zz' }],
    ['a different section than the one asked for', { ...section, section: 'hosts' }],
  ])('200 with %s → malformed', async (_label, body) => {
    hostFetch.mockResolvedValue(jsonResponse(body))
    expect(await getSection(HOST, PID, 'settings')).toMatchObject({ kind: 'failed', reason: 'malformed', status: 200 })
  })

  it('400 → rejected', async () => {
    hostFetch.mockResolvedValue(textResponse('section must be …', 400))
    expect(await getSection(HOST, PID, 'nope')).toMatchObject({ kind: 'failed', reason: 'rejected' })
  })

  it('500 → server (not null)', async () => {
    hostFetch.mockResolvedValue(textResponse('internal error', 500))
    expect(await getSection(HOST, PID, 'settings')).toMatchObject({ kind: 'failed', reason: 'server' })
  })

  it('network → failed (not null)', async () => {
    hostFetch.mockRejectedValue(new TypeError('Failed to fetch'))
    expect(await getSection(HOST, PID, 'settings')).toMatchObject({ kind: 'failed', reason: 'network' })
  })
})

/* ─── attachment ─── */

describe('putAttachment outcomes', () => {
  const put = () => putAttachment(HOST, PID, { clientId: CID, deviceName: 'mlab' })

  it('200 {attached:true} → ok', async () => {
    hostFetch.mockResolvedValue(jsonResponse({ attached: true }))
    expect(await put()).toEqual({ kind: 'ok', value: { attached: true } })
  })

  it('200 without attached:true → malformed', async () => {
    hostFetch.mockResolvedValue(jsonResponse({}))
    expect(await put()).toMatchObject({ kind: 'failed', reason: 'malformed' })
  })

  it('400 → rejected', async () => {
    hostFetch.mockResolvedValue(textResponse('deviceName is required', 400))
    expect(await put()).toMatchObject({ kind: 'failed', reason: 'rejected' })
  })

  it('404 → not-found', async () => {
    hostFetch.mockResolvedValue(textResponse('profile not found', 404))
    expect(await put()).toMatchObject({ kind: 'failed', reason: 'not-found' })
  })

  it('413 → too-large', async () => {
    hostFetch.mockResolvedValue(textResponse('body too large', 413))
    expect(await put()).toMatchObject({ kind: 'failed', reason: 'too-large' })
  })

  it('500 → server', async () => {
    hostFetch.mockResolvedValue(textResponse('internal error', 500))
    expect(await put()).toMatchObject({ kind: 'failed', reason: 'server' })
  })
})

describe('deleteAttachment outcomes', () => {
  it('200 {detached:true} → ok', async () => {
    hostFetch.mockResolvedValue(jsonResponse({ detached: true }))
    expect(await deleteAttachment(HOST, PID, CID)).toEqual({ kind: 'ok', value: { detached: true } })
  })

  it('200 {detached:false} (attached elsewhere) → ok, false', async () => {
    hostFetch.mockResolvedValue(jsonResponse({ detached: false }))
    expect(await deleteAttachment(HOST, PID, CID)).toEqual({ kind: 'ok', value: { detached: false } })
  })

  it('200 with detached not a boolean → malformed', async () => {
    hostFetch.mockResolvedValue(jsonResponse({ detached: 'yes' }))
    expect(await deleteAttachment(HOST, PID, CID)).toMatchObject({ kind: 'failed', reason: 'malformed' })
  })

  it('400 → rejected', async () => {
    hostFetch.mockResolvedValue(textResponse('clientId must match …', 400))
    expect(await deleteAttachment(HOST, PID, 'x')).toMatchObject({ kind: 'failed', reason: 'rejected' })
  })

  it('404 → not-found', async () => {
    hostFetch.mockResolvedValue(textResponse('profile not found', 404))
    expect(await deleteAttachment(HOST, PID, CID)).toMatchObject({ kind: 'failed', reason: 'not-found' })
  })

  it('500 → server', async () => {
    hostFetch.mockResolvedValue(textResponse('internal error', 500))
    expect(await deleteAttachment(HOST, PID, CID)).toMatchObject({ kind: 'failed', reason: 'server' })
  })
})

/* ─── timeout and abort ─── */

describe('timeout and abort', () => {
  /** A request that never answers and ignores its signal — the worst case. */
  const hang = () => hostFetch.mockImplementation(() => new Promise<Response>(() => {}))

  it('a hung request → failed/timeout after the default 15 s, and its signal is aborted', async () => {
    vi.useFakeTimers()
    hang()
    const settled = vi.fn()
    const pending = putSection(HOST, PID, 'settings', putBody).then((out) => {
      settled(out)
      return out
    })
    await vi.advanceTimersByTimeAsync(14_999)
    expect(settled).not.toHaveBeenCalled()
    await vi.advanceTimersByTimeAsync(1)
    expect(await pending).toMatchObject({ kind: 'failed', reason: 'timeout', status: 0 })
    expect((lastCall().init?.signal as AbortSignal).aborted).toBe(true)
  })

  it('timeoutMs overrides the default', async () => {
    vi.useFakeTimers()
    hang()
    const pending = listProfiles(HOST, { timeoutMs: 200 })
    await vi.advanceTimersByTimeAsync(200)
    expect(await pending).toMatchObject({ kind: 'failed', reason: 'timeout' })
  })

  it('a request that completes leaves no timer behind', async () => {
    vi.useFakeTimers()
    hostFetch.mockResolvedValue(jsonResponse({ rev: 4, applied: true }))
    expect(await putSection(HOST, PID, 'settings', putBody)).toEqual({ kind: 'applied', rev: 4 })
    expect(vi.getTimerCount()).toBe(0)
  })

  it('a request that fails leaves no timer behind', async () => {
    vi.useFakeTimers()
    hostFetch.mockRejectedValue(new TypeError('Failed to fetch'))
    await putSection(HOST, PID, 'settings', putBody)
    expect(vi.getTimerCount()).toBe(0)
  })

  it('a request that timed out leaves no timer behind', async () => {
    vi.useFakeTimers()
    hang()
    const pending = listProfiles(HOST)
    await vi.advanceTimersByTimeAsync(15_000)
    await pending
    expect(vi.getTimerCount()).toBe(0)
  })

  it('the caller’s signal aborting → failed/aborted, the fetch signal is aborted, no timer left', async () => {
    vi.useFakeTimers()
    hang()
    const controller = new AbortController()
    const pending = putSection(HOST, PID, 'settings', putBody, { signal: controller.signal })
    await vi.advanceTimersByTimeAsync(10)
    controller.abort()
    expect(await pending).toMatchObject({ kind: 'failed', reason: 'aborted', status: 0 })
    expect((lastCall().init?.signal as AbortSignal).aborted).toBe(true)
    expect(vi.getTimerCount()).toBe(0)
  })

  it('a signal that is already aborted → failed/aborted without sending anything', async () => {
    const controller = new AbortController()
    controller.abort()
    expect(await listProfiles(HOST, { signal: controller.signal })).toMatchObject({
      kind: 'failed',
      reason: 'aborted',
    })
    expect(hostFetch).not.toHaveBeenCalled()
  })

  it('a fetch that honours the signal (rejects AbortError) is still reported by cause: timeout', async () => {
    vi.useFakeTimers()
    hostFetch.mockImplementation(
      (_h: string, _p: string, init: RequestInit) =>
        new Promise<Response>((_resolve, reject) => {
          init.signal?.addEventListener('abort', () =>
            reject(new DOMException('The operation was aborted.', 'AbortError')),
          )
        }),
    )
    const pending = listProfiles(HOST)
    await vi.advanceTimersByTimeAsync(15_000)
    expect(await pending).toMatchObject({ kind: 'failed', reason: 'timeout' })
  })

  it('…and aborted when the caller’s signal was the cause', async () => {
    hostFetch.mockImplementation(
      (_h: string, _p: string, init: RequestInit) =>
        new Promise<Response>((_resolve, reject) => {
          init.signal?.addEventListener('abort', () =>
            reject(new DOMException('The operation was aborted.', 'AbortError')),
          )
        }),
    )
    const controller = new AbortController()
    const pending = listProfiles(HOST, { signal: controller.signal })
    controller.abort()
    expect(await pending).toMatchObject({ kind: 'failed', reason: 'aborted' })
  })

  it('an abort after completion changes nothing and does not throw', async () => {
    hostFetch.mockResolvedValue(jsonResponse({ profiles: [] }))
    const controller = new AbortController()
    const out = await listProfiles(HOST, { signal: controller.signal })
    controller.abort()
    expect(out).toEqual({ kind: 'ok', value: [] })
  })
})
