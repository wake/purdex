// spa/src/lib/pool/bar-readings.ts — what each bar of a fleet host cell's two-bar indicator shows (「主機格指示」 = 雙柱, the
// user 10-16 on #3033): a fill share and a band, with the ring's bands (70 warn, 90 danger) and rounding.
//
// Why the pressure level is offered over memory used %: on macOS the used % sits at 70–90 % most of the time (file cache), so
// a memory-% bar is yellow or red all day; the pressure level (what Activity Monitor shows, and what the gate and the watcher
// act on) rises only when the machine starts paging. A Linux host reports the level from PSI; without PSI it sends 0
// (unknown) and the bar falls back to memory used %.
import { usedPct, usageTone, type UsageTone } from '../usage-display'

/** One bar's reading: its fill share (0–100) and its band. */
export interface BarReading {
  pct: number
  tone: UsageTone
}

/** A used share, rounded and clamped like the ring's, banded at 70 / 90 like the ring's. */
function shareReading(v: number): BarReading {
  const pct = usedPct(v)
  return { pct, tone: usageTone(pct) }
}

export const cpuReading = (cpu: number): BarReading => shareReading(cpu)

export const memoryReading = (mem: number): BarReading => shareReading(mem)

/** Memory pressure level 1 / 2 / 4 -> a third ok / two thirds warn / full danger; 0 (unknown) -> memory used %. */
export function pressureReading(level: number, mem: number): BarReading {
  if (level >= 4) return { pct: 100, tone: 'danger' }
  if (level >= 2) return { pct: 200 / 3, tone: 'warn' }
  if (level >= 1) return { pct: 100 / 3, tone: 'ok' }
  return memoryReading(mem)
}
