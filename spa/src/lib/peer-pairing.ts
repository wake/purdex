// spa/src/lib/peer-pairing.ts — the pure rules of the Peers page (Phase D spec §5.1).
// No React, no store, no fetch: everything here is a function of its arguments.
import type { PeerHostRow, PeerHostVerify } from './host-api'

/** One direction's live verify, or not yet answered. */
export type VerifyOutcome =
  | { ok: true; self_alias: string; daemon_version: string; host_id: string }
  | { ok: false; error: string }
export type Side = VerifyOutcome | 'pending'

/**
 * The return direction has three non-verify states that are deliberately
 * distinct (spec §5.1): `no-entry` = the counterpart was asked and has no
 * entry for us; `not-app-host` = no App host IS this peer (a permanent fact
 * about the App, spec D-4); `counterpart-unavailable` = the App has that
 * host but could not ask it this time (a transient fact).
 */
export type InboundState = VerifyOutcome | 'pending' | 'no-entry' | 'not-app-host' | 'counterpart-unavailable'

export type PairStatus = 'bidirectional' | 'one-way' | 'outbound-only' | 'return-unknown' | 'unpaired' | 'checking'

/** An App host as a join candidate. `host_id` is '' when the page could not learn it. */
export interface CounterpartCandidate {
  hostId: string
  host_id: string
  url: string
}

/**
 * The daemon's `normalizeHostURL` (`internal/module/peers/hosts.go`): parse,
 * keep scheme+host+path, trim trailing slashes. `URL.host` lower-cases the
 * hostname and drops a default port, which the daemon's `url.String()` does
 * not — irrelevant here because both sides of the join go through THIS
 * function, and a default-port peer URL does not occur (the daemon listens
 * on 7860). Unparseable input is returned trimmed so two garbage strings
 * still compare by their own text and never collapse to a shared value.
 *
 * An App host configured by hostname and a daemon entry written by IP (or
 * vice versa) never URL-join — this only matters when the entry has no
 * host_id or the host is unavailable; identity (host_id) is unaffected.
 */
export function normalizePeerUrl(raw: string): string {
  const s = raw.trim()
  try {
    const u = new URL(s)
    const path = u.pathname.replace(/\/+$/, '')
    return `${u.protocol}//${u.host}${path}`
  } catch {
    return s
  }
}

/**
 * Spec D-3 read with §5.2 step 2. Identity (`host_id`) decides whenever
 * both sides have one. Exactly two URL fallbacks exist:
 *  1. the entry has no host_id (added without a token) → any candidate by URL;
 *  2. the entry has one that no candidate carries → only a candidate whose
 *     host_id is UNKNOWN (an App host the page could not ask), which is what
 *     makes such an entry `counterpart-unavailable` instead of `not-app-host`.
 * A candidate whose KNOWN host_id differs is never joined by URL: that is a
 * different daemon at the same address.
 *
 * When several App hosts share that URL, the URL fallback is order-invariant
 * (codex F2): collect every candidate the URL condition allows; if none, null;
 * among those, prefer the ones with a known host_id — if they all agree, that
 * daemon is the counterpart, if they disagree that is contradictory data and
 * the join is null; if none has a known host_id, take the first, since an
 * unavailable counterpart is never dialled, only reported as unaskable.
 */
export function matchCounterpart(
  entry: { host_id: string; url: string },
  hosts: CounterpartCandidate[],
): CounterpartCandidate | null {
  if (entry.host_id !== '') {
    const byId = hosts.find((h) => h.host_id !== '' && h.host_id === entry.host_id)
    if (byId) return byId
  }
  const url = normalizePeerUrl(entry.url)
  if (url === '') return null
  const matches = hosts.filter((h) =>
    (entry.host_id === '' || h.host_id === '') && normalizePeerUrl(h.url) === url,
  )
  if (matches.length === 0) return null
  const known = matches.filter((h) => h.host_id !== '')
  if (known.length > 0) {
    return known.every((h) => h.host_id === known[0].host_id) ? known[0] : null
  }
  return matches[0]
}

/**
 * Which of THEIR entries is US: `matchCounterpart` with the roles swapped,
 * over the counterpart's `GET /api/peers/hosts` rows. Returns the row so
 * callers never have to carry an alias through a `hostId` field.
 */
export function matchReturnEntry(
  self: { host_id: string; url: string },
  rows: PeerHostRow[],
): PeerHostRow | null {
  const hit = matchCounterpart(self, rows.map((r) => ({ hostId: r.alias, host_id: r.host_id, url: r.url })))
  return hit ? rows.find((r) => r.alias === hit.hostId) ?? null : null
}

/** The §5.1 status table, row for row. */
export function pairStatus(outbound: Side, inbound: InboundState): PairStatus {
  if (outbound === 'pending' || inbound === 'pending') return 'checking'
  if (outbound.ok) {
    if (inbound === 'not-app-host') return 'outbound-only'
    if (inbound === 'counterpart-unavailable') return 'return-unknown'
    if (inbound === 'no-entry') return 'one-way'
    return inbound.ok ? 'bidirectional' : 'one-way'
  }
  if (typeof inbound === 'object' && inbound.ok) return 'one-way'
  return 'unpaired'
}

/**
 * The same rule as `aliasDriftField` in `cmd/pdx/peers.go`: the peer's self
 * alias when it is non-empty and not case-insensitively equal to what we
 * call it, else ''. No trimming — the daemon bounded the value for display
 * and this compares what it said.
 */
export function aliasDrift(alias: string, selfAlias: string): string {
  if (selfAlias === '') return ''
  if (selfAlias.toLowerCase() === alias.toLowerCase()) return ''
  return selfAlias
}

/** Collapses the wire verify into the two-shape outcome the rules take. */
export function toOutcome(v: PeerHostVerify): VerifyOutcome {
  if (v.ok) return { ok: true, self_alias: v.self_alias, daemon_version: v.daemon_version, host_id: v.host_id }
  return { ok: false, error: v.error || 'peer reported ok=false' }
}
