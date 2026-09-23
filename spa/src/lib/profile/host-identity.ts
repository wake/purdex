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
