// The three JSON samples in __fixtures__/ are copied byte for byte from internal/wwd/wire/testdata: when the wire changes they go red.
import { describe, it, expect } from 'vitest'
import { parseFacts, parseFactsHistory, parseHostsAnswer, parseSeries } from './parse'
import hostsJson from './__fixtures__/pool_hosts_response.json'
import darwinJson from './__fixtures__/facts_darwin.json'
import linuxJson from './__fixtures__/facts_linux.json'

describe('facts samples', () => {
  it('darwin: times become ms, owners, residents, exceptions and the worker are read', () => {
    const f = parseFacts(darwinJson)!
    expect(f.at).toBe(Date.parse('2026-10-18T05:00:00+08:00'))
    expect(f.alias).toBe('mlab')
    expect(f.derived).toEqual({ zone: 'normal', headroom: { heavy: 1, light: 2 }, sessions_room: 2 })
    expect(f.procs.map((p) => p.owner)).toEqual([{ kind: 'job', id: 'j_01JC', session: 's1' }, { kind: 'system' }, null])
    expect(f.residents).toEqual([{ session: 's1', kind: 'codex-broker', count: 3, mem_mb: 640 }])
    expect(f.exceptions.map((e) => [e.name, e.until])).toEqual([['app-dev-package', Date.parse('2026-10-18T05:30:00+08:00')], ['dev-server', null]])
    expect(f.owner).toEqual({ active: true, idle_s: 42, reason: 'hid' })
    expect(f.worker).toEqual({ enabled: true, slots: { heavy: 1, light: 2 }, running: { heavy: 0, light: 1 }, owner_rule: 'hid<5m' })
    expect(f.mem.psi).toBeNull()
    expect(f.queue).toBeUndefined()
  })

  it('linux: psi is read, null fields stay null, the zone unknown is kept', () => {
    const f = parseFacts(linuxJson)!
    expect(f.mem.psi).toEqual({ some10: 0.5, full10: 0, some60: 0.3 })
    expect(f.mem.page_out_rate).toBeNull()
    expect(f.owner).toBeNull()
    expect(f.derived.zone).toBe('unknown')
    expect(f.tools.pnpm).toBeNull()
  })

  it('a zone the App does not know reads as unknown, never as an error', () => {
    const f = parseFacts({ ...darwinJson, derived: { ...darwinJson.derived, zone: 'purple' } })!
    expect(f.derived.zone).toBe('unknown')
  })

  it('malformed facts are null', () => {
    expect(parseFacts(null)).toBeNull()
    expect(parseFacts({ ...darwinJson, at: 'yesterday' })).toBeNull()
    expect(parseFacts({ ...darwinJson, worker: undefined })).toBeNull()
  })

  it('an unreadable swap counter stays null, not 0', () => {
    expect(parseFacts({ ...darwinJson, mem: { ...darwinJson.mem, swap_used_mb: null } })!.mem.swap_used_mb).toBeNull()
    expect(parseFacts(darwinJson)!.mem.swap_used_mb).toBe(2100)
  })

  it('facts whose worker slots are missing or malformed are null, not zero capacity', () => {
    expect(parseFacts({ ...darwinJson, worker: { ...darwinJson.worker, slots: undefined } })).toBeNull()
    expect(parseFacts({ ...darwinJson, worker: { ...darwinJson.worker, slots: { heavy: 'x', light: 1 } } })).toBeNull()
  })

  it('queue.queued is read when present', () => {
    expect(parseFacts({ ...darwinJson, queue: { queued: 2 } })!.queue).toEqual({ queued: 2 })
  })
})

describe('hosts answer', () => {
  it('the sample: an unreachable host with no facts', () => {
    const a = parseHostsAnswer(hostsJson)!
    expect(a).toHaveLength(1)
    expect(a[0]).toMatchObject({ alias: 'air19', host_id: 'h-air19', reachable: false, facts: null, labels: [], version: '0.1.0', proto: 1 })
    expect(a[0].worker).toEqual({ enabled: false, slots: { heavy: 0, light: 0 }, owner_rule: '' })
    expect(a[0].headroom_reasons).toBeUndefined()
    expect(a[0].load).toBeUndefined()
  })

  it('assess fields are optional; reasons keep their order and unknown codes', () => {
    const row = { ...hostsJson.hosts[0], facts: darwinJson, reachable: true,
      headroom_reasons: { heavy: ['worker_off', 'exception:a', 'exception:b', 'from_the_future'], light: ['gate_full'] }, load: { score: 71, driver: 'mem' } }
    const [h] = parseHostsAnswer({ hosts: [row] })!
    expect(h.headroom_reasons).toEqual({ heavy: ['worker_off', 'exception:a', 'exception:b', 'from_the_future'], light: ['gate_full'] })
    expect(h.load).toEqual({ score: 71, driver: 'mem' })
    expect(h.facts?.alias).toBe('mlab')
  })

  // The values are what the daemon's own test asserts for this body (internal/module/pool/module_test.go TestHosts_ReasonsAndLoad,
  // #3496): mlab is in the warning zone with its heavy slots full and its fullest disk at 91 %, light has room (an empty list, not null).
  it('the non-null assess shape the daemon answers for a host with facts (module_test.go TestHosts_ReasonsAndLoad)', () => {
    const [h] = parseHostsAnswer({ hosts: [{ ...hostsJson.hosts[0], facts: darwinJson, reachable: true,
      headroom_reasons: { heavy: ['zone_warning', 'slots_full'], light: [] }, load: { score: 91, driver: 'disk' } }] })!
    expect(h.headroom_reasons).toEqual({ heavy: ['zone_warning', 'slots_full'], light: [] })
    expect(h.load).toEqual({ score: 91, driver: 'disk' })
  })

  it('a malformed row is dropped whole, malformed facts only null that part, a malformed envelope is null', () => {
    const bad = { hosts: [{ nope: 1 }, { ...hostsJson.hosts[0], facts: { v: 1 } }] }
    const a = parseHostsAnswer(bad)!
    expect(a.map((h) => h.alias)).toEqual(['air19'])
    expect(a[0].facts).toBeNull()
    expect(parseHostsAnswer({ hosts: 3 })).toBeNull()
    expect(parseHostsAnswer(null)).toBeNull()
  })

  it('an unknown driver is kept as text', () => {
    const [h] = parseHostsAnswer({ hosts: [{ ...hostsJson.hosts[0], load: { score: 5, driver: 'gpu' } }] })!
    expect(h.load).toEqual({ score: 5, driver: 'gpu' })
  })
})

// R2 (attack): a response that drifted from the wire must not turn into a believable wrong state.
describe('required fields', () => {
  const row = (over: Record<string, unknown>) => ({ ...hostsJson.hosts[0], ...over })
  const drop = (o: Record<string, unknown>, k: string) => { const { [k]: _gone, ...rest } = o; void _gone; return rest }

  it.each([['worker', undefined], ['worker', { enabled: 'yes' }], ['reachable', 'true'], ['reachable', undefined], ['labels', 'os:darwin']])(
    'a host row with %s = %j is dropped, not shown as unreachable or zero capacity', (k, v) => {
      const bad = v === undefined ? drop(row({}), k) : row({ [k]: v })
      const a = parseHostsAnswer({ hosts: [bad, row({ info: { ...hostsJson.hosts[0].info, host_id: 'h-ok', alias: 'ok' } })] })!
      expect(a.map((h) => h.alias)).toEqual(['ok'])
    })

  it('worker slots missing in a host row drop the row', () => {
    expect(parseHostsAnswer({ hosts: [row({ worker: { enabled: true, owner_rule: '' } }), row({})] })!).toHaveLength(1)
  })

  it('an answer whose every row is malformed is unreadable, so the caller treats it as wwd_bad_response', () => {
    expect(parseHostsAnswer({ hosts: [{ nope: 1 }] })).toBeNull()
    expect(parseHostsAnswer({ hosts: [] })).toEqual([])
  })

  it('null labels read as none', () => {
    expect(parseHostsAnswer({ hosts: [row({ labels: null })] })![0].labels).toEqual([])
  })

  it.each(['platform', 'arch', 'os_version', 'tools', 'disk', 'procs', 'residents', 'exceptions'])('facts without %s are null', (k) => {
    expect(parseFacts(drop(darwinJson, k))).toBeNull()
  })
  it('facts carry a supported wire version: 1 and 2 are read, absent, 0 and a future one are null', () => {
    expect(parseFacts({ ...darwinJson, v: 2 })!.v).toBe(2)
    expect(parseFacts(darwinJson)!.v).toBe(1)
    expect(parseFacts(drop(darwinJson, 'v'))).toBeNull()
    expect(parseFacts({ ...darwinJson, v: 0 })).toBeNull()
    expect(parseFacts({ ...darwinJson, v: 3 })).toBeNull()
    expect(parseFacts({ ...darwinJson, v: '1' })).toBeNull()
  })
  it('facts with derived.sessions_room or mem.pressure missing are null', () => {
    expect(parseFacts({ ...darwinJson, derived: drop(darwinJson.derived, 'sessions_room') })).toBeNull()
    expect(parseFacts({ ...darwinJson, mem: drop(darwinJson.mem, 'pressure') })).toBeNull()
  })
  it('a disk without total_bytes or avail_bytes is null facts', () => {
    expect(parseFacts({ ...darwinJson, disk: [drop(darwinJson.disk[0], 'total_bytes')] })).toBeNull()
  })
  it('the lists the wire may send as null (Go nil slices) read as empty', () => {
    const f = parseFacts({ ...darwinJson, disk: null, procs: null, residents: null, exceptions: null, tools: { ...darwinJson.tools, php: null } })!
    expect([f.disk, f.procs, f.residents, f.exceptions]).toEqual([[], [], [], []])
    expect(f.tools.php).toBeNull()
  })
})

describe('series', () => {
  const point = { at: '2026-10-18T05:00:00Z', zone: 'normal', cpu_idle_pct: 50, mem_available_mb: 900, pressure: 0, page_out_rate: null, disk_max_used_pct: null, headroom: { heavy: 1, light: 1 } }
  it('truncated must be a boolean', () => {
    expect(parseSeries({ step: '1m', points: [point] })).toBeNull()
    expect(parseSeries({ step: '1m', points: [point], truncated: 'no' })).toBeNull()
  })
  it('step must be 1m or 5s', () => {
    expect(parseSeries({ step: '1h', points: [point], truncated: false })).toBeNull()
    expect(parseSeries({ step: '5s', points: [point], truncated: true })!.truncated).toBe(true)
  })
  it('points may be null (an empty series)', () => {
    expect(parseSeries({ step: '1m', points: null, truncated: false })!.points).toEqual([])
  })

  it('points become ms; a malformed point is dropped', () => {
    const s = parseSeries({ step: '1m', truncated: false, points: [
      { at: '2026-10-18T05:00:00Z', zone: 'warning', cpu_idle_pct: 50, mem_available_mb: 900, pressure: 1, page_out_rate: null, disk_max_used_pct: 80.5, headroom: { heavy: 0, light: 1 } },
      { at: 'x' },
    ] })!
    expect(s.points).toEqual([{ at: Date.parse('2026-10-18T05:00:00Z'), zone: 'warning', cpu_idle_pct: 50, mem_available_mb: 900, pressure: 1, page_out_rate: null, disk_max_used_pct: 80.5, headroom: { heavy: 0, light: 1 } }])
    expect(s.step).toBe('1m')
    expect(s.truncated).toBe(false)
  })
})

describe('parseFactsHistory', () => {
  const rec = (at: string) => ({ ...darwinJson, at })
  it('reads the step and the records, oldest first as the daemon sends them', () => {
    const h = parseFactsHistory({ step: '5s', records: [rec('2026-10-18T05:00:00Z'), rec('2026-10-18T05:00:05Z')] })!
    expect(h.step).toBe('5s')
    expect(h.records.map((r) => r.at)).toEqual([Date.UTC(2026, 9, 18, 5, 0, 0), Date.UTC(2026, 9, 18, 5, 0, 5)])
  })
  it('a record that does not parse is dropped, not the whole answer', () => {
    const h = parseFactsHistory({ step: '5s', records: [rec('2026-10-18T05:00:00Z'), { nonsense: true }] })!
    expect(h.records).toHaveLength(1)
  })
  it('an empty list is an answer (wwd just started)', () => {
    expect(parseFactsHistory({ step: '1m', records: [] })).toEqual({ step: '1m', records: [] })
  })
  it('a step this App does not know, or no records list, is no answer', () => {
    expect(parseFactsHistory({ step: '1h', records: [] })).toBeNull()
    expect(parseFactsHistory({ step: '5s' })).toBeNull()
    expect(parseFactsHistory(null)).toBeNull()
  })
})

// MON-1b: the peer rows carry `liveness`, `liveness_at`, `liveness_since` (omitempty) and `rtt_ms`; the local row carries none of them.
describe('liveness fields (monitoring tabs spec A2)', () => {
  const row = (over: Record<string, unknown> = {}) => ({ ...hostsJson.hosts[0], ...over })
  const parse1 = (over: Record<string, unknown>) => parseHostsAnswer({ hosts: [row(over)] })![0]

  it('a row without the three fields (the local host, an older daemon) has none of them', () => {
    const h = parse1({})
    expect(h).not.toHaveProperty('liveness')
    expect(h).not.toHaveProperty('liveness_at')
    expect(h).not.toHaveProperty('liveness_since')
    expect(h).not.toHaveProperty('rtt_ms')
  })

  it('a peer row that is ok: the word and the check time (ms) are read', () => {
    const h = parse1({ liveness: 'ok', liveness_at: '2026-10-20T19:50:05+08:00', rtt_ms: 12 })
    expect(h).toMatchObject({ liveness: 'ok', liveness_at: Date.parse('2026-10-20T19:50:05+08:00'), rtt_ms: 12 })
  })

  it.each(['wwd_down', 'host_down', 'unknown'] as const)('%s is read as it is, with its since time', (liveness) => {
    const h = parse1({ liveness, liveness_at: '2026-10-20T20:00:00+08:00', liveness_since: '2026-10-20T19:40:00+08:00' })
    expect(h).toMatchObject({ liveness, liveness_at: Date.parse('2026-10-20T20:00:00+08:00'), liveness_since: Date.parse('2026-10-20T19:40:00+08:00') })
  })

  it('a word the App does not know reads as unknown, never as ok', () => {
    expect(parse1({ liveness: 'sleeping' }).liveness).toBe('unknown')
    expect(parse1({ liveness: 7 }).liveness).toBe('unknown')
  })

  it('a time that does not parse leaves that field out, the others stay', () => {
    const h = parse1({ liveness: 'wwd_down', liveness_at: 'yesterday', liveness_since: '2026-10-20T19:40:00+08:00' })
    expect(h).not.toHaveProperty('liveness_at')
    expect(h.liveness_since).toBe(Date.parse('2026-10-20T19:40:00+08:00'))
  })

  it('rtt_ms is carried for a non-negative number only', () => {
    expect(parse1({ rtt_ms: 0 }).rtt_ms).toBe(0)
    for (const bad of [-1, '5', null, Number.NaN]) expect(parse1({ rtt_ms: bad })).not.toHaveProperty('rtt_ms')
  })
})
