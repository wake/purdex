// spa/src/lib/pool/indicator.ts — what each item of a host's sidebar indicator reads and how it is coloured (monitoring tabs spec M8, M9). Seven items: the composite load, CPU, memory, memory pressure, system load, disk, slots in use.
// The thresholds are the ring's and the old bars' (`bar-readings.ts`); the others are new. A host without readings (any state but ok) has none: null.
import type { UsageTone } from '../usage-display'
import { usedPct, usageTone } from '../usage-display'
import { cpuReading, memoryReading, pressureReading } from './bar-readings'
import type { CellView } from './cell-model'
import type { IndicatorItem } from '../../stores/usePoolHostsStore'

/** A fill share (0–100) and its band; `muted` is grey: an unknown zone, a worker that is off. */
export interface IndicatorReading {
  pct: number
  tone: UsageTone | 'muted'
}

const DISK_WARN = 85
const DISK_DANGER = 95

const finite = (v: number): number => (Number.isFinite(v) ? v : 0)

export function indicatorReading(item: IndicatorItem, v: Pick<CellView, 'fast' | 'worker' | 'zone'>): IndicatorReading | null {
  if (item === 'slots') {
    if (!v.worker) return null
    if (!v.worker.enabled) return { pct: 0, tone: 'muted' }
    const total = v.worker.slots.heavy + v.worker.slots.light
    if (total <= 0) return { pct: 0, tone: 'ok' }
    const used = v.worker.running.heavy + v.worker.running.light
    const pct = usedPct((used / total) * 100)
    return { pct, tone: used >= total ? 'warn' : 'ok' }
  }
  const f = v.fast
  if (!f) return null
  switch (item) {
    case 'load': {
      const tone = v.zone === 'warning' ? 'warn' : v.zone === 'danger' ? 'danger' : v.zone === 'unknown' || v.zone === null ? 'muted' : 'ok'
      return { pct: usedPct(f.load), tone }
    }
    case 'cpu': return cpuReading(f.cpu)
    case 'mem': return memoryReading(f.mem)
    case 'pressure': return pressureReading(f.level, f.mem)
    case 'disk': {
      const pct = usedPct(f.disk)
      return { pct, tone: pct >= DISK_DANGER ? 'danger' : pct >= DISK_WARN ? 'warn' : 'ok' }
    }
    case 'sysload': {
      const pct = f.ncpu > 0 ? usedPct((finite(f.load1) / f.ncpu) * 100) : 0
      // 70 warn, 100 danger (the share is clamped to 100, so danger is exactly "all cores' worth")
      return { pct, tone: pct >= 100 ? 'danger' : usageTone(pct) === 'ok' ? 'ok' : 'warn' }
    }
  }
}
