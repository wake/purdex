// spa/src/lib/peer-pairing-load.ts — what the Peers page does on mount and on
// Refresh (Phase D spec §5.2), as one async function over an injected API so
// it is testable without React and the component only renders snapshots.
//
// Cost per run with n entries on X and m connected other hosts: 3 calls on X,
// ≤ 2 per other connected host, 1 list per host that is somebody's
// counterpart, and ≤ 2n verifies — all in parallel; nothing cached across
// runs (spec D-6: a stale green is the thing this page exists to remove).
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
}

export interface PairingSnapshot {
  self: { host_id: string; self_alias: string } | null
  /** Page-level failure of one of X's three precondition calls (spec §5.2 step 0). */
  error: { call: 'info' | 'settings' | 'list'; message: string } | null
  rows: PairingRow[]
}

// Page-level and per-host causes carry the daemon's own `{error}` text when there is one.
const msg = (e: unknown) => (e instanceof HostApiError ? e.detail : e instanceof Error ? e.message : String(e))

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
      const snap: PairingSnapshot = { self: null, error: { call, message: msg(r.reason) }, rows: [] }
      emit(snap)
      return snap
    }
  }
  const self = {
    host_id: (info as PromiseFulfilledResult<{ host_id: string }>).value.host_id,
    self_alias: (settings as PromiseFulfilledResult<PeerSettings>).value.alias,
  }
  const entries = (list as PromiseFulfilledResult<PeerHostRow[]>).value

  // Step 2: every other App host's metadata, or the reason it could not be asked.
  const metas = await Promise.all(others.filter((h) => h.hostId !== x.hostId).map((h) => loadMeta(h, api)))
  const candidates: CounterpartCandidate[] = metas.map((m) => ({
    hostId: m.hostId, url: m.url, host_id: m.available ? m.host_id : '',
  }))
  const metaById = new Map(metas.map((m) => [m.hostId, m]))

  // Step 3: join, then list each distinct available counterpart once.
  const listOnce = new Map<string, Promise<PeerHostRow[] | Error>>()
  const listOf = (hostId: string) => {
    let p = listOnce.get(hostId)
    if (!p) { p = api.list(hostId).catch((e: unknown) => (e instanceof Error ? e : new Error(String(e)))); listOnce.set(hostId, p) }
    return p
  }

  const rows: PairingRow[] = await Promise.all(entries.map(async (entry): Promise<PairingRow> => {
    const y = matchCounterpart(entry, candidates)
    if (!y) return { entry, counterpart: null, counterpartCause: '', returnEntry: null, outbound: 'pending', inbound: 'not-app-host' }
    const meta = metaById.get(y.hostId)!
    const counterpart = { hostId: meta.hostId, name: meta.name }
    if (!meta.available) {
      return { entry, counterpart, counterpartCause: meta.cause, returnEntry: null, outbound: 'pending', inbound: 'counterpart-unavailable' }
    }
    const theirs = await listOf(y.hostId)
    if (theirs instanceof Error) {
      return { entry, counterpart, counterpartCause: `list: ${theirs.message}`, returnEntry: null, outbound: 'pending', inbound: 'counterpart-unavailable' }
    }
    // The same join rule with the roles swapped: which of THEIR entries is us?
    const returnEntry = matchReturnEntry({ host_id: self.host_id, url: x.url }, theirs)
    return { entry, counterpart, counterpartCause: '', returnEntry, outbound: 'pending', inbound: returnEntry ? 'pending' : 'no-entry' }
  }))

  const snap: PairingSnapshot = { self, error: null, rows }
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
  return { ...snap }
}
