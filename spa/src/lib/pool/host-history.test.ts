import { describe, it, expect } from 'vitest'
import { sampleOfFacts, sampleOfPoint } from './host-history'
import { parseFacts, parseSeries } from './parse'
import darwinJson from './__fixtures__/facts_darwin.json'

const facts = parseFacts(darwinJson)!
const seriesPoint = (extra: Record<string, unknown> = {}) => parseSeries({
  step: '1m', truncated: false,
  points: [{ at: '2026-10-18T05:00:00Z', zone: 'warning', cpu_idle_pct: 70, mem_available_mb: 4000, pressure: 2, page_out_rate: 3, disk_max_used_pct: 55, headroom: { heavy: 1, light: 2 }, ...extra }],
})!.points[0]

describe('sampleOfFacts', () => {
  it('every field comes from the full record; the disk is the fullest one', () => {
    const f = { ...facts, disk: [{ ...facts.disk[0], used_pct: 40, free_gb: 100 }, { ...facts.disk[0], mount: '/v', used_pct: 90, free_gb: 12 }] }
    const { detail: _detail, ...base } = sampleOfFacts(f)
    expect(base).toEqual({
      at: f.at, cpuPct: 100 - f.cpu.idle_pct, load1: f.cpu.load1, load5: f.cpu.load5, ncpu: f.cpu.ncpu,
      memUsedMb: f.mem.total_mb - f.mem.available_mb, memAvailMb: f.mem.available_mb, memTotalMb: f.mem.total_mb,
      pageOut: f.mem.page_out_rate, swapOut: f.mem.swap_out_rate, swapUsedMb: f.mem.swap_used_mb,
      diskUsedPct: 90, diskFreeGb: 12,
      runHeavy: f.worker.running.heavy, runLight: f.worker.running.light, slotHeavy: f.worker.slots.heavy, slotLight: f.worker.slots.light,
      workerOn: f.worker.enabled, zone: f.derived.zone,
    })
  })
  it('a missing reading is null, never 0', () => {
    const s = sampleOfFacts({ ...facts, disk: [], mem: { ...facts.mem, page_out_rate: null, swap_out_rate: null, swap_used_mb: null } })
    expect([s.diskUsedPct, s.diskFreeGb, s.pageOut, s.swapOut, s.swapUsedMb]).toEqual([null, null, null, null, null])
  })
})

describe('sampleOfPoint', () => {
  it('reads what the series has; slots and the switch are not there', () => {
    const s = sampleOfPoint(seriesPoint({ load1: 1.5, load5: 2, swap_used_mb: 10, swap_out_rate: 1, worker_running: { heavy: 1, light: 0 }, disks: [{ mount: '/', used_pct: 30, free_gb: 200 }, { mount: '/v', used_pct: 80, free_gb: 20 }] }), 16000, 10)
    const { detail: _detail, ...base } = s
    expect(base).toEqual({
      at: Date.parse('2026-10-18T05:00:00Z'), cpuPct: 30, load1: 1.5, load5: 2, ncpu: 10,
      memUsedMb: 12000, memAvailMb: 4000, memTotalMb: 16000, pageOut: 3, swapOut: 1, swapUsedMb: 10,
      diskUsedPct: 80, diskFreeGb: 20,
      runHeavy: 1, runLight: 0, slotHeavy: null, slotLight: null, workerOn: null, zone: 'warning',
    })
  })
  it('without disks the single figure is used and the free space is null; fields the point lacks are null', () => {
    const s = sampleOfPoint(seriesPoint(), null, null)
    expect(s).toMatchObject({ diskUsedPct: 55, diskFreeGb: null, load1: null, load5: null, swapUsedMb: null, swapOut: null, runHeavy: null, runLight: null, memUsedMb: null, memTotalMb: null, ncpu: null })
  })
})

// ---- The sample's detail (MON-7a): what the status tab and the full charts read beyond the mini charts' fields ----------------------------------------------------

type Rec = Record<string, unknown>
const clone = <T,>(v: T): T => JSON.parse(JSON.stringify(v)) as T
/** A record with every host-metrics field, written the way wwd writes it. */
const richFacts = () => {
  const raw = clone(darwinJson) as Rec
  Object.assign(raw.cpu as Rec, { load15: 1.5, user_pct: 12, system_pct: 6, cores: [{ user_pct: 10, system_pct: 5, idle_pct: 85, kind: 'p' }, { user_pct: null, system_pct: null, idle_pct: null, kind: null }] })
  Object.assign(raw.mem as Rec, { pressure: 2, compressor_mb: 300, decompress_rate: 11, swap_total_mb: 4096, psi: { some10: 1, full10: 2, some60: 3 } })
  Object.assign(raw, { queue: { queued: 4 }, procs_total: 500, boot_at: '2026-10-10T08:00:00Z', self: { cpu_pct: 1.5, rss_mb: 40 }, disk_io: [{ device: 'disk0', read_bps: 1, write_bps: 2, read_iops: 3, write_iops: 4 }] })
  raw.residents = [{ session: 'a', kind: 'claude', count: 2, mem_mb: 300 }, { session: 'b', kind: 'codex', count: 1, mem_mb: 100 }]
  return parseFacts(raw)!
}

describe('sampleOfFacts: detail', () => {
  it('carries every figure the full record has', () => {
    const f = richFacts()
    expect(sampleOfFacts(f).detail).toEqual({
      load15: 1.5, userPct: 12, systemPct: 6,
      cores: [{ user: 10, system: 5, idle: 85, kind: 'p' }, { user: null, system: null, idle: null, kind: null }],
      level: 2, psi: { some10: 1, full10: 2, some60: 3 }, compressorMb: 300, decompressRate: 11, swapTotalMb: 4096,
      disks: f.disk.map((d) => ({ mount: d.mount, usedPct: d.used_pct, freeGb: d.free_gb })),
      diskIO: [{ device: 'disk0', readBps: 1, writeBps: 2, readIops: 3, writeIops: 4 }],
      headroomHeavy: f.derived.headroom.heavy, headroomLight: f.derived.headroom.light, queued: 4, sessionsRoom: f.derived.sessions_room,
      procsListed: f.procs.length, residents: 3, residentsMemMb: 400, procsTotal: 500, selfCpuPct: 1.5, selfRssMb: 40, bootAt: Date.parse('2026-10-10T08:00:00Z'),
    })
  })

  it('a pressure of 0 (unknown) is null, not 0', () => {
    const f = richFacts()
    expect(sampleOfFacts({ ...f, mem: { ...f.mem, pressure: 0 } }).detail?.level).toBeNull()
  })

  it('an old record, with none of the host-metrics fields, has those figures null — not 0', () => {
    const d = sampleOfFacts(facts).detail!
    for (const k of ['load15', 'userPct', 'systemPct', 'cores', 'decompressRate', 'swapTotalMb', 'diskIO', 'procsTotal', 'selfCpuPct', 'selfRssMb', 'bootAt'] as const) expect(d[k], k).toBeNull()
    expect(d.queued).toBeNull() // no queue block either
  })
})

describe('sampleOfPoint: detail', () => {
  const rich = () => seriesPoint({
    pressure: 2, load15: 1.2, cpu_user_pct: 10, cpu_system_pct: 5, compressor_mb: 300, decompress_rate: 11, swap_total_mb: 4096, procs_total: 500, self_cpu_pct: 1.5, self_rss_mb: 40, queue_queued: 4,
    disks: [{ mount: '/', used_pct: 30, free_gb: 200 }], disk_io: [{ device: 'disk0', read_bps: 1, write_bps: 2, read_iops: 3, write_iops: 4 }],
  })

  it('carries what the series has', () => {
    expect(sampleOfPoint(rich(), 16000, 10).detail).toMatchObject({
      load15: 1.2, userPct: 10, systemPct: 5, level: 2, compressorMb: 300, decompressRate: 11, swapTotalMb: 4096, procsTotal: 500, selfCpuPct: 1.5, selfRssMb: 40, queued: 4,
      disks: [{ mount: '/', usedPct: 30, freeGb: 200 }], diskIO: [{ device: 'disk0', readBps: 1, writeBps: 2, readIops: 3, writeIops: 4 }], headroomHeavy: 1, headroomLight: 2,
    })
  })

  it('what a series does not carry is null: cores, pressure stall, sessions room, residents, listed processes, boot time', () => {
    const d = sampleOfPoint(rich(), 16000, 10).detail!
    for (const k of ['cores', 'psi', 'sessionsRoom', 'procsListed', 'residents', 'residentsMemMb', 'bootAt'] as const) expect(d[k], k).toBeNull()
  })

  it('a pressure of 0 is null; an old point has the new figures null, not 0', () => {
    const d = sampleOfPoint(seriesPoint({ pressure: 0 }), 16000, 10).detail!
    expect(d.level).toBeNull()
    for (const k of ['load15', 'userPct', 'systemPct', 'compressorMb', 'decompressRate', 'swapTotalMb', 'diskIO', 'procsTotal', 'selfCpuPct', 'selfRssMb', 'queued'] as const) expect(d[k], k).toBeNull()
  })
})
