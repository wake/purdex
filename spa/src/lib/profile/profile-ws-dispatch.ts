/**
 * profile-ws-dispatch — turns a `profile` host event (spec §4.6, Notification)
 * into a validated `ProfileRemoteEvent` for the sync driver. Same seam as
 * `backup-ws-dispatch`: `useMultiHostEventWs` owns the socket and the `hostId`,
 * this module owns the value.
 *
 * Wire shape (Go `profileEvent`, internal/module/profiles/handler_sections.go):
 * `{profileId, section, rev, hash, writerClientId, deleted?}`. `hash` has no
 * `omitempty`, so a delete arrives as `hash: ""` with `deleted: true`. The
 * daemon sends exactly two shapes and exactly those two are accepted:
 *   delete — `deleted: true`  AND `hash: ""`
 *   live   — `deleted` absent (or false) AND `hash` = 64 lowercase hex
 * Everything in between is DROPPED, never read as a delete: a false tombstone
 * tells the state machine "the SOT deleted this" → a needless reindex, a wrong
 * delete/push decision, or a phantom conflict. Dropping is safe — events are
 * only an optimization, reindex catches up.
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

let warnedContradictory = false

/** Test-only: empties the slot and re-arms the one-shot warning. */
export function __resetProfileEventsForTest(): void {
  listener = null
  warnedContradictory = false
}

/**
 * Well-formed JSON with well-typed fields that is still neither shape: that is
 * a daemon (or proxy) bug worth one line — once, a broken peer would repeat it
 * on every write. Bad JSON and mistyped fields stay silent, as before.
 */
function warnContradictory(deleted: boolean | undefined, hash: string): void {
  if (warnedContradictory) return
  warnedContradictory = true
  console.warn(
    `[profile-ws-dispatch] dropped a profile event that is neither a delete nor a live write ` +
      `(deleted=${String(deleted)}, hash length ${hash.length}); further ones are dropped silently`,
  )
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

  // The two shapes of the header, and nothing else.
  const isDelete = deleted === true && hash === ''
  const isLive = deleted !== true && LIVE_HASH.test(hash)
  if (!isDelete && !isLive) {
    warnContradictory(deleted, hash)
    return null
  }

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
