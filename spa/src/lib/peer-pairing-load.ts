// spa/src/lib/peer-pairing-load.ts — what the Peers page does on mount and on
// Refresh (Phase D spec §5.2, D4 §7.1/§7.3), as one async function over an
// injected API so it is testable without React and the component only
// renders snapshots.
//
// Cost per run with n entries on X and m connected other hosts: 3 calls on X,
// ≤ 2 per other connected host, 1 list per host that is somebody's
// counterpart OR a pair candidate, ≤ 2n verifies — all in parallel — and,
// only when a rotation is pending somewhere, 1 fresh list per host that owns
// a pending entry, strictly after the dials (step 5). Nothing is cached
// across runs (spec D-6: a stale green is the thing this page exists to remove).
import { HostApiError, type PeerHostRow, type PeerHostVerify, type PeerSettings } from './host-api'
import type { HostRuntime } from '../stores/useHostStore'
import { matchCounterpart, matchReturnEntry, toOutcome, type CounterpartCandidate, type InboundState, type Side } from './peer-pairing'

export interface PairingApi {
  info: (hostId: string) => Promise<{ host_id: string }>
  settings: (hostId: string) => Promise<PeerSettings>
  list: (hostId: string) => Promise<PeerHostRow[]>
  verify: (hostId: string, alias: string) => Promise<PeerHostVerify>
}

export interface PairingAppHost {
  hostId: string
  name: string
  url: string
  status: HostRuntime['status'] | undefined
}

/** An available App host that X has no entry for — what "Pair with…" lists (spec §7.1). */
export interface PairCandidate {
  hostId: string
  name: string
  url: string
  host_id: string
  /** Y's existing entry for X when Y already holds one (the one-way repair case, spec §7.1 last para); null otherwise. */
  returnEntry: PeerHostRow | null
  /** Why Y's entries could not be read this run; '' when they could. Non-empty ⇒ the page must not offer Pair (codex F4: a Y that already holds an entry for X must go through the repair path, and an unread Y might). */
  listError: string
}

export interface PairingRow {
  entry: PeerHostRow
  /** The App host that IS this peer, when the join found one (spec D-3). */
  counterpart: { hostId: string; name: string } | null
  /** Why the counterpart could not be asked this run; '' when it could (or there is none). */
  counterpartCause: string
  /** The counterpart's entry for X, when it was asked and has one. */
  returnEntry: PeerHostRow | null
  outbound: Side
  inbound: InboundState
  /**
   * §7.3: Commit/Cancel may only be offered from a row read AFTER the evidence dial.
   * Per side (codex F5 — both entries can be pending in one row and are re-read
   * from different hosts): `entry` = X's row, `returnEntry` = Y's row. true while a
   * rotation is pending on that side and this run has NOT yet re-read it after its
   * dial (pre-dial emits), or the re-read failed / the alias vanished. false when
   * nothing is pending on that side or the post-dial re-read landed.
   */
  gateStale: { entry: boolean; returnEntry: boolean }
}

export interface PairingSnapshot {
  /** X's own identity. `self_alias_source` is absent on a daemon < alpha.401 (self alias spec S-5): the page then offers no editor. */
  self: { host_id: string; self_alias: string; self_alias_source?: PeerSettings['alias_source'] } | null
  /** Page-level failure of one of X's three precondition calls (spec §5.2 step 0). */
  error: { call: 'info' | 'settings' | 'list'; message: string } | null
  rows: PairingRow[]
  candidates: PairCandidate[]
}

// Page-level and per-host causes carry the daemon's own `{error}` text when there is one.
const msg = (e: unknown) => (e instanceof HostApiError ? e.detail : e instanceof Error ? e.message : String(e))
const toError = (e: unknown) => (e instanceof Error ? e : new Error(String(e)))

type Meta =
  | { hostId: string; name: string; url: string; available: true; host_id: string; self_alias: string }
  | { hostId: string; name: string; url: string; available: false; cause: string }

async function loadMeta(h: PairingAppHost, api: PairingApi): Promise<Meta> {
  const base = { hostId: h.hostId, name: h.name, url: h.url }
  if (h.status !== 'connected') return { ...base, available: false, cause: h.status ?? 'unknown' }
  const [info, settings] = await Promise.allSettled([api.info(h.hostId), api.settings(h.hostId)])
  if (info.status === 'rejected') return { ...base, available: false, cause: `info: ${msg(info.reason)}` }
  if (settings.status === 'rejected') return { ...base, available: false, cause: `settings: ${msg(settings.reason)}` }
  return { ...base, available: true, host_id: info.value.host_id, self_alias: settings.value.alias }
}

/**
 * A pending side is stale by definition until step 5 has re-read it after the
 * dial: the pre-dial emit and every settle emit carry this, so the component
 * never offers Commit/Cancel from a row whose evidence predates the dial.
 */
const preDialGate = (entry: PeerHostRow, returnEntry: PeerHostRow | null): PairingRow['gateStale'] =>
  ({ entry: entry.rotation_pending, returnEntry: !!returnEntry?.rotation_pending })

export async function loadPairings(
  x: PairingAppHost,
  others: PairingAppHost[],
  api: PairingApi,
  emit: (s: PairingSnapshot) => void,
): Promise<PairingSnapshot> {
  // Step 0–1: X's own metadata and entries are the page's preconditions.
  const [info, settings, list] = await Promise.allSettled([api.info(x.hostId), api.settings(x.hostId), api.list(x.hostId)])
  const failed: Array<['info' | 'settings' | 'list', PromiseSettledResult<unknown>]> =
    [['info', info], ['settings', settings], ['list', list]]
  for (const [call, r] of failed) {
    if (r.status === 'rejected') {
      const snap: PairingSnapshot = { self: null, error: { call, message: msg(r.reason) }, rows: [], candidates: [] }
      emit(snap)
      return snap
    }
  }
  const self = {
    host_id: (info as PromiseFulfilledResult<{ host_id: string }>).value.host_id,
    self_alias: (settings as PromiseFulfilledResult<PeerSettings>).value.alias,
    self_alias_source: (settings as PromiseFulfilledResult<PeerSettings>).value.alias_source,
  }
  const entries = (list as PromiseFulfilledResult<PeerHostRow[]>).value

  // Step 2: every other App host's metadata, or the reason it could not be asked.
  const metas = await Promise.all(others.filter((h) => h.hostId !== x.hostId).map((h) => loadMeta(h, api)))
  const joinCandidates: CounterpartCandidate[] = metas.map((m) => ({
    hostId: m.hostId, url: m.url, host_id: m.available ? m.host_id : '',
  }))
  const metaById = new Map(metas.map((m) => [m.hostId, m]))

  // Step 3: join, then list each distinct available counterpart once. The
  // memo is shared with the candidate scan below, so a host is listed once
  // per run whichever role it plays — and this is the PRE-dial read.
  const listOnce = new Map<string, Promise<PeerHostRow[] | Error>>()
  const listOf = (hostId: string) => {
    let p = listOnce.get(hostId)
    if (!p) { p = api.list(hostId).catch(toError); listOnce.set(hostId, p) }
    return p
  }
  const ours = { host_id: self.host_id, url: x.url }

  const joined = entries.map((entry) => ({ entry, y: matchCounterpart(entry, joinCandidates) }))
  const counterpartIds = new Set(joined.flatMap(({ y }) => (y ? [y.hostId] : [])))

  const rowsP = Promise.all(joined.map(async ({ entry, y }): Promise<PairingRow> => {
    if (!y) {
      return { entry, counterpart: null, counterpartCause: '', returnEntry: null, outbound: 'pending', inbound: 'not-app-host',
        gateStale: preDialGate(entry, null) }
    }
    const meta = metaById.get(y.hostId)!
    const counterpart = { hostId: meta.hostId, name: meta.name }
    if (!meta.available) {
      return { entry, counterpart, counterpartCause: meta.cause, returnEntry: null, outbound: 'pending', inbound: 'counterpart-unavailable',
        gateStale: preDialGate(entry, null) }
    }
    const theirs = await listOf(y.hostId)
    if (theirs instanceof Error) {
      return { entry, counterpart, counterpartCause: `list: ${msg(theirs)}`, returnEntry: null, outbound: 'pending', inbound: 'counterpart-unavailable',
        gateStale: preDialGate(entry, null) }
    }
    // The same join rule with the roles swapped: which of THEIR entries is us?
    const returnEntry = matchReturnEntry(ours, theirs)
    return { entry, counterpart, counterpartCause: '', returnEntry, outbound: 'pending', inbound: returnEntry ? 'pending' : 'no-entry',
      gateStale: preDialGate(entry, returnEntry) }
  }))

  // Pair candidates (spec §7.1): available App hosts that no row joined to.
  // An unavailable host is not listed ("a host whose info could not be
  // fetched is not listed"); each candidate is listed to detect the repair
  // case (Y already holds an entry for X).
  const candidatesP = Promise.all(metas.flatMap((m): Promise<PairCandidate>[] => {
    if (!m.available || counterpartIds.has(m.hostId)) return []
    const base = { hostId: m.hostId, name: m.name, url: m.url, host_id: m.host_id }
    return [listOf(m.hostId).then((theirs) => (theirs instanceof Error
      ? { ...base, returnEntry: null, listError: msg(theirs) }
      : { ...base, returnEntry: matchReturnEntry(ours, theirs), listError: '' }))]
  }))

  const [rows, candidates] = await Promise.all([rowsP, candidatesP])
  const snap: PairingSnapshot = { self, error: null, rows, candidates }
  emit({ ...snap })

  // Step 4: every direction that has an entry, verified in parallel; one emit per settle.
  const settle = (i: number, side: 'outbound' | 'inbound', p: Promise<PeerHostVerify>) =>
    p.then(toOutcome, (e: unknown) => ({ ok: false as const, error: msg(e) }))
      .then((outcome) => {
        snap.rows = snap.rows.map((r, j) => (j === i ? { ...r, [side]: outcome } : r))
        emit({ ...snap })
      })

  const dials: Promise<void>[] = []
  rows.forEach((r, i) => {
    dials.push(settle(i, 'outbound', api.verify(x.hostId, r.entry.alias)))
    if (r.counterpart && r.returnEntry) dials.push(settle(i, 'inbound', api.verify(r.counterpart.hostId, r.returnEntry.alias)))
  })
  await Promise.all(dials)

  // Step 5 (spec §7.3): a pending rotation's evidence (`last_inbound_auth`) is
  // written by the peer's dial, so the rows read in steps 1/3 predate it.
  // Re-read — strictly AFTER every dial has settled, never before or
  // concurrently — each host that owns a pending entry, with a FRESH list (not
  // the step-3 memo), and swap in the row of the same alias. A failed re-read
  // or a vanished alias keeps the old row and leaves that side stale; the
  // component then withholds Commit/Cancel. Candidates are not re-read: X
  // cannot dial a host it has no entry for.
  const rereadHosts = new Set<string>()
  for (const r of snap.rows) {
    if (r.entry.rotation_pending) rereadHosts.add(x.hostId)
    if (r.counterpart && r.returnEntry?.rotation_pending) rereadHosts.add(r.counterpart.hostId)
  }
  if (rereadHosts.size > 0) {
    const fresh = new Map<string, PeerHostRow[] | Error>()
    await Promise.all([...rereadHosts].map(async (h) => { fresh.set(h, await api.list(h).catch(toError)) }))
    const freshRow = (h: string, alias: string): PeerHostRow | undefined => {
      const l = fresh.get(h)
      return l instanceof Error || l === undefined ? undefined : l.find((e) => e.alias === alias)
    }
    snap.rows = snap.rows.map((r) => {
      let next = r
      if (r.entry.rotation_pending) {
        const e = freshRow(x.hostId, r.entry.alias)
        if (e) next = { ...next, entry: e, gateStale: { ...next.gateStale, entry: false } }
      }
      if (r.counterpart && r.returnEntry?.rotation_pending) {
        const e = freshRow(r.counterpart.hostId, r.returnEntry.alias)
        if (e) next = { ...next, returnEntry: e, gateStale: { ...next.gateStale, returnEntry: false } }
      }
      return next
    })
    emit({ ...snap })
  }
  return { ...snap }
}
