import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { useHostStore } from '../stores/useHostStore'
import {
  HostApiError, listPeerHosts, verifyPeerHost, updatePeerHost, fetchPeerSettings, fetchHostInfo,
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
  verified: true, has_token: true, has_inbound_token: true, allow_bypass: true }

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
})
