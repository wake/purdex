import { describe, it, expect, vi, beforeEach } from 'vitest'

const hostFetch = vi.fn()
vi.mock('../host-api', () => ({ hostFetch: (...a: unknown[]) => hostFetch(...a) }))

import { fetchFleetConfig, fetchSshAliases, parseFleetConfig, putFleetRow, putProbeHosts } from './config-api'
import { HostConfigConflictError } from '../host-config-api'

const res = (status: number, body: unknown, text = '') => ({
  ok: status >= 200 && status < 300, status, statusText: '',
  json: async () => body, text: async () => text || (typeof body === 'string' ? body : JSON.stringify(body)),
})

beforeEach(() => hostFetch.mockReset())

describe('parseFleetConfig', () => {
  it('reads probeHosts rows, revisions and the dropped marker', () => {
    const r = parseFleetConfig({
      probeHosts: {
        items: [{ id: 'aaaaaaaa', name: 'nas', ssh_alias: 'nas', icon: 'HardDrives', icon_weight: 'bold', colors: { console: { main: { color: '#112233', alpha: 80 } } } }, { nope: 1 }],
        revision: 4,
        dropped: { count: 2, reasons: ['unknown_ssh_alias: x'] },
      },
      fleet: { items: { order: ['air26', 'mlab'], peer_ssh_alias: 'mlab', enabled: false }, revision: 7 },
    })
    expect(r.probe.items).toHaveLength(1)
    expect(r.probe.revision).toBe(4)
    expect(r.probe.dropped).toEqual({ count: 2, reasons: ['unknown_ssh_alias: x'] })
    expect(r.fleet.items).toEqual({ order: ['air26', 'mlab'], peer_ssh_alias: 'mlab', enabled: false })
    expect(r.fleet.dropped).toBeNull()
  })
  it('a daemon without the rows reads as empty, revision 0', () => {
    expect(parseFleetConfig({}).probe).toMatchObject({ items: [], revision: 0, dropped: null, invalid: false })
  })
})

describe('calls', () => {
  it('fetchFleetConfig and fetchSshAliases read the daemon', async () => {
    hostFetch.mockResolvedValueOnce(res(200, { probeHosts: { items: [], revision: 1 } }))
    expect((await fetchFleetConfig('h1')).probe.revision).toBe(1)
    hostFetch.mockResolvedValueOnce(res(200, { aliases: ['a', 'b', 3] }))
    expect(await fetchSshAliases('h1')).toEqual(['a', 'b'])
    expect(hostFetch).toHaveBeenLastCalledWith('h1', '/api/fleet/ssh-aliases')
  })
  it('PUT sends {items, baseRevision}; 409 carries the copy; 400 keeps the plain text', async () => {
    hostFetch.mockResolvedValueOnce(res(200, {}))
    await putProbeHosts('h1', [], 3)
    const [, path, init] = hostFetch.mock.calls[0]
    expect(path).toBe('/api/hostconfig/probe-hosts')
    expect(JSON.parse(init.body)).toEqual({ items: [], baseRevision: 3 })
    hostFetch.mockResolvedValueOnce(res(409, { items: [], revision: 9 }))
    await expect(putFleetRow('h1', { order: ['a'], peer_ssh_alias: '', enabled: true }, 3)).rejects.toBeInstanceOf(HostConfigConflictError)
    hostFetch.mockResolvedValueOnce(res(400, '', 'unknown_ssh_alias: zz'))
    await expect(putProbeHosts('h1', [], 3)).rejects.toMatchObject({ status: 400, message: 'unknown_ssh_alias: zz' })
  })
})
