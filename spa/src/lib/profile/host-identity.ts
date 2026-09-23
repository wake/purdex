// spa/src/lib/profile/host-identity.ts — the host identity codec of Profile
// Sync (host-sync-identity spec §2–§4, §6, §11). PURE: no store, no I/O.
//
// Three identities (spec §2):
//   - local id    — this device's key for a host; never changes, never travels
//                   when the host knows its daemon.
//   - daemon id   — `HostConfig.daemonId`, the claimed identity (synced).
//   - sync id     — the host's key ON THE WIRE: `syncIdOf(daemonId)`, identical
//                   on every device.
// A host with no valid claim travels under its local id (legacy fallback).
//
// Profile Sync translates at its boundary only: build maps local → wire,
// apply maps wire → local. Every translator here is TOTAL: it never throws,
// and a value it cannot map passes through unchanged.
import { sha256Hex, sha256HexSync } from '../crypto-hash'
import { isValidDaemonId } from '../daemon-id'

/** The prefix of THIS version's sync ids. A new algorithm gets a new prefix (`d2_`), never a changed `d1_`. */
export const SYNC_ID_PREFIX = 'd1_'

/** Any version's sync id: `d<digits>_…`. Only `d1_` ids are ever produced or matched by this version. */
const ANY_SYNC_ID_RE = /^d[0-9]+_/

/**
 * Is `value` a sync id of ANY version? A `d2_…` from a newer client is a sync
 * id this version cannot map — unknown, never mistaken for a legacy local id.
 * Anything that is not a sync id is a legacy local id.
 */
export function isSyncId(value: unknown): value is string {
  return typeof value === 'string' && ANY_SYNC_ID_RE.test(value)
}

const SYNC_ID_BODY_LENGTH = 16

/** `d1_` + base36 of the first 80 bits (10 bytes) of the digest, left-padded with '0' to 16. */
function syncIdFromDigestHex(hex: string): string {
  const n = BigInt('0x' + hex.slice(0, 20))
  return SYNC_ID_PREFIX + n.toString(36).padStart(SYNC_ID_BODY_LENGTH, '0')
}

/**
 * The wire id of a daemon (spec §3): `"d1_" + base36(first 80 bits of
 * SHA-256(UTF-8(daemonId))))`, lower-case, zero-padded to 16. Fixed forever.
 * Byte-identical to `syncIdOfSync` on both hash paths (crypto-hash invariant).
 */
export async function syncIdOf(daemonId: string): Promise<string> {
  return syncIdFromDigestHex(await sha256Hex(new TextEncoder().encode(daemonId)))
}

/** `syncIdOf`, synchronously (the pure-JS SHA-256). Same output for every input. */
export function syncIdOfSync(daemonId: string): string {
  return syncIdFromDigestHex(sha256HexSync(new TextEncoder().encode(daemonId)))
}

// === Identity (spec §4, §11.3) ===

/** The part of a `HostConfig` the identity reads. */
export interface IdentityHost {
  id: string
  daemonId?: string
}

/**
 * One snapshot's mapping between local ids and wire ids.
 *
 * `conflict` (sorted local ids, or null) names every host whose wire id is
 * ambiguous: two hosts claiming one daemon, two claims hashing to one sync id,
 * a claim's sync id equal to another host's legacy (local-id) wire id, or a
 * no-claim local id that itself looks like a sync id. Conflicting hosts are
 * LEFT OUT of both maps; the caller must not build or apply anything that
 * names hosts while `conflict` is set (spec §11.4 pauses the profile).
 *
 * `signature` changes exactly when a pair or the conflict changes — the
 * collector watches it and re-schedules every host-bearing section (§11.3).
 */
export interface HostIdentity {
  toWire: Map<string, string>
  toLocal: Map<string, string>
  conflict: string[] | null
  signature: string
}

function compareStrings(a: string, b: string): number {
  return a < b ? -1 : a > b ? 1 : 0
}

function buildIdentity(hosts: Record<string, IdentityHost>, claimWire: ReadonlyMap<string, string>): HostIdentity {
  const wireOf = new Map<string, string>()
  const conflict = new Set<string>()
  for (const local of Object.keys(hosts)) {
    const claimed = claimWire.get(local)
    if (claimed !== undefined) {
      wireOf.set(local, claimed)
    } else {
      wireOf.set(local, local)
      // A no-claim id shaped like a sync id would be read as one on the wire.
      if (isSyncId(local)) conflict.add(local)
    }
  }
  const byWire = new Map<string, string[]>()
  for (const [local, wire] of wireOf) {
    const group = byWire.get(wire)
    if (group) group.push(local)
    else byWire.set(wire, [local])
  }
  for (const group of byWire.values()) {
    if (group.length > 1) for (const local of group) conflict.add(local)
  }

  const toWire = new Map<string, string>()
  const toLocal = new Map<string, string>()
  for (const [local, wire] of wireOf) {
    if (conflict.has(local)) continue
    toWire.set(local, wire)
    toLocal.set(wire, local)
  }
  const conflictList = conflict.size > 0 ? [...conflict].sort(compareStrings) : null
  const pairs = [...toWire].sort((a, b) => compareStrings(a[0], b[0]))
  return { toWire, toLocal, conflict: conflictList, signature: JSON.stringify({ pairs, conflict: conflictList }) }
}

export interface IdentityOptions {
  /** Test seam: replaces `syncIdOfSync` (e.g. to force two claims onto one sync id). */
  hash?: (daemonId: string) => string
}

export interface AsyncIdentityOptions {
  /** Test seam: replaces `syncIdOf`. */
  hash?: (daemonId: string) => string | Promise<string>
}

/** The identity of one host-store snapshot, synchronously (spec §11.3 — the collector's path). */
export function identityOfSync(hosts: Record<string, IdentityHost>, opts: IdentityOptions = {}): HostIdentity {
  const hash = opts.hash ?? syncIdOfSync
  const claimWire = new Map<string, string>()
  for (const [local, host] of Object.entries(hosts)) {
    const daemonId = host?.daemonId
    if (isValidDaemonId(daemonId)) claimWire.set(local, hash(daemonId))
  }
  return buildIdentity(hosts, claimWire)
}

/** `identityOfSync` through the async hash (WebCrypto when present). Same result for every input. */
export async function identityOf(hosts: Record<string, IdentityHost>, opts: AsyncIdentityOptions = {}): Promise<HostIdentity> {
  const hash = opts.hash ?? syncIdOf
  const claimWire = new Map<string, string>()
  for (const [local, host] of Object.entries(hosts)) {
    const daemonId = host?.daemonId
    if (isValidDaemonId(daemonId)) claimWire.set(local, await hash(daemonId))
  }
  return buildIdentity(hosts, claimWire)
}

// === Apply-side matching (spec §6, §11.5, §11.6) ===

/**
 * An incoming `hosts` row as far as matching is concerned. Wire data: every
 * field is untrusted, so it is typed loosely and read defensively.
 */
export interface IncomingHostRow {
  id?: unknown
  daemonId?: unknown
  aliases?: unknown
}

export type HostMatchError =
  /** Two incoming rows name one daemon (same daemonId, or a canonical key and a claim hashing to it). */
  | 'duplicate-host-identity'
  /** A row's daemon is claimed by more than one LOCAL host: which one it updates is ambiguous. */
  | 'host-identity-conflict'

export interface HostMatch {
  /** Per incoming row key: the local host it updates in place, or `'new'` (create with a new local id). */
  byRow: Map<string, string>
  /** Local hosts no row matched (the caller cascades their removal). */
  removed: string[]
  /** Set → nothing may be applied; `byRow` and `removed` are then empty. */
  error?: HostMatchError
}

/** The one sentinel `byRow` uses for "create a new local host". Never a local id (those are 6-char base36). */
export const NEW_HOST = 'new'

function rowDaemonId(row: unknown): string | undefined {
  if (row === null || typeof row !== 'object') return undefined
  const daemonId = (row as IncomingHostRow).daemonId
  return isValidDaemonId(daemonId) ? daemonId : undefined
}

/**
 * Match each incoming row to a local host (spec §6 as amended by §11.5/§11.6):
 *   - a row carrying a valid `daemonId` matches the local host with that
 *     `daemonId` — and ONLY by it; its key never captures anything;
 *   - else a `d1_` key matches the local host whose `syncIdOf(daemonId)` is it;
 *     a sync id of another version matches nothing;
 *   - else (a legacy local-id key) it matches the local host of that id, but
 *     only if that host has no `daemonId` either;
 *   - otherwise the row is new.
 * One-to-one: two rows resolving to one daemon → `duplicate-host-identity`.
 */
export function matchIncomingHosts(
  localHosts: Record<string, IdentityHost>,
  incomingRows: Record<string, unknown>,
  opts: IdentityOptions = {},
): HostMatch {
  const hash = opts.hash ?? syncIdOfSync
  const failed = (error: HostMatchError): HostMatch => ({ byRow: new Map(), removed: [], error })

  const localsByDaemon = new Map<string, string[]>()
  const localsBySyncId = new Map<string, string[]>()
  for (const [local, host] of Object.entries(localHosts)) {
    const daemonId = host?.daemonId
    if (!isValidDaemonId(daemonId)) continue
    const push = (m: Map<string, string[]>, k: string) => {
      const list = m.get(k)
      if (list) list.push(local)
      else m.set(k, [local])
    }
    push(localsByDaemon, daemonId)
    push(localsBySyncId, hash(daemonId))
  }

  const byRow = new Map<string, string>()
  const seenDaemons = new Set<string>()
  const matched = new Set<string>()
  for (const [key, row] of Object.entries(incomingRows)) {
    const daemonId = rowDaemonId(row)
    let candidates: string[] = []
    let daemonKey: string | null = null
    if (daemonId !== undefined) {
      daemonKey = hash(daemonId)
      candidates = localsByDaemon.get(daemonId) ?? []
    } else if (isSyncId(key)) {
      daemonKey = key
      if (key.startsWith(SYNC_ID_PREFIX)) candidates = localsBySyncId.get(key) ?? []
    } else {
      const host = Object.hasOwn(localHosts, key) ? localHosts[key] : undefined
      if (host && !isValidDaemonId(host.daemonId)) candidates = [key]
    }
    if (daemonKey !== null) {
      if (seenDaemons.has(daemonKey)) return failed('duplicate-host-identity')
      seenDaemons.add(daemonKey)
    }
    if (candidates.length > 1) return failed('host-identity-conflict')
    byRow.set(key, candidates[0] ?? NEW_HOST)
    if (candidates[0] !== undefined) matched.add(candidates[0])
  }

  const removed = Object.keys(localHosts).filter((local) => !matched.has(local))
  return { byRow, removed }
}
