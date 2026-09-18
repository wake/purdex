// spa/src/lib/nex/nex-host-reducer.ts — the pure half of the per-host
// "is Nexen ready here?" cache (P-C spec §4.1): the entry shape, the phase
// derivation and the entry transitions. No fetching, no clocks, no stores —
// every input is a parameter, so the invariant below is checkable without
// a network. `nex-host-effects.ts` drives these; `useNexHostStore.ts` holds
// the result.
import type { NexInfo } from '../host-api'
import type { NexCapabilities } from './types'
import { isNexReady } from '../../components/hosts/nex/nex-ready'
import type { HostConfig } from '../../stores/useHostStore'

export const NEX_HOST_TTL_MS = 60_000

export type NexHostPhase = 'unknown' | 'loading' | 'ready' | 'disabled' | 'unavailable'

export interface NexHostEntry {
  info: NexInfo | null
  capabilities: NexCapabilities | null
  phase: NexHostPhase
  error: string | null
  fetchedAt: number
  generation: number
  /** The host identity (`ip:port:token`) the data came from; see `hostFingerprint`. */
  fingerprint: string
}

/** What one fetch round produced, before it becomes an entry. */
export type Loaded = Pick<NexHostEntry, 'info' | 'capabilities' | 'error'>

/**
 * A request captures the entry's generation and the host identity it went
 * to; the answer commits only while both still hold (see `nex-host-effects`).
 */
export interface RequestToken { generation: number; fingerprint: string }

/** The one place `phase` is derived, so `ready` can never outlive its inputs. */
export function phaseOf({ info, capabilities, error }: Loaded): NexHostPhase {
  if (!info) return 'unavailable'
  if (!info.configured || !info.mounted) return 'disabled'
  if (info.init_error) return 'unavailable'
  if (!isNexReady(info)) return 'unavailable'
  if (error !== null || capabilities === null) return 'unavailable'
  return 'ready'
}

/** The identity a host's data is keyed on: re-pointing or re-keying a host is a different daemon. */
export function hostFingerprint(h: Pick<HostConfig, 'ip' | 'port' | 'token'>): string {
  return `${h.ip}:${h.port}:${h.token ?? ''}`
}

/** The placeholder an entry starts as while its first fetch is out. */
export function emptyEntry(generation: number, fingerprint: string): NexHostEntry {
  return { info: null, capabilities: null, phase: 'loading', error: null, fetchedAt: 0, generation, fingerprint }
}

/** Whether a cached entry may answer `ensure` without a fetch: settled, not `unavailable`, same host identity, inside the TTL. */
export function isFresh(entry: NexHostEntry | undefined, now: number, currentFingerprint: string): boolean {
  if (!entry || entry.fetchedAt === 0 || entry.phase === 'unavailable') return false
  if (entry.fingerprint !== currentFingerprint) return false
  return now - entry.fetchedAt < NEX_HOST_TTL_MS
}

/** The entry a completed fetch becomes. */
export function commitLoaded(loaded: Loaded, token: RequestToken, now: number): NexHostEntry {
  return {
    ...loaded,
    phase: phaseOf(loaded),
    fetchedAt: now,
    generation: token.generation,
    fingerprint: token.fingerprint,
  }
}

/** `invalidate`: keep the data on screen but make the TTL miss and orphan any in-flight request. */
export function markStale(entry: NexHostEntry, generation: number): NexHostEntry {
  return { ...entry, fetchedAt: 0, generation }
}
