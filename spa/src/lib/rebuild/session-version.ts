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
//   `barrier` — (#1309 + #1310 spec §3.1.1) the host's versioned frames are
//            held back, not reconciled, from an operation-lock acquire until
//            the release's refresh settles; only the newest one is kept
//            (`stash`), with the `conn` it arrived on. See ws-sessions.ts.
//
// Not zustand, not persisted, not synced: it describes this window's sockets.
import type { HostEvent } from '../host-events'

export interface SessionVersion {
  epoch: string
  seq: number
}

/** Where a list came from: a frame of the current socket, or a fetch sent on connection `conn`. */
export type VersionOrigin = { kind: 'ws' } | { kind: 'fetch'; conn: number }

const EPOCH = /^[0-9a-f]{16}$/

const held = new Map<string, SessionVersion>()
const conns = new Map<string, number>()

interface Stashed {
  event: HostEvent
  v: SessionVersion
  conn: number
}
/** Hosts in barrier → the newest versioned frame held back (null: none yet). */
const barriers = new Map<string, Stashed | null>()

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
  barriers.delete(hostId)
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

// === The barrier (spec §3.1.1) ===

/** Hold back `hostId`'s versioned frames. A host already in barrier keeps it — and its stash. */
export function raiseBarrier(hostId: string): void {
  if (!barriers.has(hostId)) barriers.set(hostId, null)
}

export function inBarrier(hostId: string): boolean {
  return barriers.has(hostId)
}

/**
 * Keep `event` (version `v`, a frame of the current socket) if it is the
 * newest held back so far: same epoch → the higher seq; another epoch → the
 * later arrival (the current socket speaks for the running process, as `decide`).
 */
export function stashFrame(hostId: string, event: HostEvent, v: SessionVersion): void {
  if (!barriers.has(hostId)) return
  const prev = barriers.get(hostId) ?? null
  if (prev !== null && prev.v.epoch === v.epoch && prev.v.seq >= v.seq) return
  barriers.set(hostId, { event, v, conn: currentConn(hostId) })
}

/** A later list was handled on this socket: nothing held back may be reconciled after it. */
export function dropStash(hostId: string): void {
  if (barriers.has(hostId)) barriers.set(hostId, null)
}

/**
 * End `hostId`'s barrier. Returns the frame held back when it came on the
 * connection that is live now — the caller hands it to the normal path, where
 * `decide` judges it. A frame of a connection that has since closed is dropped:
 * that connection's gate is closed, and the new one's own first frame (read
 * later) is what may open it.
 */
export function lowerBarrier(hostId: string): HostEvent | null {
  const stashed = barriers.get(hostId) ?? null
  barriers.delete(hostId)
  if (stashed === null || stashed.conn !== currentConn(hostId)) return null
  return stashed.event
}

export function __resetForTests(): void {
  held.clear()
  conns.clear()
  barriers.clear()
}
