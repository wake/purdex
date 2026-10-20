// The host-metrics fields (wwd host-metrics spec §2; G-2 cpu split and cores, G-3 memory breakdown, G-4 processes/boot/self/disk I/O), on facts and on series points, and `info.fingerprint` (MON-7a). Every one is optional on the App's side:
// a field that is absent or does not parse is left out and the rest of the record stays. The literals follow the shape of Go's internal/wwd/wire (cpu_cores_test.go, diskio_test.go, host_g4_test.go).
import { describe, it, expect } from 'vitest'
import { parseFacts, parseHostsAnswer, parseSeries } from './parse'
import darwinJson from './__fixtures__/facts_darwin.json'
import linuxJson from './__fixtures__/facts_linux.json'
import hostsJson from './__fixtures__/pool_hosts_response.json'

const clone = <T,>(v: T): T => JSON.parse(JSON.stringify(v)) as T
type Rec = Record<string, unknown>

const core = (kind: string | null, over: Rec = {}) => ({ user_pct: 10, system_pct: 5, idle_pct: 85, kind, ...over })
/** The fixture with the host-metrics fields written the way wwd writes them. */
function withMetrics(base: unknown, over: Rec = {}): Rec {
  const f = clone(base) as Rec
  const cpu = f.cpu as Rec
  Object.assign(cpu, { load15: 1.5, user_pct: 12.5, system_pct: 6, cores: [core('p'), core('p', { user_pct: null }), core('e'), core('e')] })
  const mem = f.mem as Rec
  Object.assign(mem, { decompress_rate: 120.5, swap_total_mb: 4096, wired_mb: 2000, active_mb: 3000, inactive_mb: 1500, file_cache_mb: 900 })
  Object.assign(f, {
    procs_total: 512, boot_at: '2026-10-10T08:00:00Z', self: { cpu_pct: 1.5, rss_mb: 40 },
    disk_io: [{ device: 'disk0', read_bps: 1000, write_bps: 2000, read_iops: 10, write_iops: 20 }],
  }, over)
  return f
}

describe('facts: the host-metrics fields', () => {
  it('a darwin record with all of them (cores of both kinds)', () => {
    const f = parseFacts(withMetrics(darwinJson))!
    expect(f.cpu.load15).toBe(1.5)
    expect(f.cpu.user_pct).toBe(12.5)
    expect(f.cpu.system_pct).toBe(6)
    expect(f.cpu.cores).toEqual([
      { user_pct: 10, system_pct: 5, idle_pct: 85, kind: 'p' },
      { user_pct: null, system_pct: 5, idle_pct: 85, kind: 'p' },
      { user_pct: 10, system_pct: 5, idle_pct: 85, kind: 'e' },
      { user_pct: 10, system_pct: 5, idle_pct: 85, kind: 'e' },
    ])
    expect(f.mem).toMatchObject({ decompress_rate: 120.5, swap_total_mb: 4096, wired_mb: 2000, active_mb: 3000, inactive_mb: 1500, file_cache_mb: 900 })
    expect(f.procs_total).toBe(512)
    expect(f.boot_at).toBe(Date.parse('2026-10-10T08:00:00Z'))
    expect(f.self).toEqual({ cpu_pct: 1.5, rss_mb: 40 })
    expect(f.disk_io).toEqual([{ device: 'disk0', read_bps: 1000, write_bps: 2000, read_iops: 10, write_iops: 20 }])
  })

  it('a linux record: core kinds all null, no wired figure', () => {
    const raw = withMetrics(linuxJson)
    ;(raw.cpu as Rec).cores = [core(null), core(null)]
    delete (raw.mem as Rec).wired_mb
    const f = parseFacts(raw)!
    expect(f.cpu.cores?.map((c) => c.kind)).toEqual([null, null])
    expect(f.mem.wired_mb).toBeUndefined()
    expect(f.mem.file_cache_mb).toBe(900)
  })

  it('the stored fixtures still parse, every new field absent and the rest as before', () => {
    for (const raw of [darwinJson, linuxJson]) {
      const f = parseFacts(raw)!
      expect(f).not.toBeNull()
      for (const k of ['load15', 'user_pct', 'system_pct', 'cores']) expect(f.cpu).not.toHaveProperty(k)
      for (const k of ['decompress_rate', 'swap_total_mb', 'wired_mb', 'active_mb', 'inactive_mb', 'file_cache_mb']) expect(f.mem).not.toHaveProperty(k)
      for (const k of ['procs_total', 'boot_at', 'self', 'disk_io']) expect(f).not.toHaveProperty(k)
      expect(f.cpu).toEqual({ ncpu: f.cpu.ncpu, load1: f.cpu.load1, load5: f.cpu.load5, idle_pct: f.cpu.idle_pct })
    }
  })

  it('one core that does not parse takes the whole core list out (a shorter list would shift the core numbers); the rest stays', () => {
    for (const bad of [core('x'), core('p', { user_pct: 'a' }), 'nope']) {
      const raw = withMetrics(darwinJson)
      ;(raw.cpu as Rec).cores = [core('p'), bad, core('e')]
      const f = parseFacts(raw)!
      expect(f).not.toBeNull()
      expect(f.cpu.cores).toBeUndefined()
      expect(f.cpu.load15).toBe(1.5)
      expect(f.procs_total).toBe(512)
    }
  })

  it('a disk I/O entry without a device is dropped alone', () => {
    const raw = withMetrics(darwinJson, { disk_io: [{ read_bps: 1, write_bps: 2, read_iops: 3, write_iops: 4 }, { device: 'disk1', read_bps: null, write_bps: 5, read_iops: null, write_iops: 6 }] })
    const f = parseFacts(raw)!
    expect(f.disk_io).toEqual([{ device: 'disk1', read_bps: null, write_bps: 5, read_iops: null, write_iops: 6 }])
  })

  it('processes in total is a non-negative whole number, else absent', () => {
    for (const bad of [-1, 1.5, '5', null]) expect(parseFacts(withMetrics(darwinJson, { procs_total: bad }))!.procs_total, String(bad)).toBeUndefined()
    expect(parseFacts(withMetrics(darwinJson, { procs_total: 0 }))!.procs_total).toBe(0)
  })

  it('a boot time that is not a time is absent; the record stays', () => {
    for (const bad of ['yesterday', 5, null]) {
      const f = parseFacts(withMetrics(darwinJson, { boot_at: bad }))!
      expect(f).not.toBeNull()
      expect(f.boot_at).toBeUndefined()
    }
  })

  it('wwd\'s own use: a string is absent; null figures stay null', () => {
    expect(parseFacts(withMetrics(darwinJson, { self: 'x' }))!.self).toBeUndefined()
    expect(parseFacts(withMetrics(darwinJson, { self: { cpu_pct: null, rss_mb: 40 } }))!.self).toEqual({ cpu_pct: null, rss_mb: 40 })
    expect(parseFacts(withMetrics(darwinJson, { self: { cpu_pct: 'a', rss_mb: 40 } }))!.self).toEqual({ cpu_pct: null, rss_mb: 40 })
  })

  it('a memory figure that is not a number is absent', () => {
    const raw = withMetrics(darwinJson)
    Object.assign(raw.mem as Rec, { wired_mb: 'lots', swap_total_mb: null })
    const f = parseFacts(raw)!
    expect(f.mem.wired_mb).toBeUndefined()
    expect(f.mem.swap_total_mb).toBeUndefined()
    expect(f.mem.active_mb).toBe(3000)
  })
})

describe('series: the host-metrics fields on a point', () => {
  const point = (over: Rec = {}) => ({
    at: '2026-10-21T01:00:00Z', zone: 'normal', cpu_idle_pct: 80, mem_available_mb: 4000, pressure: 1, page_out_rate: 0, disk_max_used_pct: 50, headroom: { heavy: 1, light: 2 },
    load15: 1.2, cpu_user_pct: 10, cpu_system_pct: 5, compressor_mb: 300, decompress_rate: 11, swap_total_mb: 4096, procs_total: 500, self_cpu_pct: 1.5, self_rss_mb: 40,
    disk_io: [{ device: 'disk0', read_bps: 1, write_bps: 2, read_iops: 3, write_iops: 4 }], ...over,
  })
  const parse = (p: Rec) => parseSeries({ step: '5s', truncated: false, points: [p] })!.points[0]

  it('a point with every one of them', () => {
    expect(parse(point())).toMatchObject({
      load15: 1.2, cpu_user_pct: 10, cpu_system_pct: 5, compressor_mb: 300, decompress_rate: 11, swap_total_mb: 4096, procs_total: 500, self_cpu_pct: 1.5, self_rss_mb: 40,
      disk_io: [{ device: 'disk0', read_bps: 1, write_bps: 2, read_iops: 3, write_iops: 4 }],
    })
  })

  it('a field that is null or not a number is absent; the point stays', () => {
    const p = parse(point({ load15: null, self_cpu_pct: 'x', compressor_mb: undefined }))
    expect(p).toBeDefined()
    expect(p).not.toHaveProperty('load15')
    expect(p).not.toHaveProperty('self_cpu_pct')
    expect(p).not.toHaveProperty('compressor_mb')
    expect(p.cpu_user_pct).toBe(10)
    expect(p.self_rss_mb).toBe(40)
  })

  it('a disk I/O entry that does not parse is dropped alone', () => {
    const p = parse(point({ disk_io: [{ device: 'a', read_bps: 1, write_bps: 2, read_iops: 3, write_iops: 4 }, { read_bps: 1 }, 'x'] }))
    expect(p.disk_io).toEqual([{ device: 'a', read_bps: 1, write_bps: 2, read_iops: 3, write_iops: 4 }])
  })

  it('an old point has none of them', () => {
    const old = parse({ at: '2026-10-21T01:00:00Z', zone: 'normal', cpu_idle_pct: 80, mem_available_mb: 4000, pressure: 1, page_out_rate: 0, disk_max_used_pct: 50, headroom: { heavy: 1, light: 2 } })
    for (const k of ['load15', 'cpu_user_pct', 'cpu_system_pct', 'compressor_mb', 'decompress_rate', 'swap_total_mb', 'procs_total', 'self_cpu_pct', 'self_rss_mb', 'disk_io']) expect(old, k).not.toHaveProperty(k)
  })
})

describe('host: info.fingerprint', () => {
  const rowWith = (fingerprint: unknown) => {
    const answer = clone(hostsJson) as { hosts: Array<{ info: Rec }> }
    answer.hosts[0].info.fingerprint = fingerprint
    return parseHostsAnswer(answer)![0]
  }

  it('keeps the string values', () => {
    expect(rowWith({ cpu: 'Apple M4', ram: '16 GB' }).fingerprint).toEqual({ cpu: 'Apple M4', ram: '16 GB' })
  })

  it('drops a value that is not a string, keeps the others', () => {
    expect(rowWith({ cpu: 'Apple M4', cores: 10, x: null, y: ['a'] }).fingerprint).toEqual({ cpu: 'Apple M4' })
  })

  it('an empty object, nothing usable, or no fingerprint at all: absent', () => {
    expect(rowWith({})).not.toHaveProperty('fingerprint')
    expect(rowWith({ cores: 10 })).not.toHaveProperty('fingerprint')
    expect(rowWith('x')).not.toHaveProperty('fingerprint')
    expect(rowWith(undefined)).not.toHaveProperty('fingerprint')
  })
})
