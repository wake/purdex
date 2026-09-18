import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { useHostStore } from '../stores/useHostStore'
import {
  HostApiError, listPeerHosts, verifyPeerHost, updatePeerHost, fetchPeerSettings, fetchHostInfo,
  addPeerHost, deletePeerHost, rotatePeerHost, commitRotation, cancelRotation, updatePeerSettings,
} from './host-api'

const H = 'hx'

function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status, statusText: status === 200 ? 'OK' : 'ERR',
    headers: { 'Content-Type': 'application/json' },
  })
}

const fetchMock = vi.fn<(input: RequestInfo | URL, init?: RequestInit) => Promise<Response>>()

beforeEach(() => {
  useHostStore.setState({
    hosts: { [H]: { id: H, name: 'mlab', ip: '100.64.0.2', port: 7860, order: 0, token: 'adm' } },
    hostOrder: [H],
    runtime: { [H]: { status: 'connected' } },
  })
  fetchMock.mockReset()
  vi.stubGlobal('fetch', fetchMock)
})
afterEach(() => vi.unstubAllGlobals())

const ROW = { alias: 'air', url: 'http://100.64.0.4:7860', host_id: 'wakes-air-2026:oa6drb',
  verified: true, has_token: true, has_inbound_token: true, allow_bypass: true,
  rotation_pending: false, last_inbound_auth: '' as const }

// Live token values in the `pdxp_` + 32 hex form (internal/config/config.go:94)
// so a leak anywhere is greppable (spec D-8).
const TOKEN_NEW = 'pdxp_0123456789abcdef0123456789abcdef'
const TOKEN_OUT = 'pdxp_fedcba9876543210fedcba9876543210'
const BASE = 'http://100.64.0.2:7860'

describe('peer-host wrappers', () => {
  it('listPeerHosts unwraps {hosts} and hits GET /api/peers/hosts with the admin token', async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse(200, { hosts: [ROW] }))
    const rows = await listPeerHosts(H)
    expect(rows).toEqual([ROW])
    const [url, init] = fetchMock.mock.calls[0]
    expect(url).toBe('http://100.64.0.2:7860/api/peers/hosts')
    expect(new Headers(init?.headers).get('Authorization')).toBe('Bearer adm')
    expect(init?.method ?? 'GET').toBe('GET')
  })

  it('listPeerHosts throws HostApiError with the daemon message on a non-2xx', async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse(403, { error: 'admin required' }))
    await expect(listPeerHosts(H)).rejects.toMatchObject({ name: 'HostApiError', status: 403, detail: 'admin required' })
  })

  it('verifyPeerHost POSTs to /api/peers/hosts/<encoded alias>/verify and returns the body verbatim', async () => {
    const body = { alias: 'air', host_id: 'wakes-air-2026:oa6drb', ok: true, self_alias: 'air26', daemon_version: '1.0.0-alpha.378' }
    fetchMock.mockResolvedValueOnce(jsonResponse(200, body))
    await expect(verifyPeerHost(H, 'a b/c')).resolves.toEqual(body)
    const [url, init] = fetchMock.mock.calls[0]
    expect(url).toBe('http://100.64.0.2:7860/api/peers/hosts/a%20b%2Fc/verify')
    expect(init?.method).toBe('POST')
  })

  it('verifyPeerHost 404 (unknown alias) is a HostApiError, not an ok:false', async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse(404, { error: 'unknown alias' }))
    await expect(verifyPeerHost(H, 'ghost')).rejects.toMatchObject({ status: 404, detail: 'unknown alias' })
  })

  it('updatePeerHost PUTs only the fields given and returns the row', async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse(200, { ...ROW, alias: 'air26' }))
    const row = await updatePeerHost(H, 'air', { alias: 'air26' })
    expect(row.alias).toBe('air26')
    const [url, init] = fetchMock.mock.calls[0]
    expect(url).toBe('http://100.64.0.2:7860/api/peers/hosts/air')
    expect(init?.method).toBe('PUT')
    expect(JSON.parse(String(init?.body))).toEqual({ alias: 'air26' })
    expect(new Headers(init?.headers).get('Content-Type')).toBe('application/json')
  })

  it('updatePeerHost surfaces a 409 with the daemon text in detail', async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse(409, { error: 'alias "air26" is already used by another host' }))
    const err = await updatePeerHost(H, 'air', { alias: 'air26' }).catch((e) => e)
    expect(err).toBeInstanceOf(HostApiError)
    expect(err.status).toBe(409)
    expect(err.detail).toBe('alias "air26" is already used by another host')
  })

  it('a non-JSON error body falls back to statusText in detail', async () => {
    fetchMock.mockResolvedValueOnce(new Response('<html>', { status: 502, statusText: 'Bad Gateway' }))
    await expect(fetchPeerSettings(H)).rejects.toMatchObject({ status: 502, detail: 'Bad Gateway' })
  })

  it('a non-JSON error body with an empty statusText (HTTP/2) falls back to "HTTP <status>"', async () => {
    // A pre-D1 daemon answering the verify route with Go's plain-text 404 —
    // under HTTP/2 `Response.statusText` is always ''.
    fetchMock.mockResolvedValueOnce(new Response('404 page not found', { status: 404, statusText: '' }))
    await expect(verifyPeerHost(H, 'air')).rejects.toMatchObject({ status: 404, detail: 'HTTP 404' })
  })

  it('fetchPeerSettings and fetchHostInfo return their bodies', async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse(200, { deliver: true, alias: 'mini-lab' }))
    await expect(fetchPeerSettings(H)).resolves.toEqual({ deliver: true, alias: 'mini-lab' })
    fetchMock.mockResolvedValueOnce(jsonResponse(200, { host_id: 'mini-lab:278cbm', tmux_instance: '1:2', purdex_version: 'x', tmux_version: 'y', os: 'darwin', arch: 'arm64' }))
    await expect(fetchHostInfo(H)).resolves.toMatchObject({ host_id: 'mini-lab:278cbm' })
    expect(fetchMock.mock.calls[0][0]).toBe('http://100.64.0.2:7860/api/peers/settings')
    expect(fetchMock.mock.calls[1][0]).toBe('http://100.64.0.2:7860/api/info')
  })

  it('fetchHostInfo rejects with HostApiError on a non-2xx (the untyped fetchInfo would have resolved)', async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse(500, { error: 'boom' }))
    await expect(fetchHostInfo(H)).rejects.toMatchObject({ name: 'HostApiError', status: 500, detail: 'boom' })
  })

  describe('rotation fields (D3, alpha.391)', () => {
    it('listPeerHosts normalises a pre-391 row (both keys absent) to rotation_pending=false, last_inbound_auth=""', async () => {
      // eslint-disable-next-line @typescript-eslint/no-unused-vars
      const { rotation_pending, last_inbound_auth, ...pre391 } = ROW
      fetchMock.mockResolvedValueOnce(jsonResponse(200, { hosts: [pre391] }))
      const rows = await listPeerHosts(H)
      expect(rows).toHaveLength(1)
      expect(rows[0].rotation_pending).toBe(false)
      expect(rows[0].last_inbound_auth).toBe('')
      expect(rows[0]).toEqual(ROW)
    })

    it('listPeerHosts passes a 391 row {rotation_pending:true, last_inbound_auth:"prev"} through unchanged', async () => {
      const live = { ...ROW, rotation_pending: true, last_inbound_auth: 'prev' }
      fetchMock.mockResolvedValueOnce(jsonResponse(200, { hosts: [live] }))
      const rows = await listPeerHosts(H)
      expect(rows).toEqual([live])
      expect(rows[0].last_inbound_auth).toBe('prev')
    })

    it('listPeerHosts coerces an unknown last_inbound_auth value to "" and a non-boolean rotation_pending to false', async () => {
      const odd = { ...ROW, rotation_pending: 'yes', last_inbound_auth: 'bogus' }
      fetchMock.mockResolvedValueOnce(jsonResponse(200, { hosts: [odd] }))
      const rows = await listPeerHosts(H)
      expect(rows[0].rotation_pending).toBe(false)
      expect(rows[0].last_inbound_auth).toBe('')
    })
  })

  describe('addPeerHost', () => {
    const ADDED = { alias: 'air', url: 'http://100.64.0.4:7860', host_id: 'wakes-air-2026:oa6drb',
      inbound_token: TOKEN_NEW, verified: true }

    it('POSTs /api/peers/hosts as JSON with exactly {url, token} (no alias key) and returns the 201 body verbatim', async () => {
      fetchMock.mockResolvedValueOnce(jsonResponse(201, ADDED))
      await expect(addPeerHost(H, { url: 'http://100.64.0.4:7860', token: TOKEN_OUT })).resolves.toEqual(ADDED)
      const [url, init] = fetchMock.mock.calls[0]
      expect(url).toBe(`${BASE}/api/peers/hosts`)
      expect(init?.method).toBe('POST')
      expect(new Headers(init?.headers).get('Content-Type')).toBe('application/json')
      expect(new Headers(init?.headers).get('Authorization')).toBe('Bearer adm')
      const sent = JSON.parse(String(init?.body))
      expect(sent).toEqual({ url: 'http://100.64.0.4:7860', token: TOKEN_OUT })
      expect(Object.keys(sent).sort()).toEqual(['token', 'url'])
    })

    it('POSTs exactly {alias, url} (no token key) when no token is given', async () => {
      fetchMock.mockResolvedValueOnce(jsonResponse(201, { ...ADDED, verified: false }))
      await addPeerHost(H, { alias: 'air', url: 'http://100.64.0.4:7860' })
      const sent = JSON.parse(String(fetchMock.mock.calls[0][1]?.body))
      expect(sent).toEqual({ alias: 'air', url: 'http://100.64.0.4:7860' })
      expect('token' in sent).toBe(false)
    })

    it('409 (alias taken) rejects with HostApiError{status:409, detail}', async () => {
      const msg = 'alias "mini-lab" is already used by another host; pass an explicit alias for this one'
      fetchMock.mockResolvedValueOnce(jsonResponse(409, { error: msg }))
      const err = await addPeerHost(H, { url: 'http://100.64.0.4:7860' }).catch((e) => e)
      expect(err).toBeInstanceOf(HostApiError)
      expect(err).toMatchObject({ status: 409, detail: msg })
    })

    it('502 (verify failed) rejects with HostApiError{status:502, detail}', async () => {
      fetchMock.mockResolvedValueOnce(jsonResponse(502, { error: 'verify: dial tcp 100.64.0.4:7860: connection refused' }))
      await expect(addPeerHost(H, { url: 'http://100.64.0.4:7860', token: TOKEN_OUT }))
        .rejects.toMatchObject({ name: 'HostApiError', status: 502, detail: 'verify: dial tcp 100.64.0.4:7860: connection refused' })
    })
  })

  describe('deletePeerHost', () => {
    it('sends DELETE /api/peers/hosts/<encoded alias> and resolves undefined on 204', async () => {
      fetchMock.mockResolvedValueOnce(new Response(null, { status: 204 }))
      await expect(deletePeerHost(H, 'a b/c')).resolves.toBeUndefined()
      const [url, init] = fetchMock.mock.calls[0]
      expect(url).toBe(`${BASE}/api/peers/hosts/a%20b%2Fc`)
      expect(init?.method).toBe('DELETE')
      expect(init?.body).toBeUndefined()
    })

    it('404 rejects with HostApiError{status:404, detail:"unknown alias"} — the wrapper does not swallow it', async () => {
      fetchMock.mockResolvedValueOnce(jsonResponse(404, { error: 'unknown alias' }))
      const err = await deletePeerHost(H, 'ghost').catch((e) => e)
      expect(err).toBeInstanceOf(HostApiError)
      expect(err).toMatchObject({ status: 404, detail: 'unknown alias' })
    })
  })

  describe('rotatePeerHost', () => {
    it('POSTs /api/peers/hosts/<alias>/rotate with no body and returns {alias, inbound_token}', async () => {
      fetchMock.mockResolvedValueOnce(jsonResponse(200, { alias: 'air', inbound_token: TOKEN_NEW }))
      await expect(rotatePeerHost(H, 'air')).resolves.toEqual({ alias: 'air', inbound_token: TOKEN_NEW })
      const [url, init] = fetchMock.mock.calls[0]
      expect(url).toBe(`${BASE}/api/peers/hosts/air/rotate`)
      expect(init?.method).toBe('POST')
      expect(init?.body).toBeUndefined()
    })

    it('encodes the alias in the path', async () => {
      fetchMock.mockResolvedValueOnce(jsonResponse(200, { alias: 'a b/c', inbound_token: TOKEN_NEW }))
      await rotatePeerHost(H, 'a b/c')
      expect(fetchMock.mock.calls[0][0]).toBe(`${BASE}/api/peers/hosts/a%20b%2Fc/rotate`)
    })

    it('409 "rotation already pending" surfaces in detail', async () => {
      fetchMock.mockResolvedValueOnce(jsonResponse(409, { error: 'rotation already pending' }))
      await expect(rotatePeerHost(H, 'air')).rejects.toMatchObject({ name: 'HostApiError', status: 409, detail: 'rotation already pending' })
    })

    it('404 (unknown alias) surfaces in detail', async () => {
      fetchMock.mockResolvedValueOnce(jsonResponse(404, { error: 'unknown alias' }))
      await expect(rotatePeerHost(H, 'ghost')).rejects.toMatchObject({ status: 404, detail: 'unknown alias' })
    })
  })

  describe('commitRotation / cancelRotation (spec D-7: the page never sends force)', () => {
    it('commitRotation POSTs …/rotate/commit with NO body and NO Content-Type, returns the row', async () => {
      const after = { ...ROW, rotation_pending: false, last_inbound_auth: '' }
      fetchMock.mockResolvedValueOnce(jsonResponse(200, after))
      await expect(commitRotation(H, 'air')).resolves.toEqual(after)
      const [url, init] = fetchMock.mock.calls[0]
      expect(url).toBe(`${BASE}/api/peers/hosts/air/rotate/commit`)
      expect(init?.method).toBe('POST')
      expect(init?.body).toBeUndefined()
      expect(new Headers(init?.headers).get('Content-Type')).toBeNull()
      expect(new Headers(init?.headers).get('Authorization')).toBe('Bearer adm')
    })

    it('cancelRotation POSTs …/rotate/cancel with NO body and NO Content-Type, returns the row', async () => {
      const after = { ...ROW, rotation_pending: false, last_inbound_auth: 'current' }
      fetchMock.mockResolvedValueOnce(jsonResponse(200, after))
      await expect(cancelRotation(H, 'a b/c')).resolves.toEqual(after)
      const [url, init] = fetchMock.mock.calls[0]
      expect(url).toBe(`${BASE}/api/peers/hosts/a%20b%2Fc/rotate/cancel`)
      expect(init?.method).toBe('POST')
      expect(init?.body).toBeUndefined()
      expect(new Headers(init?.headers).get('Content-Type')).toBeNull()
    })

    it('commitRotation 409 "rotation unconfirmed" surfaces in detail', async () => {
      fetchMock.mockResolvedValueOnce(jsonResponse(409, { error: 'rotation unconfirmed' }))
      await expect(commitRotation(H, 'air')).rejects.toMatchObject({ name: 'HostApiError', status: 409, detail: 'rotation unconfirmed' })
    })

    it('cancelRotation 409 "no rotation pending" / "rotation unconfirmed" surface in detail', async () => {
      fetchMock.mockResolvedValueOnce(jsonResponse(409, { error: 'no rotation pending' }))
      await expect(cancelRotation(H, 'air')).rejects.toMatchObject({ status: 409, detail: 'no rotation pending' })
      fetchMock.mockResolvedValueOnce(jsonResponse(409, { error: 'rotation unconfirmed' }))
      await expect(cancelRotation(H, 'air')).rejects.toMatchObject({ status: 409, detail: 'rotation unconfirmed' })
    })
  })

  describe('updatePeerSettings (self alias, #1196)', () => {
    it('PUTs /api/peers/settings as JSON with exactly {alias} and returns the body verbatim', async () => {
      fetchMock.mockResolvedValueOnce(jsonResponse(200, { deliver: true, alias: 'mlab', alias_source: 'config' }))
      await expect(updatePeerSettings(H, { alias: 'mlab' })).resolves.toEqual({ deliver: true, alias: 'mlab', alias_source: 'config' })
      const [url, init] = fetchMock.mock.calls[0]
      expect(url).toBe(`${BASE}/api/peers/settings`)
      expect(init?.method).toBe('PUT')
      expect(new Headers(init?.headers).get('Content-Type')).toBe('application/json')
      expect(new Headers(init?.headers).get('Authorization')).toBe('Bearer adm')
      // The raw body, not a parsed view of it: no `deliver` key may sneak in (S-2: absent = unchanged).
      expect(String(init?.body)).toBe('{"alias":"mlab"}')
    })

    it('{alias: ""} sends {"alias":""} (S-2: empty clears, absent leaves it)', async () => {
      fetchMock.mockResolvedValueOnce(jsonResponse(200, { deliver: true, alias: 'mini-lab', alias_source: 'host_id' }))
      await expect(updatePeerSettings(H, { alias: '' })).resolves.toMatchObject({ alias: 'mini-lab', alias_source: 'host_id' })
      expect(String(fetchMock.mock.calls[0][1]?.body)).toBe('{"alias":""}')
    })

    it('{deliver: true} alone sends {"deliver":true} and no alias key', async () => {
      fetchMock.mockResolvedValueOnce(jsonResponse(200, { deliver: true, alias: 'mini-lab', alias_source: 'host_id' }))
      await updatePeerSettings(H, { deliver: true })
      expect(String(fetchMock.mock.calls[0][1]?.body)).toBe('{"deliver":true}')
    })

    it('an old daemon\'s 200 without alias_source is returned as is — the wrapper invents nothing (S-5)', async () => {
      fetchMock.mockResolvedValueOnce(jsonResponse(200, { deliver: true, alias: 'mini-lab' }))
      const r = await updatePeerSettings(H, { alias: 'mlab' })
      expect(r).toEqual({ deliver: true, alias: 'mini-lab' })
      expect(r.alias_source).toBeUndefined()
    })

    it('409 (collision with a peer host) rejects with HostApiError{status:409, detail}', async () => {
      fetchMock.mockResolvedValueOnce(jsonResponse(409, { error: 'alias "air26" is already used by a peer host' }))
      const err = await updatePeerSettings(H, { alias: 'air26' }).catch((e) => e)
      expect(err).toBeInstanceOf(HostApiError)
      expect(err).toMatchObject({ status: 409, detail: 'alias "air26" is already used by a peer host' })
    })

    it('400 (pattern / reserved) rejects with the daemon text', async () => {
      fetchMock.mockResolvedValueOnce(jsonResponse(400, { error: 'alias ".." is reserved' }))
      await expect(updatePeerSettings(H, { alias: '..' })).rejects.toMatchObject({ status: 400, detail: 'alias ".." is reserved' })
    })
  })

  it('no wrapper ever sends `force` (spec D-7)', async () => {
    fetchMock
      .mockResolvedValueOnce(jsonResponse(201, { alias: 'air', url: 'http://100.64.0.4:7860', host_id: 'x', inbound_token: TOKEN_NEW, verified: true }))
      .mockResolvedValueOnce(jsonResponse(200, { alias: 'air', inbound_token: TOKEN_NEW }))
      .mockResolvedValueOnce(jsonResponse(200, ROW))
      .mockResolvedValueOnce(jsonResponse(200, ROW))
      .mockResolvedValueOnce(new Response(null, { status: 204 }))
    await addPeerHost(H, { alias: 'air', url: 'http://100.64.0.4:7860', token: TOKEN_OUT })
    await rotatePeerHost(H, 'air')
    await commitRotation(H, 'air')
    await cancelRotation(H, 'air')
    await deletePeerHost(H, 'air')
    expect(fetchMock).toHaveBeenCalledTimes(5)
    for (const [, init] of fetchMock.mock.calls) {
      const body = init?.body === undefined ? '' : String(init.body)
      expect(body).not.toContain('force')
    }
  })
})
