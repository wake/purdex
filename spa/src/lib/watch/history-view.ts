// spa/src/lib/watch/history-view.ts — the 歷史 body's arithmetic (watcher area spec §6.1): one gate record → a row's times, bar parts and
// usage columns. Pure; the words and units are the component's (i18n).
import type { GateDecision } from './decisions-types'

/** The window the 歷史 body reads: the last 3 hours. */
export const HISTORY_WINDOW_MS = 3 * 3600_000
/** A wait shorter than this reads 「未等」 and has no hatched part. */
export const NO_WAIT_S = 3
/** A wait past this turns warning. */
export const LONG_WAIT_S = 60
/** The page-fault count past which 換頁拖累 turns error. */
export const HEAVY_FAULTS = 5000

/** A command that never reached its process: the gate refused it, or it was interrupted while waiting. */
const NEVER_STARTED = new Set(['refused', 'interrupted'])

export interface HistoryRow {
  key: string
  /** Epoch ms: when the gate started waiting. */
  arriveMs: number
  startMs: number
  endMs: number
  cls: string
  command: string
  project: string
  session: string
  waitS: number
  runS: number
  /** False: only waited, never ran (no 「跑」 part, no exit code, no usage). */
  started: boolean
  failed: boolean
  exit: number
  cores: number | null
  maxRssMb: number | null
  majorFaults: number | null
  record: GateDecision
}

const basename = (p: string): string => p.replace(/\/+$/, '').split('/').pop() ?? ''

export function toRow(r: GateDecision, i: number): HistoryRow {
  const started = !NEVER_STARTED.has(r.outcome)
  const arriveMs = Date.parse(r.time)
  // `duration_s` runs from when the gate began waiting to the end, so it already holds the wait: the run is what is left.
  const runS = started ? Math.max(0, r.durationS - r.waitS) : 0
  const startMs = arriveMs + r.waitS * 1000
  return {
    key: `${r.time}|${r.session ?? ''}|${i}`,
    arriveMs, startMs, endMs: arriveMs + r.durationS * 1000,
    cls: r.class ?? '',
    command: r.argv.join(' '),
    project: r.cwd ? basename(r.cwd) : '',
    session: r.session ?? '',
    waitS: r.waitS, runS, started,
    failed: started && r.exit !== 0,
    exit: r.exit,
    cores: started && r.cpuS !== undefined && runS > 0 ? r.cpuS / runS : null,
    maxRssMb: started ? r.maxRssMb ?? null : null,
    majorFaults: started ? r.majorFaults ?? null : null,
    record: r,
  }
}

/** Rows newest first by the time they were queued (the daemon already sends them so; this keeps it true for the bar's table). */
export const toRows = (records: GateDecision[]): HistoryRow[] => records.map(toRow).sort((a, b) => b.arriveMs - a.arriveMs)

/** The shared bar scale in minutes: the longest wait + run rounded up to a multiple of 5, at least 5. Every row uses it. */
export function scaleMinutes(rows: HistoryRow[]): number {
  const longest = rows.reduce((m, r) => Math.max(m, r.waitS + r.runS), 0)
  return Math.max(5, Math.ceil(longest / 60 / 5) * 5)
}

/** The bar's parts as fractions of the full bar (0..1), on the shared scale. */
export function barParts(r: HistoryRow, scaleMin: number): { wait: number; run: number } {
  const full = scaleMin * 60
  return { wait: r.waitS >= NO_WAIT_S ? Math.min(1, r.waitS / full) : 0, run: Math.min(1, r.runS / full) }
}
