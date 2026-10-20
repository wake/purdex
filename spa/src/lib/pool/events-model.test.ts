import { describe, it, expect } from 'vitest'
import { alertRowsOf, observationRowsOf, mergeObservations, filterEvents, groupByDay, liveCounts, OBSERVATIONS_SHOWN, type EventRow } from './events-model'
import type { PoolAlert } from './alerts-api'
import type { PoolObservation } from './observations-api'
import type { HostAlertsEntry } from './alerts-store'

const t = (iso: string) => Date.parse(iso)
const HOSTS = [{ hostId: 'id-mlab', alias: 'mlab' }, { hostId: 'id-air26', alias: 'air26' }, { hostId: 'id-air19', alias: 'air19' }, { hostId: 'id-mserv', alias: 'mserv' }]
const alert = (key: string, over: Partial<PoolAlert> = {}): PoolAlert => ({ key, kind: 'disk_low', level: 'push', since: t('2026-10-20T06:00:00Z'), last: t('2026-10-20T06:30:00Z'), clearedAt: null, detail: '', ...over })
const silence = (alias: string, since: string, cleared: string | null, over: Partial<PoolAlert> = {}): PoolAlert =>
  alert(`silence:${alias}`, { kind: 'silence', silence: 'host_down', detail: `${alias}：主機離線`, since: t(since), last: t(cleared ?? since), clearedAt: cleared ? t(cleared) : null, ...over })
const ok = (alerts: PoolAlert[]): HostAlertsEntry => ({ kind: 'ok', known: true, alerts, at: 1 })
const obs = (id: string, started: string, over: Partial<PoolObservation> = {}): PoolObservation => ({ id, host: 'air26', session: 's1', via: 'session', command: 'go test', signature: 'go test', startedAt: t(started), endedAt: null, cpuSeconds: 61.5, cpuPeak: 150, memPeakMb: 300, procsPeak: 3, ...over })

describe('alertRowsOf (M23, M35)', () => {
  it('air19 06:31–07:19 host_down, in the lists of mlab and air26: one row, over, ending 07:19', () => {
    const s = silence('air19', '2026-10-20T06:31:00Z', '2026-10-20T07:19:00Z')
    const rows = alertRowsOf({ mlab: ok([s]), air26: ok([{ ...s, since: t('2026-10-20T06:32:00Z'), clearedAt: t('2026-10-20T07:18:00Z') }]) }, HOSTS)
    expect(rows).toHaveLength(1)
    expect(rows[0]).toMatchObject({ hostId: 'id-air19', alias: 'air19', source: 'alert', kind: 'silence', silence: 'host_down', live: false, at: t('2026-10-20T06:31:00Z'), endAt: t('2026-10-20T07:19:00Z') })
  })
  it('two stops of the same key that do not overlap (06:31–07:19, 09:02–09:10) are two rows', () => {
    const rows = alertRowsOf({ mlab: ok([silence('air19', '2026-10-20T06:31:00Z', '2026-10-20T07:19:00Z'), silence('air19', '2026-10-20T09:02:00Z', '2026-10-20T09:10:00Z')]) }, HOSTS)
    expect(rows).toHaveLength(2)
    expect(rows.map((r) => r.at).sort()).toEqual([t('2026-10-20T06:31:00Z'), t('2026-10-20T09:02:00Z')])
  })
  it('overlapping ones merge: still active if one is, otherwise it ends when the last one does', () => {
    const live = alertRowsOf({ mlab: ok([silence('air19', '2026-10-20T06:31:00Z', '2026-10-20T07:19:00Z')]), air26: ok([silence('air19', '2026-10-20T07:00:00Z', null)]) }, HOSTS)
    expect(live).toHaveLength(1)
    expect(live[0]).toMatchObject({ live: true, endAt: null, at: t('2026-10-20T06:31:00Z') })
    const done = alertRowsOf({ mlab: ok([silence('air19', '2026-10-20T06:31:00Z', '2026-10-20T07:19:00Z')]), air26: ok([silence('air19', '2026-10-20T07:00:00Z', '2026-10-20T07:40:00Z')]) }, HOSTS)
    expect(done[0]).toMatchObject({ live: false, endAt: t('2026-10-20T07:40:00Z') })
  })
  it('a silence of a peer the pool does not list belongs to the host whose list holds it', () => {
    const rows = alertRowsOf({ mlab: ok([silence('ghost', '2026-10-20T06:31:00Z', null)]) }, HOSTS)
    expect(rows[0]).toMatchObject({ hostId: 'id-mlab', alias: 'mlab' })
  })
  it('a digest orphans alert that cleared is over (M35); it belongs to the list\'s host', () => {
    const rows = alertRowsOf({ mlab: ok([alert('orphans:mlab', { kind: 'orphans', level: 'digest', detail: 'dev 孤兒 4 個', clearedAt: t('2026-10-20T08:00:00Z') })]) }, HOSTS)
    expect(rows[0]).toMatchObject({ hostId: 'id-mlab', level: 'digest', live: false, endAt: t('2026-10-20T08:00:00Z'), title: 'dev 孤兒 4 個' })
  })
  it('partner_down (a silence the App cannot tell apart, key not shaped silence:<alias>) belongs to the list\'s host', () => {
    const rows = alertRowsOf({ air26: ok([alert('partner_down', { kind: 'silence', silence: 'unknown', detail: 'x' })]) }, HOSTS)
    expect(rows[0]).toMatchObject({ hostId: 'id-air26', silence: 'unknown' })
  })
  it('a kind the App does not know stays as the word; the title falls back to it when there is no detail', () => {
    const rows = alertRowsOf({ mlab: ok([alert('x', { kind: 'new_rule', detail: '' })]) }, HOSTS)
    expect(rows[0]).toMatchObject({ kind: 'new_rule', title: 'new_rule' })
  })
  it('two alerts of one key on one host that do not overlap stay two (it came back)', () => {
    const rows = alertRowsOf({ mlab: ok([alert('disk_low:mlab', { since: t('2026-10-20T01:00:00Z'), clearedAt: t('2026-10-20T02:00:00Z') }), alert('disk_low:mlab', { since: t('2026-10-20T05:00:00Z') })]) }, HOSTS)
    expect(rows.map((r) => r.live).sort()).toEqual([false, true])
  })
  it('a host that failed to read, and a list of a host the pool does not know, give no rows', () => {
    expect(alertRowsOf({ mlab: { kind: 'error', code: 'wwd_unavailable', at: 1 }, nobody: ok([alert('a')]) }, HOSTS)).toEqual([])
  })
  it('when levels differ in a merge the row is push', () => {
    const rows = alertRowsOf({ mlab: ok([silence('air19', '2026-10-20T06:31:00Z', null, { level: 'digest' })]), air26: ok([silence('air19', '2026-10-20T06:40:00Z', null)]) }, HOSTS)
    expect(rows).toHaveLength(1)
    expect(rows[0].level).toBe('push')
  })
})

describe('observationRowsOf (M35)', () => {
  it('a host with 5 records: the newest 3 as rows (newest first) and one "more" row saying 2', () => {
    const list = ['01', '02', '03', '04', '05'].map((h) => obs(`o${h}`, `2026-10-20T${h}:00:00Z`))
    const rows = observationRowsOf({ air26: list }, HOSTS)
    const normal = rows.filter((r): r is Extract<EventRow, { more?: undefined }> => !('more' in r && r.more))
    expect(normal.map((r) => r.id)).toEqual(['id-air26|o05', 'id-air26|o04', 'id-air26|o03'])
    const more = rows.filter((r) => 'more' in r && r.more)
    expect(more).toHaveLength(1)
    expect(more[0]).toMatchObject({ more: true, hostId: 'id-air26', alias: 'air26', n: 2 })
    // the "more" row sorts right under the third record
    expect((more[0] as { at: number }).at).toBe(t('2026-10-20T03:00:00Z') - 1)
  })
  it('exactly 3 records have no "more" row', () => {
    expect(observationRowsOf({ air26: [obs('a', '2026-10-20T01:00:00Z'), obs('b', '2026-10-20T02:00:00Z'), obs('c', '2026-10-20T03:00:00Z')] }, HOSTS)).toHaveLength(OBSERVATIONS_SHOWN)
  })
  it('a row carries what the page writes: the signature, the session and way, CPU seconds, peak cores, memory, processes; running is live', () => {
    const [r] = observationRowsOf({ air26: [obs('a', '2026-10-20T01:00:00Z', { via: 'codex', endedAt: null })] }, HOSTS)
    expect(r).toMatchObject({ source: 'obs', level: 'obs', kind: 'undeclared', live: true, endAt: null, title: 'go test', hostId: 'id-air26' })
    expect((r as { obs: unknown }).obs).toEqual({ session: 's1', via: 'codex', cpuSeconds: 61.5, cpuCores: 1.5, memPeakMb: 300, procsPeak: 3 })
  })
  it('a finished record is over and ends when it ended', () => {
    const [r] = observationRowsOf({ air26: [obs('a', '2026-10-20T01:00:00Z', { endedAt: t('2026-10-20T01:10:00Z') })] }, HOSTS)
    expect(r).toMatchObject({ live: false, endAt: t('2026-10-20T01:10:00Z') })
  })
  it('records are taken newest first whatever order they came in (wwd gives the oldest first)', () => {
    const rows = observationRowsOf({ air26: [obs('old', '2026-10-20T01:00:00Z'), obs('new', '2026-10-20T09:00:00Z')] }, HOSTS)
    expect(rows.map((r) => ('id' in r ? r.id : ''))).toEqual(['id-air26|new', 'id-air26|old'])
  })
  it('two hosts with the same wwd observation id give two different row ids', () => {
    const same = (alias: string) => [obs('o_1_2', '2026-10-20T01:00:00Z', { host: alias })]
    const ids = observationRowsOf({ air26: same('air26'), mlab: same('mlab') }, HOSTS).map((r) => r.id)
    expect(new Set(ids).size).toBe(2)
  })
  it('a host the pool does not know gives nothing', () => {
    expect(observationRowsOf({ nobody: [obs('a', '2026-10-20T01:00:00Z')] }, HOSTS)).toEqual([])
  })
})

describe('mergeObservations', () => {
  it('a record on both pages (next_until is inclusive) is one', () => {
    const a = obs('a', '2026-10-20T01:00:00Z')
    const b = obs('b', '2026-10-20T02:00:00Z')
    expect(mergeObservations([a, b], [b, obs('c', '2026-10-20T03:00:00Z')]).map((o) => o.id)).toEqual(['a', 'b', 'c'])
  })
})

describe('filterEvents (M35)', () => {
  const rows = [
    ...alertRowsOf({ mlab: ok([alert('a'), alert('b', { clearedAt: t('2026-10-20T07:00:00Z') })]) }, HOSTS),
    ...observationRowsOf({ air26: ['01', '02', '03', '04'].map((h) => obs(`o${h}`, `2026-10-20T${h}:00:00Z`, { endedAt: t(`2026-10-20T${h}:30:00Z`) })) }, HOSTS),
  ]
  const f = (host: string, source: 'all' | 'alert' | 'obs', status: 'all' | 'live' | 'done') => filterEvents(rows, { host, source, status })
  const mores = (r: EventRow[]) => r.filter((x) => 'more' in x && x.more).length
  it('all: everything', () => {
    expect(f('all', 'all', 'all')).toHaveLength(rows.length)
  })
  it('by host', () => {
    expect(f('id-mlab', 'all', 'all').every((r) => r.hostId === 'id-mlab')).toBe(true)
    expect(f('id-air26', 'all', 'all').every((r) => r.hostId === 'id-air26')).toBe(true)
  })
  it('by source', () => {
    expect(f('all', 'alert', 'all').every((r) => 'source' in r && r.source === 'alert')).toBe(true)
    expect(f('all', 'obs', 'all').every((r) => ('source' in r && r.source === 'obs') || ('more' in r && r.more))).toBe(true)
  })
  it('by status', () => {
    expect(f('all', 'all', 'live').every((r) => !('more' in r && r.more) && r.live)).toBe(true)
    expect(f('all', 'all', 'done').every((r) => ('more' in r && r.more) || !r.live)).toBe(true)
  })
  it('the "more" row appears only for its host, not under wwd alerts, not under in-progress', () => {
    expect(mores(f('all', 'all', 'all'))).toBe(1)
    expect(mores(f('id-air26', 'obs', 'done'))).toBe(1)
    expect(mores(f('id-mlab', 'all', 'all'))).toBe(0)
    expect(mores(f('all', 'alert', 'all'))).toBe(0)
    expect(mores(f('all', 'all', 'live'))).toBe(0)
  })
})

describe('groupByDay', () => {
  const at = (y: number, mo: number, d: number, h: number, mi = 0) => new Date(y, mo - 1, d, h, mi).getTime()
  const row = (id: string, time: number): EventRow => ({ id, hostId: 'id-mlab', alias: 'mlab', at: time, source: 'alert', level: 'push', kind: 'x', live: true, endAt: null, title: id, last: time })
  it('newest first, the days named today, yesterday, then MM/DD', () => {
    const now = at(2026, 10, 20, 12)
    const g = groupByDay([row('old', at(2026, 10, 18, 9)), row('today-early', at(2026, 10, 20, 1)), row('yest', at(2026, 10, 19, 23, 59)), row('today-late', at(2026, 10, 20, 11))], now)
    expect(g.map((x) => [x.label, x.date])).toEqual([['today', '10/20'], ['yesterday', '10/19'], ['date', '10/18']])
    expect(g[0].rows.map((r) => ('id' in r ? r.id : ''))).toEqual(['today-late', 'today-early'])
  })
  it('across midnight: 23:59 and 00:01 are two days', () => {
    const now = at(2026, 10, 20, 12)
    const g = groupByDay([row('a', at(2026, 10, 19, 23, 59)), row('b', at(2026, 10, 20, 0, 1))], now)
    expect(g.map((x) => x.label)).toEqual(['today', 'yesterday'])
  })
  it('across a month: yesterday of 11/01 is 10/31', () => {
    const now = at(2026, 11, 1, 9)
    const g = groupByDay([row('a', at(2026, 10, 31, 20)), row('b', at(2026, 11, 1, 1))], now)
    expect(g.map((x) => [x.label, x.date])).toEqual([['today', '11/01'], ['yesterday', '10/31']])
  })
  it('nothing in, nothing out', () => {
    expect(groupByDay([], Date.now())).toEqual([])
  })
})

describe('liveCounts', () => {
  it('push in progress and the others in progress; over ones and "more" rows are not counted', () => {
    const rows = [
      ...alertRowsOf({ mlab: ok([alert('a'), alert('b'), alert('c', { level: 'digest' }), alert('d', { clearedAt: t('2026-10-20T07:00:00Z') })]) }, HOSTS),
      ...observationRowsOf({ air26: ['01', '02', '03', '04'].map((h) => obs(`o${h}`, `2026-10-20T${h}:00:00Z`)) }, HOSTS),
    ]
    expect(liveCounts(rows)).toEqual({ push: 2, other: 1 + 3 })
  })
})
