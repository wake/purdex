// spa/src/lib/team/approval-unanswered.ts — when an open card counts as "nobody answered it" (relay v2 plan, App contract 3,
// `relay.unanswered.v1`). The daemon sends NO event op when the last card runs out, so a card an App holds from `opened` never
// gets `unanswered_at`; the session's pause is the live signal. A card is unanswered when
//   (a) the row carries `unanswered_at > 0`, or
//   (b) it is past `deadline_at` AND this App's pause data — the paused list or a roster row, on the card's OWN host — shows the
//       card's session (`origin.session_id`) holding `relay_unanswered`.
// Past the deadline with no pause data yet, the card is left as it was: nothing is guessed.
import type { ApprovalEntry } from '../../stores/useApprovalStore'
import { usePausedStore, type PausedHostEntry } from '../../stores/usePausedStore'
import { useTeamRosterStore } from '../../stores/useTeamRosterStore'
import { isUnanswered, type Approval } from './types'
import type { TeamRoster } from './roster'

/** The pause data of every host: `usePausedStore.byHost` and `useTeamRosterStore.byHost`. */
export interface PauseSources {
  paused: Readonly<Record<string, PausedHostEntry>>
  roster: Readonly<Record<string, readonly TeamRoster[]>>
}

/** Does `sessionId`, on `hostId`, hold the pause cause `relay_unanswered` in either source? Other hosts' data never counts. */
export function sessionHoldsUnanswered(hostId: string, sessionId: string, src: PauseSources): boolean {
  if (src.paused[hostId]?.sessions.some((p) => p.session_id === sessionId && p.causes.includes('relay_unanswered'))) return true
  return (src.roster[hostId] ?? []).some((t) =>
    [t.lead, ...t.members].some((m) => m.session_id === sessionId && m.paused?.causes.includes('relay_unanswered') === true))
}

/** Rule (a) or (b) above; `now` is unix ms. */
export function isCardUnanswered(hostId: string, a: Approval, now: number, src: PauseSources): boolean {
  if (isUnanswered(a)) return true
  return now >= a.deadline_at && sessionHoldsUnanswered(hostId, a.origin.session_id, src)
}

/** The deadline the pill counts down to (U22 (b)): the smallest `deadline_at` across hosts, skipping unanswered cards (their
 *  deadline is a sentinel or already passed). Null when nothing is open, or when only unanswered cards are. */
export function nearestDeadline(entries: Readonly<Record<string, ApprovalEntry>>, now: number, src: PauseSources): number | null {
  let nearest: number | null = null
  for (const e of Object.values(entries)) {
    if (isCardUnanswered(e.hostId, e.approval, now, src)) continue
    if (nearest === null || e.approval.deadline_at < nearest) nearest = e.approval.deadline_at
  }
  return nearest
}

/** The pause data, live. */
export function usePauseSources(): PauseSources {
  const paused = usePausedStore((s) => s.byHost)
  const roster = useTeamRosterStore((s) => s.byHost)
  return { paused, roster }
}

/** Is this open card unanswered right now? Re-renders when the pause data changes; the caller's ticking `now` covers the deadline. */
export function useCardUnanswered(hostId: string, a: Approval, now: number): boolean {
  return isCardUnanswered(hostId, a, now, usePauseSources())
}
