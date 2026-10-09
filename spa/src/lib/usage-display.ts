// spa/src/lib/usage-display.ts — pure helpers behind the status bar's usage segments: reading Claude Code's
// statusLine payload (context window, 5-hour / weekly rate limits), the colour tone of a percentage, and the
// "resets in 2h13m" phrase. Field names are the real payload's (captured from a live daemon):
//   context_window.used_percentage
//   rate_limits.five_hour.{used_percentage, resets_at}   rate_limits.seven_day.{used_percentage, resets_at}
// `resets_at` is epoch SECONDS. Every field may be absent (rate_limits only appears after the first API response),
// so each is read defensively and a missing one is null, never 0.

export interface UsageWindow {
  /** 0–100. */
  pct: number
  /** Epoch milliseconds, or null when the source gave none. */
  resetsAtMs: number | null
}

export interface CcUsage {
  context: number | null
  fiveHour: UsageWindow | null
  sevenDay: UsageWindow | null
}

/** A snapshot older than this renders dimmed: the numbers may no longer hold. */
export const USAGE_STALE_MS = 10 * 60 * 1000

function isPct(v: unknown): v is number {
  return typeof v === 'number' && Number.isFinite(v)
}

/** Epoch seconds (Claude Code) or milliseconds (anything already past 1e11) → milliseconds. */
export function epochToMs(v: unknown): number | null {
  if (typeof v !== 'number' || !Number.isFinite(v) || v <= 0) return null
  return v > 1e11 ? v : v * 1000
}

function readWindow(raw: unknown): UsageWindow | null {
  if (typeof raw !== 'object' || raw === null) return null
  const w = raw as Record<string, unknown>
  if (!isPct(w.used_percentage)) return null
  return { pct: w.used_percentage, resetsAtMs: epochToMs(w.resets_at) }
}

export function parseCcUsage(raw: Record<string, unknown> | null | undefined): CcUsage | null {
  if (!raw) return null
  const ctx = (raw.context_window as Record<string, unknown> | undefined)?.used_percentage
  const limits = raw.rate_limits as Record<string, unknown> | undefined
  const usage: CcUsage = {
    context: isPct(ctx) ? ctx : null,
    fiveHour: readWindow(limits?.five_hour),
    sevenDay: readWindow(limits?.seven_day),
  }
  return usage.context === null && !usage.fiveHour && !usage.sevenDay ? null : usage
}

const clamp = (n: number, lo: number, hi: number) => Math.min(hi, Math.max(lo, n))

/** The one normalised used share: rounded, clamped to 0-100. Ring, tone, tooltip and remaining all derive from it. */
export function usedPct(used: number): number {
  return clamp(Math.round(used), 0, 100)
}

/** Remaining share shown as the number: 100 - used, rounded, clamped to 0-100. */
export function remainingPct(used: number): number {
  return 100 - usedPct(used)
}

export type UsageTone = 'ok' | 'warn' | 'danger'

/** Shifts at 70 and 90 of the USED share (inclusive). */
export function usageTone(pct: number): UsageTone {
  if (pct >= 90) return 'danger'
  if (pct >= 70) return 'warn'
  return 'ok'
}

/** "2h13m", "45m", "3d4h"; null once the reset has passed (the window already rolled over). */
export function formatResetsIn(resetsAtMs: number, nowMs: number): string | null {
  const mins = Math.floor((resetsAtMs - nowMs) / 60_000)
  if (mins < 0) return null
  if (mins < 60) return `${mins}m`
  const hours = Math.floor(mins / 60)
  if (hours < 24) return `${hours}h${String(mins % 60).padStart(2, '0')}m`
  return `${Math.floor(hours / 24)}d${hours % 24}h`
}
