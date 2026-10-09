// spa/src/components/team/seat-flags.ts — what the panel says about a seat beyond its readings (cross-host teams, X5-App):
// a remote member in a transition (`joining` / `releasing` / `killing`) and a remote member whose host this Mac does not have.
import type { TeamSeatView } from './team-display'

const TRANSITIONS = ['joining', 'releasing', 'killing'] as const
export type SeatTransition = (typeof TRANSITIONS)[number]

/** The transition a seat is in, or null (`active`, and any value the wire may add later, read as active). */
export function transitionOf(seat: Pick<TeamSeatView, 'state'>): SeatTransition | null {
  return (TRANSITIONS as readonly string[]).includes(seat.state) ? (seat.state as SeatTransition) : null
}

/** A remote seat on a host this Mac has no verified entry for: drawn from the roster, never opened. */
export function notInApp(seat: Pick<TeamSeatView, 'remote' | 'hostId'>): boolean {
  return seat.remote && seat.hostId === ''
}
