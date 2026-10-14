import { describe, it, expect, beforeEach, vi } from 'vitest'
import { fetchFleet, fetchProbeHosts, fetchSshAliases, putProbeHosts } from './api'
import { useHostStore } from '../../stores/useHostStore'

const H = 'h1'
const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
const call = (m: ReturnType<typeof vi.fn>, i = 0) => m.mock.calls[i] as unknown as [string, RequestInit]

beforeEach(() => {
  vi.unstubAllGlobals()
  useHostStore.setState({
    hosts: { [H]: { id: H, name: 'mlab', ip: '100.64.0.2', port: 7860, token: 'tok', order: 0 } },
    hostOrder: [H], activeHostId: H,
  })
})

describe('fetchFleet / fetchSshAliases', () => {
  it('GETs /api/fleet and parses it', async () => {
    const f = vi.fn(async () => json({ self: { cpu: 5, zone: 'normal' }, probes: [] }))
    vi.stubGlobal('fetch', f)
    const p = await fetchFleet(H)
    expect(call(f)[0]).toBe('http://100.64.0.2:7860/api/fleet')
    expect(p.self?.cpu).toBe(5)
  })
  it('throws on a non-2xx or a body that is not the shape', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response('no', { status: 500 })))
    await expect(fetchFleet(H)).rejects.toThrow()
    vi.stubGlobal('fetch', vi.fn(async () => json({ nope: 1 })))
    await expect(fetchFleet(H)).rejects.toThrow()
  })
  it('reads the alias list', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => json({ aliases: ['air19', 'mserv', 3] })))
    await expect(fetchSshAliases(H)).resolves.toEqual(['air19', 'mserv'])
  })
})

describe('probe host definitions', () => {
  it('reads probeHosts out of GET /api/hostconfig', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => json({ probeHosts: { items: [{ id: 'a', name: 'n', ssh_alias: 's' }], revision: 4 } })))
    await expect(fetchProbeHosts(H)).resolves.toEqual({ items: [{ id: 'a', name: 'n', ssh_alias: 's' }], revision: 4 })
  })
  it('an old daemon without the field answers null', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => json({ projects: { items: [], revision: 0 } })))
    await expect(fetchProbeHosts(H)).resolves.toBeNull()
  })
  it('refuses an unknown host without fetching', async () => {
    const f = vi.fn()
    vi.stubGlobal('fetch', f)
    await expect(fetchProbeHosts('ghost')).rejects.toThrow()
    await expect(putProbeHosts('ghost', [], 0)).rejects.toThrow()
    expect(f).not.toHaveBeenCalled()
  })
})

describe('putProbeHosts', () => {
  it('PUTs {items, baseRevision} with only the wire keys and answers ok with the stored copy', async () => {
    const f = vi.fn(async () => json({ items: [{ id: 'a', name: 'n', ssh_alias: 's' }], revision: 5 }))
    vi.stubGlobal('fetch', f)
    const res = await putProbeHosts(H, [{ id: 'a', name: 'n', ssh_alias: 's', icon: 'Desktop', icon_weight: 'bold', colors: undefined, status: 'ok' } as never], 4)
    const [url, init] = call(f)
    expect(url).toBe('http://100.64.0.2:7860/api/hostconfig/probe-hosts')
    expect(init.method).toBe('PUT')
    expect(JSON.parse(init.body as string)).toEqual({ items: [{ id: 'a', name: 'n', ssh_alias: 's', icon: 'Desktop', icon_weight: 'bold' }], baseRevision: 4 })
    expect(res).toEqual({ kind: 'ok', current: { items: [{ id: 'a', name: 'n', ssh_alias: 's' }], revision: 5 } })
  })
  it('409 carries the daemon current copy', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => json({ items: [{ id: 'z', name: 'Z', ssh_alias: 'z' }], revision: 9 }, 409)))
    await expect(putProbeHosts(H, [], 1)).resolves.toEqual({ kind: 'conflict', current: { items: [{ id: 'z', name: 'Z', ssh_alias: 'z' }], revision: 9 } })
  })
  it('400 is a plain-text reason, shown as is', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response('unknown_ssh_alias: nope\n', { status: 400 })))
    await expect(putProbeHosts(H, [], 1)).resolves.toEqual({ kind: 'invalid', text: 'unknown_ssh_alias: nope' })
  })
  it('any other failure rejects', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response('', { status: 403 })))
    await expect(putProbeHosts(H, [], 1)).rejects.toThrow()
  })
})
