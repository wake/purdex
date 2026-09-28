// spa/src/lib/nex/turn-footer.ts — the per-turn footer text (spec §7.2,
// F1–F3). Fixed English regardless of UI locale (F1): the footer is not
// translated and not a search unit, so its strings live here, not in
// locales/en.json / zh-TW.json.
import type { TurnMeta } from './event-reducer'

const SECOND = 1_000
const MINUTE = 60 * SECOND
const HOUR = 60 * MINUTE

/**
 * The footer's duration scale (spec §7.2): `<1s` under a second, `Ns` under a
 * minute, `Mm Ss` under an hour, else `Hh Mm`. Always floored and never
 * zero-padded — distinct from `formatDuration` (tool-timing badges, one
 * decimal, zero-padded, rounds).
 */
export function formatTurnDuration(ms: number): string {
  if (!Number.isFinite(ms) || ms < SECOND) return '<1s'
  if (ms < MINUTE) return `${Math.floor(ms / SECOND)}s`
  if (ms < HOUR) {
    const m = Math.floor(ms / MINUTE)
    const s = Math.floor((ms % MINUTE) / SECOND)
    return `${m}m ${s}s`
  }
  const h = Math.floor(ms / HOUR)
  const m = Math.floor((ms % HOUR) / MINUTE)
  return `${h}h ${m}m`
}

export interface TurnFooterResult {
  text: string
  tone: 'normal' | 'error'
  /** The hover title: full localised date and time (spec §7.2) — no date is in `text`. */
  title: string
}

// The title is always the full localised date/time; unlike the short `time`
// text (system 12/24h, F2) it is not injected — component tests only assert
// it contains a date, not an exact locale-dependent string.
const titleFormat = new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'medium' })

/**
 * The footer's text, tone and hover title for one turn, or null when the
 * turn draws no footer: a turn the user interrupted (F3), a turn with no
 * outcome yet, or one with no end time (the live turn).
 *
 * `fmt` formats `meta.endAt` (epoch ms) into the short completion time shown
 * in the text (F2, system 12/24h setting) — inject `(ms) => timeFormat.format(new Date(ms))`.
 * Duration always comes from `formatTurnDuration`, which is fixed and not injected.
 */
export function turnFooterText(meta: TurnMeta, fmt: (ms: number) => string): TurnFooterResult | null {
  if (meta.outcome === null || meta.outcome === 'interrupted' || meta.endAt === null) return null
  const time = fmt(meta.endAt)
  const title = titleFormat.format(new Date(meta.endAt))
  const duration = meta.durationMs !== null ? formatTurnDuration(meta.durationMs) : null
  if (meta.outcome === 'failed') {
    return { text: duration !== null ? `✻ Failed after ${duration} · ${time}` : `✻ Failed · ${time}`, tone: 'error', title }
  }
  return { text: duration !== null ? `✻ Worked for ${duration} · done ${time}` : `✻ Done · ${time}`, tone: 'normal', title }
}
