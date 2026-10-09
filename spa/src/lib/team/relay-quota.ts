// spa/src/lib/team/relay-quota.ts — the App's picture of the relay quotas (plan RQ-A Task 3; spec §3). A small zustand
// store, never persisted: every window follows the daemon through GET rows, `team.relay_quota` events and PUT answers.
//
// The numbers belong to a CHAIN, so everything is keyed by (host, root session id): every row of one root shows the same
// numbers, and a click on any of them writes the same thing.
//
// Confirmed numbers: per (host, root) the last pair the daemon confirmed with its `rev` (daemon D2: +1 on every write of
// that root's row). A GET row, an event or an answer replaces them only when its `rev` is NOT SMALLER than the one held, so
// no arrival order can roll a number back. Events that arrive while a host's GET is in flight are buffered and applied
// after the rows, by the same rule.
//
// Writes: per (host, root, field) a *desired* value (clicked, not yet sent) and an *in-flight* one (sent, not answered).
// A stepper shows the desired value while there is one, else the in-flight one, else the confirmed one - so an answer or
// an event about the other field (both carry the whole pair) never changes what this stepper shows. The writer
// (relay-quota-writer.ts) drives these; this module only holds and merges.
import { create } from 'zustand'
import type { RelayQuotaEvent, RelayQuotaField, RelayQuotaPair, SessionQuota } from './types'

export interface ConfirmedQuota extends RelayQuotaPair {
  rev: number
}

export interface FieldWrite {
  /** Clicked and not yet sent (or sent and clicked over since). */
  desired?: number
  /** Sent, not answered yet. */
  inflight?: number
}

const SEP = '\u0000'
export const quotaKey = (hostId: string, root: string): string => `${hostId}${SEP}${root}`
export const fieldKey = (hostId: string, root: string, field: RelayQuotaField): string => `${quotaKey(hostId, root)}${SEP}${field}`

/** The confirmed numbers after `next` arrives: `next` when nothing is held or its rev is not smaller. */
function merge(cur: ConfirmedQuota | undefined, next: ConfirmedQuota): ConfirmedQuota {
  return cur === undefined || next.rev >= cur.rev ? next : cur
}

interface GetState {
  depth: number
  events: RelayQuotaEvent[]
}

interface RelayQuotaState {
  confirmed: Record<string, ConfirmedQuota>
  writes: Record<string, FieldWrite>
  /** Per host, the GETs in flight and the events buffered meanwhile (absent = none in flight). */
  gets: Record<string, GetState>

  beginGet: (hostId: string) => void
  /** The GET ended: its rows (null = it failed) are applied, then the events buffered while it was out. */
  endGet: (hostId: string, rows: readonly SessionQuota[] | null) => void
  applyEvent: (hostId: string, ev: RelayQuotaEvent) => void
  applyAnswer: (hostId: string, root: string, pair: RelayQuotaPair, rev: number) => void
  setWrite: (hostId: string, root: string, field: RelayQuotaField, write: FieldWrite) => void
  clearWrite: (hostId: string, root: string, field: RelayQuotaField) => void
  forgetHost: (hostId: string) => void
  reset: () => void
}

function withConfirmed(confirmed: Record<string, ConfirmedQuota>, hostId: string, root: string, next: ConfirmedQuota): Record<string, ConfirmedQuota> {
  const key = quotaKey(hostId, root)
  const merged = merge(confirmed[key], next)
  return merged === confirmed[key] ? confirmed : { ...confirmed, [key]: merged }
}

export const useRelayQuotaStore = create<RelayQuotaState>()((set) => ({
  confirmed: {},
  writes: {},
  gets: {},

  beginGet: (hostId) => set((s) => {
    const cur = s.gets[hostId]
    return { gets: { ...s.gets, [hostId]: { depth: (cur?.depth ?? 0) + 1, events: cur?.events ?? [] } } }
  }),

  endGet: (hostId, rows) => set((s) => {
    const cur = s.gets[hostId]
    if (cur === undefined) return s // the host was forgotten while the GET was out: its answer is the old daemon's
    let confirmed = s.confirmed
    if (rows) for (const r of rows) confirmed = withConfirmed(confirmed, hostId, r.root_session_id, { self_left: r.self_left, member_pool_left: r.member_pool_left, rev: r.rev })
    if (cur.depth > 1) return { confirmed, gets: { ...s.gets, [hostId]: { depth: cur.depth - 1, events: cur.events } } }
    for (const e of cur.events) confirmed = withConfirmed(confirmed, hostId, e.root_session_id, { self_left: e.self_left, member_pool_left: e.member_pool_left, rev: e.rev })
    const { [hostId]: _done, ...gets } = s.gets
    return { confirmed, gets }
  }),

  applyEvent: (hostId, ev) => set((s) => {
    const get = s.gets[hostId]
    if (get !== undefined) return { gets: { ...s.gets, [hostId]: { depth: get.depth, events: [...get.events, ev] } } }
    const confirmed = withConfirmed(s.confirmed, hostId, ev.root_session_id, { self_left: ev.self_left, member_pool_left: ev.member_pool_left, rev: ev.rev })
    return confirmed === s.confirmed ? s : { confirmed }
  }),

  applyAnswer: (hostId, root, pair, rev) => set((s) => {
    const confirmed = withConfirmed(s.confirmed, hostId, root, { ...pair, rev })
    return confirmed === s.confirmed ? s : { confirmed }
  }),

  setWrite: (hostId, root, field, write) => set((s) => ({ writes: { ...s.writes, [fieldKey(hostId, root, field)]: write } })),

  clearWrite: (hostId, root, field) => set((s) => {
    const key = fieldKey(hostId, root, field)
    if (!Object.hasOwn(s.writes, key)) return s
    const { [key]: _gone, ...writes } = s.writes
    return { writes }
  }),

  forgetHost: (hostId) => set((s) => {
    const prefix = `${hostId}${SEP}`
    const keep = <T,>(rec: Record<string, T>): Record<string, T> => Object.fromEntries(Object.entries(rec).filter(([k]) => !k.startsWith(prefix)))
    const { [hostId]: _gone, ...gets } = s.gets
    return { confirmed: keep(s.confirmed), writes: keep(s.writes), gets }
  }),

  reset: () => set({ confirmed: {}, writes: {}, gets: {} }),
}))

/** Every host the store holds something for: a confirmed row, a write, or a GET in flight. */
export function quotaHostIds(s: Pick<RelayQuotaState, 'confirmed' | 'writes' | 'gets'>): Set<string> {
  const ids = new Set<string>(Object.keys(s.gets))
  for (const k of [...Object.keys(s.confirmed), ...Object.keys(s.writes)]) ids.add(k.split(SEP)[0])
  return ids
}

/**
 * What the stepper of `field` shows: its desired value while one is set, else the in-flight one, else the confirmed one
 * (else `fallback`, the GET row's own number before anything was confirmed).
 */
export function shownValue(
  s: Pick<RelayQuotaState, 'confirmed' | 'writes'>,
  hostId: string,
  root: string,
  field: RelayQuotaField,
  fallback: number,
): number {
  const w = s.writes[fieldKey(hostId, root, field)]
  if (w?.desired !== undefined) return w.desired
  if (w?.inflight !== undefined) return w.inflight
  return s.confirmed[quotaKey(hostId, root)]?.[field] ?? fallback
}

/** True while `field` has a write pending or in flight. */
export function hasWrite(s: Pick<RelayQuotaState, 'writes'>, hostId: string, root: string, field: RelayQuotaField): boolean {
  const w = s.writes[fieldKey(hostId, root, field)]
  return w !== undefined && (w.desired !== undefined || w.inflight !== undefined)
}
