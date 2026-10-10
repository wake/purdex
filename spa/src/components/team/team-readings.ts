// spa/src/components/team/team-readings.ts — a seat's live readings (model, effort, context) for the team panel.
//
// Selected per seat from `useTeamRosterStore`, never carried by the structural team context (team-display.ts): a
// context-window update re-renders that seat's row only. The selector returns the store's own RosterSession object, so a
// frame that did not touch this seat does not re-render it.
import { useMemo } from 'react'
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
  /** The seat's host did not answer (`context_unavailable`): model and context are blank for that reason. */
  unavailable?: boolean
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
  return {
    ...(model ? { model } : raw ? { modelRaw: raw } : {}),
    ...(effort ? { effort } : {}),
    ...(typeof used === 'number' ? { ctx: Math.round(used) } : {}),
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

export function useSeatReading(teamKey: string, sessionId: string): SeatReading {
  const session = useRosterSession(teamKey, sessionId)
  return useMemo(() => readingOf(session), [session])
}
