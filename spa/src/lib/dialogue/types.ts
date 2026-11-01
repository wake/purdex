// spa/src/lib/dialogue/types.ts — the wire shapes of the daemon's dialogue routes (plan T11) and the host event.

/** The `/api/info` capability a host lists when its daemon has the dialogue module. */
export const DIALOGUE_CAPABILITY = 'dialogue.v1'
/** The host event announcing a new recap; its value is JSON `{"session_id","recap_id"}`. */
export const DIALOGUE_RECAP_EVENT_TYPE = 'dialogue.recap'

/** One recap of a session (`GET /api/dialogue/{sid}/recaps`), newest first in a response. `at` is the daemon's stamp (a number). */
export interface Recap {
  id: number
  at: number
  from_no: number
  to_no: number
  trigger: string
  headline: string
  done: string[]
  now: string
  decided: string[]
  waiting: string[]
  next: string
}

export interface RecapsResponse {
  recaps: Recap[]
  /** The session can take a recap job now (false: it has ended). */
  can_generate: boolean
  /** The number of the newest done turn summary (0: none). */
  newest_turn_no: number
  /** The `to_no` of the newest recap (0: none). */
  last_recap_to_no: number
}

/** One done turn summary (`GET /api/dialogue/{sid}/turn-summaries`), oldest first in a response. */
export interface TurnSummary {
  turn_no: number
  at: number
  title: string
  summary: string
  push: string
}

/** `POST /api/dialogue/{sid}/recap`'s answer. */
export type RecapState = 'queued' | 'up_to_date' | 'unavailable'
export type RecapTrigger = 'open' | 'manual'

export interface TurnRange {
  from?: number
  to?: number
  limit?: number
}

/** The most turn summaries one `turn-summaries` read gives (the newest ones in the range): the daemon's limit (plan T11). */
export const TURN_SUMMARIES_LIMIT = 100
