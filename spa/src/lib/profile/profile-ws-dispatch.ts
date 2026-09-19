/**
 * profile-ws-dispatch — turns a `profile` host event (spec §4.6, Notification)
 * into a validated `ProfileRemoteEvent` for the sync driver. Same seam as
 * `backup-ws-dispatch`: `useMultiHostEventWs` owns the socket and the `hostId`,
 * this module owns the value.
 *
 * Wire shape (Go `profileEvent`, internal/module/profiles/handler_sections.go):
 * `{profileId, section, rev, hash, writerClientId, deleted?}`. `hash` has no
 * `omitempty`, so a delete arrives as `hash: ""` with `deleted: true`.
 *
 * Deliberately NOT done here: filtering this client's own writes. The reducer
 * needs `own` as an input, and whether a write is "ours" also depends on which
 * window holds the leader lease — so this module imports no identity source and
 * no store, and passes `writerClientId` through untouched.
 */
import type { HostEvent } from '../host-events'

export interface ProfileRemoteEvent {
  hostId: string
  profileId: string
  section: string
  rev: number
  /** `null` = the section was deleted (a tombstone has no hash). */
  hash: string | null
  writerClientId: string
}

export type ProfileEventListener = (e: ProfileRemoteEvent) => void

const LIVE_HASH = /^[0-9a-f]{64}$/

let listener: ProfileEventListener | null = null

/** One slot: a later listener replaces the earlier one; `null` clears it. */
export function setProfileEventListener(fn: ProfileEventListener | null): void {
  listener = fn
}

function parse(hostId: string, value: string): ProfileRemoteEvent | null {
  let raw: unknown
  try {
    raw = JSON.parse(value)
  } catch {
    return null
  }
  if (typeof raw !== 'object' || raw === null || Array.isArray(raw)) return null
  const { profileId, section, rev, hash, writerClientId, deleted } = raw as Record<string, unknown>

  if (typeof profileId !== 'string' || profileId === '') return null
  if (typeof section !== 'string' || section === '') return null
  if (typeof rev !== 'number' || !Number.isSafeInteger(rev) || rev < 0) return null
  if (typeof writerClientId !== 'string') return null
  if (typeof hash !== 'string') return null
  if (deleted !== undefined && typeof deleted !== 'boolean') return null

  // `deleted` wins over whatever `hash` says; an empty hash alone also means
  // "no live content". Anything else must be a real section hash — an invalid
  // live hash must never reach the state machine.
  const isDelete = deleted === true || hash === ''
  if (!isDelete && !LIVE_HASH.test(hash)) return null

  return { hostId, profileId, section, rev, hash: isDelete ? null : hash, writerClientId }
}

export function dispatchProfileWsEvent(hostId: string, event: HostEvent): void {
  const fn = listener
  if (!fn) return
  const parsed = parse(hostId, event.value)
  if (!parsed) return // malformed — ignore, never throw on the WS path
  try {
    fn(parsed)
  } catch (err) {
    console.error('[profile-ws-dispatch] listener threw', err)
  }
}
