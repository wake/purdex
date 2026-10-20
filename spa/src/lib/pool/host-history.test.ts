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
    expect(sampleOfFacts(f)).toEqual({
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
    expect(s).toEqual({
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
