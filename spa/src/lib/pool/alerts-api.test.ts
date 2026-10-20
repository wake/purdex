import { describe, it, expect, vi, beforeEach } from 'vitest'
import { parseHostAlerts, fetchHostAlerts } from './alerts-api'

vi.mock('../host-api', () => ({ hostFetch: vi.fn() }))
import { hostFetch } from '../host-api'

// The Go shape: internal/module/pool/alerts.go PoolHostAlerts (wire.Alert inlined, `silence` for a silence only).
const DISK = { key: 'disk_low:mlab', kind: 'disk_low', level: 'push', since: '2026-10-20T06:31:00Z', last: '2026-10-20T07:00:00Z', cleared_at: null, detail: 'mlab 剩 11.2 GB' }
const SILENCE_CLEARED = { key: 'silence:air19', kind: 'silence', level: 'push', since: '2026-10-20T06:31:00Z', last: '2026-10-20T07:19:00Z', cleared_at: '2026-10-20T07:19:00Z', detail: 'air19：主機離線', silence: 'host_down' }
const ORPHANS = { key: 'orphans:mlab', kind: 'orphans', level: 'digest', since: '2026-10-20T05:00:00Z', last: '2026-10-20T07:00:00Z', cleared_at: null, detail: 'dev 孤兒 4 個' }
const answer = (alerts: unknown) => ({ host: 'mlab', known: true, alerts })

describe('parseHostAlerts', () => {
  it('reads a push, a cleared silence and a digest, times as ms', () => {
    const r = parseHostAlerts(answer([DISK, SILENCE_CLEARED, ORPHANS]))!
    expect(r.host).toBe('mlab')
    expect(r.known).toBe(true)
    expect(r.alerts.map((a) => a.key)).toEqual(['disk_low:mlab', 'silence:air19', 'orphans:mlab'])
    expect(r.alerts[0]).toEqual({ key: 'disk_low:mlab', kind: 'disk_low', level: 'push', since: Date.parse('2026-10-20T06:31:00Z'), last: Date.parse('2026-10-20T07:00:00Z'), clearedAt: null, detail: 'mlab 剩 11.2 GB' })
    expect(r.alerts[1].clearedAt).toBe(Date.parse('2026-10-20T07:19:00Z'))
    expect(r.alerts[1].silence).toBe('host_down')
    expect(r.alerts[2].level).toBe('digest')
  })
  it('known: false is an answer with no alerts (an older wwd)', () => {
    expect(parseHostAlerts({ host: 'air19', known: false, alerts: [] })).toEqual({ host: 'air19', known: false, alerts: [] })
  })
  it('alerts: null is an empty list', () => {
    expect(parseHostAlerts(answer(null))!.alerts).toEqual([])
  })
  it('a bad row is dropped and the others stay', () => {
    const r = parseHostAlerts(answer([{ ...DISK, key: undefined }, { ...DISK, kind: 3 }, { ...DISK, since: 'yesterday' }, { ...DISK, last: null }, { ...DISK, cleared_at: 'soon' }, ORPHANS]))!
    expect(r.alerts.map((a) => a.key)).toEqual(['orphans:mlab'])
  })
  it('a kind or level it does not know stays as the word; a missing detail is empty', () => {
    const r = parseHostAlerts(answer([{ ...DISK, kind: 'new_rule', level: 'urgent', detail: undefined }]))!
    expect(r.alerts[0].kind).toBe('new_rule')
    expect(r.alerts[0].level).toBe('urgent')
    expect(r.alerts[0].detail).toBe('')
  })
  it('silence is read only for a silence; an unknown or missing one is unknown (partner_down)', () => {
    const r = parseHostAlerts(answer([
      { ...DISK, silence: 'host_down' },
      { ...SILENCE_CLEARED, silence: undefined },
      { ...SILENCE_CLEARED, key: 'silence:x', silence: 'something' },
      { ...SILENCE_CLEARED, key: 'silence:y', silence: 'wwd_down' },
    ]))!
    expect(r.alerts[0].silence).toBeUndefined()
    expect(r.alerts[1].silence).toBe('unknown')
    expect(r.alerts[2].silence).toBe('unknown')
    expect(r.alerts[3].silence).toBe('wwd_down')
  })
  it.each([null, 3, 'x', [], { host: '', known: true, alerts: [] }, { host: 'a', known: 'yes', alerts: [] }, { host: 'a', known: true, alerts: 'none' }, { known: true, alerts: [] }])('a wrong envelope is null: %j', (v) => {
    expect(parseHostAlerts(v)).toBeNull()
  })
})

describe('fetchHostAlerts', () => {
  beforeEach(() => vi.mocked(hostFetch).mockReset())
  const ok = (body: unknown) => vi.mocked(hostFetch).mockResolvedValue(new Response(JSON.stringify(body), { status: 200 }))
  it('asks the source daemon for that host alias', async () => {
    ok(answer([DISK]))
    const r = await fetchHostAlerts('cfg-a', 'mlab')
    expect(hostFetch).toHaveBeenCalledWith('cfg-a', '/api/pool/hosts/mlab/alerts')
    expect(r.alerts).toHaveLength(1)
  })
  it('an alias is path-encoded', async () => {
    ok(answer([]))
    await fetchHostAlerts('cfg-a', 'a b/c')
    expect(hostFetch).toHaveBeenCalledWith('cfg-a', '/api/pool/hosts/a%20b%2Fc/alerts')
  })
  it('an answer that parses as nothing is unreadable (wwd_bad_response)', async () => {
    ok({ nope: true })
    await expect(fetchHostAlerts('cfg-a', 'mlab')).rejects.toMatchObject({ poolError: { code: 'wwd_bad_response' } })
  })
  it('a refusal keeps its classification', async () => {
    vi.mocked(hostFetch).mockResolvedValue(new Response('{}', { status: 502 }))
    await expect(fetchHostAlerts('cfg-a', 'mlab')).rejects.toMatchObject({ status: 502 })
  })
})
