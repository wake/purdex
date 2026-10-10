// spa/src/components/team/team-readings.ts — a seat's live readings (model, effort, context) for the team panel.
//
// Selected per seat from `useTeamRosterStore`, never carried by the structural team context (team-display.ts): a
// context-window update re-renders that seat's row only. The selector returns the store's own RosterSession object, so a
// frame that did not touch this seat does not re-render it.
import { useMemo } from 'react'
import { useCoarseNow } from '../../hooks/useCoarseNow'
import { useTeamRosterStore } from '../../stores/useTeamRosterStore'
import type { RosterSession } from '../../lib/team/roster'
import { remainingPct } from '../../lib/usage-display'
import { familyOf, type ModelFamily } from './model-family'

export interface SeatReading {
  model?: ModelFamily
  /** The model string as reported, when it names no known family. */
  modelRaw?: string
  effort?: string
  /** Context used, 0-100 whole percent; undefined when unknown. */
  ctx?: number
  /**
   * When the seat's own host took this reading (`context.at`, unix ms): its daemon stamps it when the statusline arrives
   * (internal/module/agent/context_usage.go), keeps the original stamp across a restart, and the lead host copies it unchanged
   * — so it is that host's clock, which may differ from ours. Absent = unknown: the roster carries none, or a value that is
   * not a time (0, negative, not finite). #2410: a reading restored after a member host's restart, or an idle session's last
   * one, is drawn like a live one; this is what a freshness policy reads.
   */
  at?: number
  /** The seat's host did not answer (`context_unavailable`): model and context are blank for that reason. */
  unavailable?: boolean
}

/**
 * How old a reading may get before `isStale` says so. PENDING THE USER'S DECISION (#2410): the value, and what the panel
 * does with a stale reading (dim, hide, print its age), are not settled. Nothing draws from this yet.
 */
export const READING_STALE_AFTER_MS = 30 * 60_000

/** The reading's age in ms at `now`; undefined when its time is unknown. A time in the future (clock skew between hosts) is age 0: as new as it gets. */
export function readingAge(reading: Pick<SeatReading, 'at'>, now: number): number | undefined {
  return reading.at === undefined ? undefined : Math.max(0, now - reading.at)
}

/**
 * Whether the reading is older than `thresholdMs` at `now` (strictly: exactly the threshold is still fresh). A reading of
 * unknown time is NOT stale — it cannot be told, so it is drawn as it was before the policy existed; likewise a time in
 * the future.
 */
export function isStale(reading: Pick<SeatReading, 'at'>, now: number, thresholdMs: number = READING_STALE_AFTER_MS): boolean {
  const age = readingAge(reading, now)
  return age !== undefined && age > thresholdMs
}

/** A team key is `<hostId>\0<teamId>` (team-views `teamKeyOf`). */
export function useRosterSession(teamKey: string, sessionId: string): RosterSession | undefined {
  return useTeamRosterStore((s) => {
    const cut = teamKey.indexOf('\u0000')
    const team = s.byHost[teamKey.slice(0, cut)]?.find((t) => t.id === teamKey.slice(cut + 1))
    if (!team) return undefined
    return team.lead.session_id === sessionId ? team.lead : team.members.find((m) => m.session_id === sessionId)
  })
}

export function readingOf(s: RosterSession | undefined): SeatReading {
  if (!s) return {}
  const effort = s.effort || s.context?.effort || undefined
  // Its host did not answer: whatever model / context the row still carries is not a reading, so none is drawn.
  if (s.context_unavailable === true) return { unavailable: true, ...(effort ? { effort } : {}) }
  const raw = s.model || s.context?.model_id || undefined
  const model = familyOf(raw)
  const used = s.context?.used_percentage
  const at = s.context?.at
  return {
    ...(model ? { model } : raw ? { modelRaw: raw } : {}),
    ...(effort ? { effort } : {}),
    ...(typeof used === 'number' ? { ctx: Math.round(used) } : {}),
    ...(typeof at === 'number' && Number.isFinite(at) && at > 0 ? { at } : {}),
  }
}

/** The number the panel prints for context: what is LEFT (the status bar's rule, usage-display remainingPct); the ring still draws the used share. */
export function ctxLeftText(ctx: number | undefined): string {
  return ctx === undefined ? '—' : `${remainingPct(ctx)}%`
}

/** The tooltip phrase for context: 「context 剩 60%」 / "context 60% left"; a missing value is 「context —」, never 0. */
export function ctxTip(ctx: number | undefined, t: (key: string, params?: Record<string, string | number>) => string): string {
  return ctx === undefined ? `${t('team.panel.context')} —` : t('team.panel.context_left', { pct: remainingPct(ctx) })
}

/** A reading and whether it is stale (#2410). `stale` is judged against the minute (useCoarseNow); no component reads it yet. */
export type SeatReadingView = SeatReading & { stale: boolean }

export function useSeatReading(teamKey: string, sessionId: string): SeatReadingView {
  const session = useRosterSession(teamKey, sessionId)
  const now = useCoarseNow()
  return useMemo(() => {
    const reading = readingOf(session)
    return { ...reading, stale: isStale(reading, now) }
  }, [session, now])
}
