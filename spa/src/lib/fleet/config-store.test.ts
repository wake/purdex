import { describe, it, expect, vi, beforeEach } from 'vitest'

const fetchFleetConfig = vi.fn()
const fetchSshAliases = vi.fn()
const putProbeHosts = vi.fn()
const putFleetRow = vi.fn()
vi.mock('./config-api', () => ({
  fetchFleetConfig: (...a: unknown[]) => fetchFleetConfig(...a),
  fetchSshAliases: (...a: unknown[]) => fetchSshAliases(...a),
  putProbeHosts: (...a: unknown[]) => putProbeHosts(...a),
  putFleetRow: (...a: unknown[]) => putFleetRow(...a),
}))

import { mergedProbeEntries, useFleetConfigStore } from './config-store'
import { HostConfigApiError, HostConfigConflictError } from '../host-config-api'
import type { FleetConfigRead, ProbeRow } from './config-api'

const row = (id: string, alias: string): ProbeRow => ({ id, name: `n-${id}`, ssh_alias: alias, icon: 'Desktop', icon_weight: 'bold' })
const cfg = (items: ProbeRow[], revision: number, over: Partial<FleetConfigRead['probe']> = {}): FleetConfigRead => ({
  probe: { items, revision, dropped: null, invalid: false, ...over },
  fleet: { items: { order: ['air26', 'mlab'], peer_ssh_alias: 'mlab', enabled: true }, revision: 2, dropped: null, invalid: false },
})

const A = 'ha'
const B = 'hb'
const entry = { id: 'aaaaaaaa', name: 'nas', icon: 'Desktop', icon_weight: 'bold', aliases: { [A]: 'nas-a', [B]: 'nas-b' } }

async function ready(a = cfg([], 3), b = cfg([], 8)) {
  fetchFleetConfig.mockImplementation(async (id: string) => (id === A ? a : b))
  await useFleetConfigStore.getState().loadMany([A, B])
}

beforeEach(() => {
  for (const f of [fetchFleetConfig, fetchSshAliases, putProbeHosts, putFleetRow]) f.mockReset()
  fetchSshAliases.mockResolvedValue(['x'])
  putProbeHosts.mockResolvedValue(undefined)
  putFleetRow.mockResolvedValue(undefined)
  useFleetConfigStore.getState().reset()
})

describe('saveProbeList', () => {
  it('writes BOTH daemons, each with its own alias and its own revision', async () => {
    await ready()
    const r = await useFleetConfigStore.getState().saveProbeList([A, B], [entry])
    expect(r).toEqual({ kind: 'saved' })
    const byHost = Object.fromEntries(putProbeHosts.mock.calls.map((c) => [c[0], c]))
    expect(byHost[A][1][0]).toMatchObject({ id: 'aaaaaaaa', ssh_alias: 'nas-a' })
    expect(byHost[A][2]).toBe(3)
    expect(byHost[B][1][0]).toMatchObject({ id: 'aaaaaaaa', ssh_alias: 'nas-b' })
    expect(byHost[B][2]).toBe(8)
  })

  it('a 409 from ONE daemon reloads both and says conflict', async () => {
    await ready()
    putProbeHosts.mockImplementation(async (id: string) => { if (id === B) throw new HostConfigConflictError({ items: [], revision: 9 }) })
    fetchFleetConfig.mockClear()
    const r = await useFleetConfigStore.getState().saveProbeList([A, B], [entry])
    expect(r).toEqual({ kind: 'conflict' })
    expect(fetchFleetConfig.mock.calls.map((c) => c[0]).sort()).toEqual([A, B])
  })

  it('a 400 returns the daemon text', async () => {
    await ready()
    putProbeHosts.mockRejectedValueOnce(new HostConfigApiError(400, 'unknown_ssh_alias: nas-a'))
    const r = await useFleetConfigStore.getState().saveProbeList([A, B], [entry])
    expect(r).toEqual({ kind: 'error', text: 'unknown_ssh_alias: nas-a' })
  })

  it('dropped > 0 asks first and writes nothing; confirmed, it writes', async () => {
    await ready(cfg([row('aaaaaaaa', 'nas-a')], 3, { dropped: { count: 1, reasons: ['unknown_ssh_alias: gone'] } }))
    const r = await useFleetConfigStore.getState().saveProbeList([A, B], [entry])
    expect(r).toEqual({ kind: 'confirm', notices: [{ hostId: A, count: 1, reasons: ['unknown_ssh_alias: gone'], invalid: false }] })
    expect(putProbeHosts).not.toHaveBeenCalled()
    const again = await useFleetConfigStore.getState().saveProbeList([A, B], [entry], { confirmDropped: true })
    expect(again).toEqual({ kind: 'saved' })
    expect(putProbeHosts).toHaveBeenCalledTimes(2)
  })

  it('an invalid stored row also asks first', async () => {
    await ready(cfg([], 3, { invalid: true }))
    const r = await useFleetConfigStore.getState().saveProbeList([A, B], [entry])
    expect(r.kind).toBe('confirm')
    expect(putProbeHosts).not.toHaveBeenCalled()
  })
})

describe('saveFleetRow', () => {
  it('round trips with its revision, then rereads', async () => {
    await ready()
    const row2 = { order: ['mlab', 'air26'], peer_ssh_alias: 'air26', enabled: false }
    expect(await useFleetConfigStore.getState().saveFleetRow(A, row2)).toEqual({ kind: 'saved' })
    expect(putFleetRow).toHaveBeenCalledWith(A, row2, 2)
  })
  it('409 reloads and says conflict; 400 keeps the text', async () => {
    await ready()
    putFleetRow.mockRejectedValueOnce(new HostConfigConflictError({ items: {}, revision: 5 }))
    expect((await useFleetConfigStore.getState().saveFleetRow(A, { order: ['a'], peer_ssh_alias: '', enabled: true })).kind).toBe('conflict')
    putFleetRow.mockRejectedValueOnce(new HostConfigApiError(400, 'order: bad'))
    expect(await useFleetConfigStore.getState().saveFleetRow(A, { order: ['a'], peer_ssh_alias: '', enabled: true })).toEqual({ kind: 'error', text: 'order: bad' })
  })
  it('a dropped marker on the fleet row asks first', async () => {
    const c = cfg([], 3)
    c.fleet.dropped = { count: 1, reasons: ['peer_ssh_alias cleared: unknown_ssh_alias: z'] }
    await ready(c)
    const r = await useFleetConfigStore.getState().saveFleetRow(A, { order: ['a'], peer_ssh_alias: '', enabled: true })
    expect(r.kind).toBe('confirm')
    expect(putFleetRow).not.toHaveBeenCalled()
  })
})

describe('mergedProbeEntries and the focus request', () => {
  it('one list by id, with each daemon\'s alias', async () => {
    await ready(cfg([row('aaaaaaaa', 'a1'), row('bbbbbbbb', 'a2')], 1), cfg([row('bbbbbbbb', 'b2'), row('cccccccc', 'b3')], 1))
    const list = mergedProbeEntries([A, B], useFleetConfigStore.getState().byHost)
    expect(list.map((e) => e.id)).toEqual(['aaaaaaaa', 'bbbbbbbb', 'cccccccc'])
    expect(list[1].aliases).toEqual({ [A]: 'a2', [B]: 'b2' })
  })
  it('requestProbeFocus sets and clearProbeFocus clears', () => {
    useFleetConfigStore.getState().requestProbeFocus('aaaaaaaa')
    expect(useFleetConfigStore.getState().focusProbe).toBe('aaaaaaaa')
    useFleetConfigStore.getState().clearProbeFocus()
    expect(useFleetConfigStore.getState().focusProbe).toBeNull()
  })
})
