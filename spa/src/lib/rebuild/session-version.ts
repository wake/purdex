// spa/src/lib/rebuild/session-version.ts — which session list is newer (#1255 SPA
// spec §3.1; daemon contract docs/specs/2026-09-23-session-list-fresh-spec.md).
//
// A new daemon stamps every list it hands out on the versioned paths — WS
// `sessions` frames and `GET /api/sessions?fresh=1` — with `{epoch, seq}`: the
// daemon process, and the list's place in that process's read order. Delivery
// order is not read order (a push can land after a fetch read later), so a list
// is applied only when it is NEWER than the one last applied for its host.
//
// Per host this window holds:
//   `held` — the version of the list last handed to the reconciliation —
//            claimed before it runs, kept even if it threw (see `note`) —
//            null: none, or the host is an old, unversioned daemon again;
//   `conn` — a connection generation, bumped on every open and close of the
//            host-events socket and on entry teardown. A fetch records the
//            `conn` it was sent on: a list from a DIFFERENT daemon process is
//            only trusted when it was sent on the connection that is live now.
//
// Not zustand, not persisted, not synced: it describes this window's sockets.

export interface SessionVersion {
  epoch: string
  seq: number
}

/** Where a list came from: a frame of the current socket, or a fetch sent on connection `conn`. */
export type VersionOrigin = { kind: 'ws' } | { kind: 'fetch'; conn: number }

const EPOCH = /^[0-9a-f]{16}$/

const held = new Map<string, SessionVersion>()
const conns = new Map<string, number>()

/** `{epoch, seq}` off a frame or a response body; null when it is unversioned (or malformed). */
export function parseVersion(x: unknown): SessionVersion | null {
  if (typeof x !== 'object' || x === null) return null
  const { epoch, seq } = x as { epoch?: unknown; seq?: unknown }
  if (typeof epoch !== 'string' || !EPOCH.test(epoch)) return null
  if (typeof seq !== 'number' || !Number.isSafeInteger(seq) || seq < 1) return null
  return { epoch, seq }
}

export function currentConn(hostId: string): number {
  return conns.get(hostId) ?? 0
}

function bump(hostId: string): void {
  conns.set(hostId, currentConn(hostId) + 1)
}

/** The host-events socket opened. */
export function connectionOpened(hostId: string): void {
  bump(hostId)
}

/** The host-events socket closed or was retired. Call BEFORE closing the attach gate (§3.3). */
export function connectionClosed(hostId: string): void {
  bump(hostId)
}

/** The host's entry was torn down (removed, endpoint changed, unmount): nothing held survives it. */
export function forgetHost(hostId: string): void {
  held.delete(hostId)
  bump(hostId)
}

/** An unversioned list arrived: nothing versioned may be compared against a list from before it. */
export function clearHeld(hostId: string): void {
  held.delete(hostId)
}

export function heldVersion(hostId: string): SessionVersion | null {
  return held.get(hostId) ?? null
}

/** May list `v`, which came via `origin`, be reconciled for `hostId`? Changes nothing. */
export function decide(hostId: string, v: SessionVersion, origin: VersionOrigin): 'apply' | 'stale' {
  const h = held.get(hostId)
  if (h === undefined) return 'apply'
  if (v.epoch === h.epoch) return v.seq > h.seq ? 'apply' : 'stale'
  // Another daemon process: comparable by nothing but the channel (contract §3.4).
  if (origin.kind === 'ws') return 'apply'
  return origin.conn === currentConn(hostId) ? 'apply' : 'stale'
}

/**
 * Claim `v` for `hostId` — call right BEFORE `reconcileHostSessions`, after
 * `decide` said apply. The reconciliation is not transactional (it may write
 * part of a list, e.g. an irreversible `session-closed`, and then throw), so
 * an older list must never be decided newer after it; a failed reconciliation
 * is recovered by a fresh refresh instead (refresh-sessions.ts).
 */
export function note(hostId: string, v: SessionVersion): void {
  held.set(hostId, v)
}

export function __resetForTests(): void {
  held.clear()
  conns.clear()
}
