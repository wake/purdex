// spa/src/lib/pairing-types.ts — phone pairing's constants and shapes (moved verbatim out of pairing.ts, #2238).
import type { HostConfig } from '../stores/useHostStore'
import type { HostLook } from './host-look'

export const PAIRING_TTL_MS = 10 * 60_000
/** How long a token outlives the deadline D (spec §2). */
export const PAIRING_TOKEN_MARGIN_S = 300
/** Below this many seconds left the entry is not created (the daemon's own minimum, spec §4.1). */
export const PAIRING_MIN_ENTRY_S = 60
export const PAIRING_POLL_MS = 2000
export const PAIRING_HTTP_TIMEOUT_MS = 10_000

export interface PairingProfile {
  /** SPA host id of the profile's SOT host. */
  sotHostId: string
  profileId: string
  profileName: string
}

export interface PairingInput {
  profile: PairingProfile
  /** The relay host: holds the pairing entry; its ip:port goes into the QR. */
  relay: HostConfig
  /** Candidate hosts (those with a token). The SOT host must be among them. */
  hosts: readonly HostConfig[]
  /** The phone's name (`label` of every device token). */
  label: string
  /** The workbench look (name + look fields) of a host; default: the host's own fields. */
  lookOf?: (hostId: string) => HostLook
  /** Default `Date.now`. */
  clock?: () => number
  /** Aborting closes the session. */
  signal?: AbortSignal
}

export type LeftOutReason = 'unknown_host' | 'no_token' | 'no_daemon_id' | 'invalid_address' | 'mint_failed'
export interface LeftOut {
  hostId: string
  reason: LeftOutReason
}

export type PairingFailureReason =
  | 'sot_failed' // the profile's SOT host could not be minted on (or has no daemon id / token)
  | 'too_late' //   < 60 s of the 10 minutes left after minting
  | 'cancelled' //  closed while minting
  | 'unknown_host' // the relay is not in the host store; nothing was sent to it
  | 'bad_payload'
  | 'too_large'
  | 'capacity'
  | 'unavailable'
  | 'unauthorized'
  | 'no_token'
  | 'unsupported'
  | 'network'
  | 'timeout'
  | 'malformed'

export interface PairingFailure {
  kind: 'failed'
  reason: PairingFailureReason
  /** Hosts whose tokens could not be revoked after the abort; the caller retries later. */
  revokeFailed: string[]
  /** The pairing the minted tokens belong to (absent when nothing was minted): what a later retry revokes. */
  pairingId?: string
}

export interface PairingReady {
  kind: 'ok'
  code: string
  /** The entry's expiry on the relay's clock (ms). */
  expiresAt: number
  qrUrl: string
  leftOut: LeftOut[]
  /** SPA host ids whose pair row is in the entry. */
  mintedHostIds: string[]
  pairingId: string
  /** D, ms on this clock. */
  deadline: number
  /** Hosts left out whose mint outcome was unknown and whose cleanup revoke failed. */
  revokeFailed: string[]
}

export type PackageResult = PairingReady | PairingFailure
