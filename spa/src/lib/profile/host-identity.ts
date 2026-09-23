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
