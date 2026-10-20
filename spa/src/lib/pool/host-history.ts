// spa/src/lib/pool/host-history.ts — what the host page remembers of a host's readings, from the pool only (monitoring tabs spec M43, plan MON-6c). One reading
// is a `HostSample`; two sources make it: wwd's full 5 s record and the daemon's 1 minute series point. A field the source does not say is null, never 0 (a 0 reads as
// "measured, and nothing"). The memory is per `host_id`, module-level, never persisted.
import type { PoolFacts, PoolZone, SeriesPoint } from './types'

export interface HostSample {
  at: number
  cpuPct: number | null
  load1: number | null
  load5: number | null
  ncpu: number | null
  memUsedMb: number | null
  memAvailMb: number | null
  memTotalMb: number | null
  pageOut: number | null
  swapOut: number | null
  swapUsedMb: number | null
  diskUsedPct: number | null
  diskFreeGb: number | null
  runHeavy: number | null
  runLight: number | null
  slotHeavy: number | null
  slotLight: number | null
  workerOn: boolean | null
  zone: PoolZone | null
}

/** The sample of a full 5 s record; the disk is the one with the highest `used_pct`. */
export function sampleOfFacts(f: PoolFacts): HostSample {
  const fullest = f.disk.reduce<PoolFacts['disk'][number] | null>((best, d) => (best === null || d.used_pct > best.used_pct ? d : best), null)
  return {
    at: f.at, cpuPct: 100 - f.cpu.idle_pct, load1: f.cpu.load1, load5: f.cpu.load5, ncpu: f.cpu.ncpu,
    memUsedMb: f.mem.total_mb - f.mem.available_mb, memAvailMb: f.mem.available_mb, memTotalMb: f.mem.total_mb,
    pageOut: f.mem.page_out_rate, swapOut: f.mem.swap_out_rate, swapUsedMb: f.mem.swap_used_mb,
    diskUsedPct: fullest?.used_pct ?? null, diskFreeGb: fullest?.free_gb ?? null,
    runHeavy: f.worker.running.heavy, runLight: f.worker.running.light, slotHeavy: f.worker.slots.heavy, slotLight: f.worker.slots.light,
    workerOn: f.worker.enabled, zone: f.derived.zone,
  }
}

/**
 * The sample of a series point. The series carries only what is available of memory, so the used figure needs the host's total; `ncpu` is the host's constant.
 * The series has no slots and no worker switch: those are null.
 */
export function sampleOfPoint(p: SeriesPoint, memTotalMb: number | null, ncpu: number | null): HostSample {
  const total = memTotalMb !== null && memTotalMb > 0 ? memTotalMb : null
  const fullest = (p.disks ?? []).reduce<NonNullable<SeriesPoint['disks']>[number] | null>((best, d) => (best === null || d.used_pct > best.used_pct ? d : best), null)
  return {
    at: p.at, cpuPct: 100 - p.cpu_idle_pct, load1: p.load1 ?? null, load5: p.load5 ?? null, ncpu,
    memUsedMb: total === null ? null : total - p.mem_available_mb, memAvailMb: p.mem_available_mb, memTotalMb: total,
    pageOut: p.page_out_rate, swapOut: p.swap_out_rate ?? null, swapUsedMb: p.swap_used_mb ?? null,
    diskUsedPct: fullest?.used_pct ?? p.disk_max_used_pct, diskFreeGb: fullest?.free_gb ?? null,
    runHeavy: p.worker_running?.heavy ?? null, runLight: p.worker_running?.light ?? null, slotHeavy: null, slotLight: null, workerOn: null, zone: p.zone,
  }
}
