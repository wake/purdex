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

import { mergedProbeEntries, probeBaseOf, useFleetConfigStore } from './config-store'
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
  fetchSshAliases.mockResolvedValue(['nas-a', 'nas-b', 'x'])
  putProbeHosts.mockResolvedValue(undefined)
  putFleetRow.mockResolvedValue(undefined)
  useFleetConfigStore.getState().reset()
})

describe('saveProbe', () => {
  it('writes BOTH daemons, each with its own alias and its own revision', async () => {
    await ready()
    const r = await useFleetConfigStore.getState().saveProbe([A, B], { kind: 'upsert', entry })
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
    const r = await useFleetConfigStore.getState().saveProbe([A, B], { kind: 'upsert', entry })
    expect(r).toEqual({ kind: 'conflict', outcomes: [{ hostId: A, ok: true }, { hostId: B, ok: false, conflict: true }] })
    expect(fetchFleetConfig.mock.calls.map((c) => c[0]).sort()).toEqual([A, B])
  })

  it('a 400 returns the daemon text', async () => {
    await ready()
    putProbeHosts.mockRejectedValueOnce(new HostConfigApiError(400, 'unknown_ssh_alias: nas-a'))
    const r = await useFleetConfigStore.getState().saveProbe([A, B], { kind: 'upsert', entry })
    expect(r).toEqual({ kind: 'error', text: 'unknown_ssh_alias: nas-a', outcomes: [{ hostId: A, ok: false, text: 'unknown_ssh_alias: nas-a' }, { hostId: B, ok: true }] })
  })

  it('dropped > 0 asks first and writes nothing; confirmed, it writes', async () => {
    await ready(cfg([row('aaaaaaaa', 'nas-a')], 3, { dropped: { count: 1, reasons: ['unknown_ssh_alias: gone'] } }))
    const r = await useFleetConfigStore.getState().saveProbe([A, B], { kind: 'upsert', entry })
    expect(r).toEqual({ kind: 'confirm', notices: [{ hostId: A, count: 1, reasons: ['unknown_ssh_alias: gone'], invalid: false }] })
    expect(putProbeHosts).not.toHaveBeenCalled()
    const again = await useFleetConfigStore.getState().saveProbe([A, B], { kind: 'upsert', entry }, { confirmDropped: true })
    expect(again).toEqual({ kind: 'saved' })
    expect(putProbeHosts).toHaveBeenCalledTimes(2)
  })

  it('an invalid stored row also asks first', async () => {
    await ready(cfg([], 3, { invalid: true }))
    const r = await useFleetConfigStore.getState().saveProbe([A, B], { kind: 'upsert', entry })
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
    expect(await useFleetConfigStore.getState().saveFleetRow(A, { order: ['a'], peer_ssh_alias: '', enabled: true })).toEqual({ kind: 'error', text: 'order: bad', outcomes: [{ hostId: A, ok: false, text: 'order: bad' }] })
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

const named = (id: string, name: string, alias: string, over: Partial<ProbeRow> = {}): ProbeRow => ({ id, name, ssh_alias: alias, icon: 'Desktop', icon_weight: 'bold', ...over })
const putBy = () => Object.fromEntries(putProbeHosts.mock.calls.map((c) => [c[0], c]))
const other = { id: 'bbbbbbbb', name: 'other', icon: '', icon_weight: '', aliases: { [A]: 'nas-a', [B]: 'nas-b' } }
const store = () => useFleetConfigStore.getState()

describe('saveProbe — each daemon keeps its own rows', () => {
  it('an upsert is applied to EACH daemon\'s own list: a mismatched row the reader did not touch keeps both versions', async () => {
    await ready(cfg([named('aaaaaaaa', 'on A', 'nas-a')], 3), cfg([named('aaaaaaaa', 'on B', 'nas-b')], 8))
    expect(await store().saveProbe([A, B], { kind: 'upsert', entry: other })).toEqual({ kind: 'saved' })
    const by = putBy()
    expect((by[A][1] as ProbeRow[]).map((r) => [r.id, r.name])).toEqual([['aaaaaaaa', 'on A'], ['bbbbbbbb', 'other']])
    expect((by[B][1] as ProbeRow[]).map((r) => [r.id, r.name])).toEqual([['aaaaaaaa', 'on B'], ['bbbbbbbb', 'other']])
  })
  it('a row only one daemon has stays where it is when something else is saved', async () => {
    await ready(cfg([named('aaaaaaaa', 'half', 'half-a')], 3), cfg([], 8))
    await store().saveProbe([A, B], { kind: 'upsert', entry: other })
    const by = putBy()
    expect((by[A][1] as ProbeRow[]).map((r) => r.id)).toEqual(['aaaaaaaa', 'bbbbbbbb'])
    expect((by[B][1] as ProbeRow[]).map((r) => r.id)).toEqual(['bbbbbbbb'])
  })
  it('remove drops the id from both lists and keeps each daemon\'s other rows', async () => {
    await ready(cfg([named('aaaaaaaa', 'x', 'nas-a'), named('bbbbbbbb', 'y', 'nas-a')], 3), cfg([named('aaaaaaaa', 'x', 'nas-b')], 8))
    await store().saveProbe([A, B], { kind: 'remove', id: 'aaaaaaaa' })
    const by = putBy()
    expect((by[A][1] as ProbeRow[]).map((r) => r.id)).toEqual(['bbbbbbbb'])
    expect(by[B][1]).toEqual([])
  })
  it('the saved name is the trimmed one', async () => {
    await ready()
    await store().saveProbe([A, B], { kind: 'upsert', entry: { ...entry, name: '  nas  ' } })
    expect((putBy()[A][1] as ProbeRow[])[0].name).toBe('nas')
  })
})

describe('saveProbe — rewrite', () => {
  it('writes only the daemon whose copy differs, keeping its own alias', async () => {
    await ready(cfg([named('aaaaaaaa', 'first', 'nas-a')], 3), cfg([named('aaaaaaaa', 'second', 'nas-b')], 8))
    expect(await store().saveProbe([A, B], { kind: 'rewrite', id: 'aaaaaaaa' })).toEqual({ kind: 'saved' })
    expect(putProbeHosts).toHaveBeenCalledTimes(1)
    expect(putProbeHosts.mock.calls[0][0]).toBe(B)
    expect((putProbeHosts.mock.calls[0][1] as ProbeRow[])[0]).toMatchObject({ name: 'first', ssh_alias: 'nas-b' })
  })
  it('nothing differs: nothing is written', async () => {
    await ready(cfg([named('aaaaaaaa', 'same', 'nas-a')], 3), cfg([named('aaaaaaaa', 'same', 'nas-b')], 8))
    expect(await store().saveProbe([A, B], { kind: 'rewrite', id: 'aaaaaaaa' })).toEqual({ kind: 'saved' })
    expect(putProbeHosts).not.toHaveBeenCalled()
  })
  it('a failure on the one daemon is reported for that daemon', async () => {
    await ready(cfg([named('aaaaaaaa', 'first', 'nas-a')], 3), cfg([named('aaaaaaaa', 'second', 'nas-b')], 8))
    putProbeHosts.mockRejectedValueOnce(new HostConfigApiError(500, 'disk full'))
    const r = await store().saveProbe([A, B], { kind: 'rewrite', id: 'aaaaaaaa' })
    expect(r).toEqual({ kind: 'error', text: 'disk full', outcomes: [{ hostId: B, ok: false, text: 'disk full' }] })
  })
})

describe('saveProbe — the aliases it will send', () => {
  it('an empty alias is refused and nothing is sent', async () => {
    await ready()
    const r = await store().saveProbe([A, B], { kind: 'upsert', entry: { ...entry, aliases: { [A]: 'nas-a', [B]: '' } } })
    expect(r).toEqual({ kind: 'missing_alias', rows: [{ id: 'aaaaaaaa', name: 'nas', missing: [B], presentOn: [A] }] })
    expect(putProbeHosts).not.toHaveBeenCalled()
  })
  it('an alias the daemon\'s loaded list does not carry is refused', async () => {
    await ready()
    const r = await store().saveProbe([A, B], { kind: 'upsert', entry: { ...entry, aliases: { [A]: 'nas-a', [B]: 'nope' } } })
    expect(r.kind).toBe('missing_alias')
    expect(putProbeHosts).not.toHaveBeenCalled()
  })
  it('when a daemon\'s alias list could not be read its aliases are not blocked here (the daemon checks)', async () => {
    fetchSshAliases.mockImplementation(async (id: string) => { if (id === B) throw new Error('down'); return ['nas-a'] })
    await ready()
    expect(store().byHost[B].aliasesLoaded).toBe(false)
    expect(await store().saveProbe([A, B], { kind: 'upsert', entry })).toEqual({ kind: 'saved' })
  })
})

describe('saveProbe — an edit remembers what it started from', () => {
  const start = async () => {
    await ready(cfg([named('aaaaaaaa', 'v0', 'nas-a')], 3), cfg([named('aaaaaaaa', 'v0', 'nas-b')], 8))
    return probeBaseOf([A, B], store().byHost, 'aaaaaaaa')
  }
  const edited = { ...entry, name: 'mine' }
  it('A saved and B answered 409: a retry does not overwrite B\'s newer copy until it is confirmed', async () => {
    const base = await start()
    putProbeHosts.mockImplementation(async (id: string) => { if (id === B) throw new HostConfigConflictError({ items: [], revision: 9 }) })
    // while the save is in flight somebody else changed B's row
    fetchFleetConfig.mockImplementation(async (id: string) => (id === A ? cfg([named('aaaaaaaa', 'mine', 'nas-a')], 4) : cfg([named('aaaaaaaa', 'theirs', 'nas-b')], 9)))
    expect((await store().saveProbe([A, B], { kind: 'upsert', entry: edited }, { base })).kind).toBe('conflict')
    putProbeHosts.mockReset().mockResolvedValue(undefined)
    expect(await store().saveProbe([A, B], { kind: 'upsert', entry: edited }, { base })).toEqual({ kind: 'changed', hosts: [B] })
    expect(putProbeHosts).not.toHaveBeenCalled()
    expect(await store().saveProbe([A, B], { kind: 'upsert', entry: edited }, { base, confirmChanged: true })).toEqual({ kind: 'saved' })
    const by = putBy()
    expect(by[B][2]).toBe(9) // the revision it was read at
    expect((by[B][1] as ProbeRow[])[0].name).toBe('mine')
  })
  it('B moved only in other rows: the edit goes through at the new revision without asking', async () => {
    const base = await start()
    fetchFleetConfig.mockImplementation(async (id: string) => (id === A ? cfg([named('aaaaaaaa', 'v0', 'nas-a')], 3) : cfg([named('aaaaaaaa', 'v0', 'nas-b'), named('cccccccc', 'new', 'nas-b')], 9)))
    await store().load(B)
    expect(await store().saveProbe([A, B], { kind: 'upsert', entry: edited }, { base })).toEqual({ kind: 'saved' })
    expect(putBy()[B][2]).toBe(9)
    expect((putBy()[B][1] as ProbeRow[]).map((r) => r.id)).toEqual(['aaaaaaaa', 'cccccccc']) // the other row survives
  })
  it('a background read between the edit and the save does not hide a change to the edited row', async () => {
    const base = await start()
    fetchFleetConfig.mockImplementation(async (id: string) => (id === A ? cfg([named('aaaaaaaa', 'v0', 'nas-a')], 3) : cfg([named('aaaaaaaa', 'changed', 'nas-b')], 9)))
    await store().load(B)
    expect((await store().saveProbe([A, B], { kind: 'upsert', entry: edited }, { base })).kind).toBe('changed')
  })
})

describe('mergedProbeEntries — mismatch', () => {
  const merged = (a: ProbeRow, b: ProbeRow) => {
    useFleetConfigStore.setState({ byHost: {
      [A]: { status: 'ready', probe: { items: [a], revision: 1, dropped: null, invalid: false }, fleet: cfg([], 1).fleet, aliases: [], aliasesLoaded: true },
      [B]: { status: 'ready', probe: { items: [b], revision: 1, dropped: null, invalid: false }, fleet: cfg([], 1).fleet, aliases: [], aliasesLoaded: true },
    } })
    return mergedProbeEntries([A, B], store().byHost)[0]
  }
  const c1 = { console: { main: { color: '#336699', alpha: 100 }, light: { color: '#ffffff', alpha: 20 } } }
  const c2 = { console: { light: { alpha: 20, color: '#FFFFFF' }, main: { alpha: 100, color: '#336699' } } }
  it('a different name is a mismatch', () => { expect(merged(named('aaaaaaaa', 'a', 'x'), named('aaaaaaaa', 'b', 'y')).mismatch).toBe(true) })
  it('the same colours written in another key order and hex case are equal', () => {
    expect(merged(named('aaaaaaaa', 'a', 'x', { colors: c1 }), named('aaaaaaaa', 'a', 'y', { colors: c2 })).mismatch).toBeUndefined()
  })
  it('an empty icon weight equals the default weight', () => {
    expect(merged(named('aaaaaaaa', 'a', 'x', { icon_weight: '' }), named('aaaaaaaa', 'a', 'y', { icon_weight: 'regular' })).mismatch).toBeUndefined()
  })
  it('different aliases alone are not a mismatch', () => {
    expect(merged(named('aaaaaaaa', 'a', 'x'), named('aaaaaaaa', 'a', 'y')).mismatch).toBeUndefined()
  })
})

describe('load — stale answers', () => {
  it('a slow older read does not put an old revision back', async () => {
    let releaseOld: (v: FleetConfigRead) => void = () => {}
    fetchFleetConfig.mockReset()
    fetchFleetConfig.mockImplementationOnce(() => new Promise<FleetConfigRead>((res) => { releaseOld = res }))
    fetchFleetConfig.mockImplementationOnce(async () => cfg([row('aaaaaaaa', 'new')], 9))
    const old = store().load(A)
    await store().load(A)
    releaseOld(cfg([row('aaaaaaaa', 'old')], 1))
    await old
    expect(store().byHost[A].probe.revision).toBe(9)
  })
  it('a read that started before reset() cannot land after it', async () => {
    let releaseOld: (v: FleetConfigRead) => void = () => {}
    fetchFleetConfig.mockReset()
    fetchFleetConfig.mockImplementationOnce(() => new Promise<FleetConfigRead>((res) => { releaseOld = res }))
    const old = store().load(A)
    store().reset()
    fetchFleetConfig.mockImplementationOnce(async () => cfg([], 9))
    await store().load(A)
    releaseOld(cfg([], 1))
    await old
    expect(store().byHost[A].probe.revision).toBe(9)
  })
})
