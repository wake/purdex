// spa/src/lib/fleet/types.ts — the fleet wire shapes (fleet spec rev 2 §6) in ONE place. The daemon side is not built yet:
// everything here is the contract as the spec states it, and the test fixtures are written from it. Where the spec does not
// name a field (the `Queue` summary's keys), the shape is marked ASSUMED, so matching the real answer means editing this
// file and parse.ts only.
import type { HostConfig } from '../../stores/useHostStore'

export const FLEET_CAPABILITY = 'fleet.v1'
export const FLEET_EVENT_TYPE = 'fleet.changed'

export const HOST_KINDS = ['daemon', 'probe'] as const
export type FleetHostKind = (typeof HOST_KINDS)[number]

export const HOST_STATUSES = ['ok', 'offline', 'error'] as const
export type FleetHostStatus = (typeof HOST_STATUSES)[number]

/** rev 1's error codes; an unknown code is read as `parse`. */
export const ERROR_CODES = ['timeout', 'unreachable', 'host_key', 'auth', 'remote_exit', 'parse', 'alias_gone'] as const
export type FleetErrorCode = (typeof ERROR_CODES)[number]

export const ZONES = ['normal', 'warning', 'danger'] as const
export type FleetZone = (typeof ZONES)[number]

/** `level`: memory pressure 1, 2 or 4; 0 = unknown (a Linux host without PSI). */
export interface FleetFast {
  at: number // ms since epoch (RFC3339 on the wire), the primary's receive time
  cpu: number
  mem: number
  level: number
  page_rate: number
  disk: number
  disk_free_gb: number
  load1: number
  ncpu: number
  zone: FleetZone
  load: number // the composite score, 0–100
}

export interface FleetProc { pid: number; name: string; cpu: number; rss_mb: number }

/** Times are RFC3339 strings on the wire (UTC); parse.ts turns them into ms since epoch, which is all the App holds. */
export interface FleetWaiter { class: string; place: number; command: string; project: string; session: string; since: number }
/** A command that holds a slot. `since` is when it started; `pid` is the gate's pid (the 名額鎖 column). The daemon never sends the slot's token. */
export interface FleetHolder { class: string; slot: number; command: string; project: string; session: string; since: number; pid: number }
export interface FleetQueue {
  heavy_used: number
  heavy_total: number
  light_used: number
  light_total: number
  /** Heavy holders first, then light; at most 20. A 744 daemon sends none: parsed as []. */
  holders: FleetHolder[]
  waiters: FleetWaiter[]
  longest_wait_s: number
  admission_held: boolean
  /** Why admission holds ('' when it does not, or when the daemon did not say). */
  admission_reason: string
  /** A class with 0 slots is paused: new commands wait. */
  heavy_paused: boolean
  light_paused: boolean
}

export interface FleetSlow { at: number; top_cpu: FleetProc[]; top_mem: FleetProc[]; queue?: FleetQueue }

export interface FleetHost {
  id: string // a daemon host's own daemon host id, or a probe row's config id
  kind: FleetHostKind
  alias: string
  name: string
  icon: string
  colors?: HostConfig['colors']
  icon_weight?: string
  os: string
  status: FleetHostStatus
  error: FleetErrorCode | null
  last_ok_at: number | null
  fast: FleetFast | null
  slow: FleetSlow | null
}

/** `GET /api/fleet` (samples: `__fixtures__/api-fleet-*.json`, copied from the daemon's testdata). */
export type FleetAnswer =
  | { role: 'primary'; term: number; at: number; hosts: FleetHost[] }
  | { role: 'standby'; term: number; primary: string }
  | { role: 'off'; reason: string }

/**
 * One host of a `fleet.changed` push: always `id`, `status`, `error`, `last_ok_at` and `fast` (null before the first reading);
 * `slow` only right after it changed. `full` is the whole row when one carries a `kind` (the daemon's sample has none, so a
 * host the App has not seen is a re-read).
 */
export interface FleetPushHost {
  id: string
  fast?: FleetFast | null // null: no reading yet; absent: not in this push
  slow?: FleetSlow // only when it changed
  last_ok_at?: number | null
  status?: FleetHostStatus
  error?: FleetErrorCode | null
  full?: FleetHost
}
export interface FleetPush { term: number; primary: string; hosts: FleetPushHost[] }
