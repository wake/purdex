// spa/src/lib/profile/sync-state.ts — the per-section sync state machine of
// Profile Sync (spec §4.4, §4.6.1, §4.6.2, §4.6.3; P2a plan Task 4). A pure
// reducer in the style of lib/nex/event-reducer.ts: no store, no fetch, no
// timer, no clock, no randomness, no React. P2b's driver feeds it events and
// asks `decideSection` what to do next; nothing here performs an effect.
//
// The core holds HASHES ONLY. `retainedHashes` names the payloads the driver
// must keep alive (the one in flight, both sides of an open conflict, the
// snapshot awaiting restore).
//
// Conventions every transition obeys:
//   - Inputs are never mutated (the tests feed deep-frozen states and events).
//   - An event that changes nothing returns THE SAME state reference.
//   - An event that changes anything — any field at all, `currentHash` and
//     `indexStale` included — bumps `epoch` by exactly one. A FlightToken
//     carries the epoch it was made at, so it dies the moment the state it was
//     made in is gone (§4.6.2 rule 4).
//   - An index request carries `indexEpoch` instead (§4.6.2 rule 3). The index
//     is knowledge about the SOT, so its freshness must not depend on what the
//     user types: `indexEpoch` moves only when `base`, `sot` or `inFlight`
//     (a flight opening or closing) change, and on an applied `reconnected`.
//     `local-changed`, `locked`, `resolved`, `local-restored` and `indexStale`
//     flips leave it alone. Were it tied to `epoch`, a user typing steadily
//     (one `local-changed` per debounce) would invalidate every index response
//     before it landed, and `indexStale` — which shadows push, pull and lock —
//     would starve the section forever.
//     Why `reconnected` DOES bump it: a request sent before the connection
//     dropped describes the SOT at some time T. Whatever this client learnt
//     after T (remote-event, push result) has already bumped `indexEpoch`. But
//     the reason `reconnected` re-stales the index is the events this client
//     did NOT learn — those lost while disconnected. A pre-drop response that
//     lands after the reconnect may predate them; accepting it would clear
//     `indexStale` with an old view and nothing would ask again. So the
//     reconnect kills it. (A reconnect is not user-driven, so this cannot
//     starve.) Known gap, as before this field existed: a `reconnected` that
//     finds the index ALREADY stale is a no-op and kills nothing — the driver
//     must drop an index request that was outstanding across a disconnect.
//   - `status` is stored, but only its `locked:*` values are *decided* (by the
//     `locked` event, by a 409, and by `resolved`). `synced` / `pending` are
//     re-derived at the end of every event: `pending` iff the section is dirty
//     or a flight is open, `synced` otherwise.
//   - Convergence (rule 1) is folded at the end of every event, except while a
//     flight is open (its terminal event folds) and while locked (the user
//     decides; `resolved` re-runs the fold).
//   - Whatever moves `sot` while a conflict is open moves `conflict.sot` with
//     it: a lock refuses to *apply*, not to *know* (plan-review finding #6).
//
//   - A 409 locks only if something unsynced is left. If the section was edited
//     back to the base while the push was in flight (`currentHash === base.hash`
//     when the 409 lands), no conflict is opened: the snapshot that was sent has
//     already been abandoned, the section is clean, and row 3 pulls. Edited to a
//     third value, it is still dirty → `locked:conflict`, the local side being
//     the snapshot that was SENT.
//   - Nothing is decided on an index this client has not seen. A fresh or
//     restored state starts `indexStale`, and `reconnected` sets it again, so the
//     first action of every connection is `reindex` (spec §4.6, reconcile on
//     connect). Row 0d (`reindex`) shadows every row below it.
//   - Decision order, first match wins: 0a locked → 0b in flight → 0c
//     `restore-local` → 0d `reindex` → 0e `forcePull` → rows 1–8. The restore
//     sits ABOVE the reindex because putting the sent snapshot back is a purely
//     local action: it must not queue behind a step that needs the network
//     (`indexStale` is the normal state offline and after every `reconnected`),
//     or edits made while waiting would be overwritten by the late restore. It
//     touches no rev and sends nothing, so it is epoch-safe on a stale index.
//
// Driver contract (P2b), stated here because this reducer is what makes it safe:
//   - connect / reconnect to the dev host: dispatch `reconnected` to EVERY
//     section, GET the index, then dispatch one `sot-index` per section carrying
//     the `indexEpoch` that section had when the request was SENT (i.e. read
//     after `reconnected`). `reconnected` does not close a flight: whatever was on the
//     wire still owes its one terminal event (`push-failed` on timeout).
//   - startup: sections with a persisted base are rebuilt with
//     `restoreSectionState`, never-synced ones with `initialSectionState`. Only
//     `base` is persisted; flights, locks, conflicts, `forcePull` and
//     `restoreLocal` are not — an unresolved conflict is re-derived by the
//     decision table (row 8) after the first index.
//   - push / delete: dispatch `push-started` with the token from
//     `decideSection`, then send the request only if the resulting
//     `inFlight === token` (same object). Every outcome — timeouts and
//     malformed responses included — must end in exactly one terminal event.
//   - lock-conflict / lock-reset: dispatch `{type:'locked', reason}`. It is
//     refused if the state no longer calls for that lock.
//   - pull: fetch, then check `canApplyPull` on the *current* state before
//     touching the stores; then dispatch `pull-applied`. For an absent SOT
//     (404) the deletion is applied and reported with `rev = state.sot.rev`.
//   - sot-index: tag the response with the `indexEpoch` the `reindex` action
//     carried (i.e. read when the request was *sent*), unchanged.
//   - restore-local: a pending restore is CANCELLED by any `local-changed` that
//     really changes the live hash — what the user typed after choosing
//     keep-local beats the snapshot that choice was about. So, before writing
//     the snapshot into the stores, check `canRestoreLocal(state, hash)` on the
//     *current* state (i.e. `restoreLocal?.hash` still is the hash about to be
//     restored); then dispatch `local-restored`. If that event is ignored (same
//     state reference back) the restore had been cancelled in between: the
//     driver must NOT write the snapshot. `local-restored` is accepted only
//     for the pending hash; anything else never touches `currentHash`.

/** What one side holds. `hash === null` ⇔ the section does not exist there
 *  (never created, or a P1 tombstone — the client cannot and need not tell them apart). */
export interface Held {
  rev: number
  hash: string | null
}

export type SectionStatus = 'synced' | 'pending' | 'locked:conflict' | 'locked:reset'

export interface FlightToken {
  kind: 'put' | 'delete'
  /** Hash of the payload being sent; null for a delete. */
  hash: string | null
  /** The `baseRev` that goes on the wire — see `wireBaseRev`. */
  baseRev: number
  /** The state epoch the decision was made at. */
  epoch: number
}

export interface SectionConflict {
  /** The snapshot that was *sent* (or, for a decide-time lock, the live hash at lock time). */
  localHash: string | null
  /** The newest SOT state known; keeps advancing while locked. */
  sot: Held
}

export interface SectionSyncState {
  /** What this client last agreed with the SOT on. `{0, null}` = nothing yet. */
  base: Held
  /** Hash of the live local payload; null = does not exist locally. */
  currentHash: string | null
  /** Newest SOT state this client has observed. `rev` decreases only through an index made at the current `indexEpoch`. */
  sot: Held
  /** Bumped by every state change. Guards flight tokens. */
  epoch: number
  /** Bumped only when `base`, `sot` or `inFlight` change, and by an applied
   *  `reconnected`. Guards index responses — local edits never move it. */
  indexEpoch: number
  status: SectionStatus
  inFlight: FlightToken | null
  sotMovedWhileInFlight: boolean
  conflict: SectionConflict | null
  /** Set by `resolved keep:'sot'`: pull even though the section is dirty. */
  forcePull: boolean
  /** Set by `resolved keep:'local'`: the sent snapshot the driver must put back.
   *  `null` = nothing to restore; `{ hash: null }` = restore to "does not exist"
   *  (the 409 was on a delete). Cleared by the restore itself and by any
   *  `local-changed` that changes the live hash (a later edit wins). */
  restoreLocal: { hash: string | null } | null
  /** The index must be (re)fetched before anything else: never seen yet, a
   *  response was discarded, or the connection was (re)established. */
  indexStale: boolean
}

export type SectionEvent =
  | { type: 'local-changed'; hash: string | null }
  | { type: 'reconnected' } // the dev host became reachable (first connect included)
  | { type: 'sot-index'; epoch: number; entry: { rev: number; hash: string } | null } // epoch = the reindex action's indexEpoch; entry null = not listed
  | { type: 'remote-event'; rev: number; hash: string | null; own: boolean } // hash null = deleted
  | { type: 'push-started'; token: FlightToken }
  | { type: 'push-applied'; rev: number } // 200 applied:true / DELETE 200
  | { type: 'push-converged'; rev: number } // 200 applied:false
  | { type: 'push-conflict'; rev: number; hash: string | null } // 409 conflict; rev 0 + null = absent
  | { type: 'push-failed' } // network, 5xx, 503, timeout, malformed
  | { type: 'pull-applied'; rev: number; hash: string | null } // null = applied a deletion
  | { type: 'local-restored'; hash: string | null } // the driver put the snapshot back
  | { type: 'resolved'; keep: 'local' | 'sot' }
  | { type: 'locked'; reason: 'conflict' | 'reset' } // the driver acting on lock-conflict / lock-reset

export type SectionAction =
  | { do: 'nothing' }
  | { do: 'reindex'; indexEpoch: number } // echo `indexEpoch` as the `epoch` of the resulting `sot-index`
  | { do: 'pull' }
  | { do: 'push'; token: FlightToken }
  | { do: 'delete'; token: FlightToken }
  | { do: 'restore-local'; hash: string | null }
  | { do: 'lock-conflict' }
  | { do: 'lock-reset' }

export interface SectionContext {
  reachable: boolean
  autoSync: boolean
}

const NOTHING: SectionAction = { do: 'nothing' }

/** A section this client has never synced. The index has not been seen, so the
 *  first decision is `reindex` (offline: `nothing`, a dirty section reading `pending`). */
export function initialSectionState(currentHash: string | null): SectionSyncState {
  return restoreSectionState({ base: { rev: 0, hash: null }, currentHash })
}

/** Rebuild a section from what P2b persists device-locally: the agreed `base`
 *  and the hash of the live payload. `sot` starts as a copy of `base` — a
 *  placeholder that `indexStale` keeps anyone from acting on. Flight, lock,
 *  conflict, `forcePull` and `restoreLocal` are deliberately NOT persisted:
 *  after a restart every section reconciles from scratch, and an unresolved
 *  conflict is re-derived by the decision table once the index is in. */
export function restoreSectionState(persisted: { base: Held; currentHash: string | null }): SectionSyncState {
  const { base, currentHash } = persisted
  return {
    base: { rev: base.rev, hash: base.hash },
    currentHash,
    sot: { rev: base.rev, hash: base.hash },
    epoch: 0,
    indexEpoch: 0,
    status: currentHash === base.hash ? 'synced' : 'pending',
    inFlight: null,
    sotMovedWhileInFlight: false,
    conflict: null,
    forcePull: false,
    restoreLocal: null,
    indexStale: true,
  }
}

export function isDirty(s: SectionSyncState): boolean {
  return s.currentHash !== s.base.hash
}

export function sotMoved(s: SectionSyncState): boolean {
  return s.sot.hash !== s.base.hash || s.sot.rev > s.base.rev
}

function isLocked(s: SectionSyncState): boolean {
  return s.status === 'locked:conflict' || s.status === 'locked:reset'
}

/** May the driver apply a pulled payload (or deletion) to the stores right now?
 *  §4.6.2: apply and flush are mutually exclusive, and inbound lands only on a
 *  clean section — unless the user said "take the SOT" (`forcePull`). */
export function canApplyPull(s: SectionSyncState): boolean {
  return !isLocked(s) && s.inFlight === null && (s.forcePull || !isDirty(s))
}

/** May the driver put the snapshot `hash` back into the stores right now? True
 *  only while that very snapshot is the pending restore — an edit made after
 *  the user resolved cancels it. Check this BEFORE writing the stores;
 *  `local-restored` is accepted under exactly the same condition. */
export function canRestoreLocal(s: SectionSyncState, hash: string | null): boolean {
  return s.restoreLocal !== null && s.restoreLocal.hash === hash
}

/** Payloads the driver must keep, by hash: deduplicated, never null. */
export function retainedHashes(s: SectionSyncState): string[] {
  const out: string[] = []
  for (const h of [s.inFlight?.hash, s.conflict?.localHash, s.conflict?.sot.hash, s.restoreLocal?.hash]) {
    if (h !== null && h !== undefined && !out.includes(h)) out.push(h)
  }
  return out
}

/** P1 accepts a create — over nothing *or* over a tombstone — only with
 *  `baseRev 0` (sections.go, decision step 2). */
function wireBaseRev(s: SectionSyncState): number {
  return s.sot.hash === null ? 0 : s.base.rev
}

/** The decision table; first match wins. Order: 0a locked → 0b in flight →
 *  0c restore-local → 0d reindex → 0e forcePull → rows 1–8. `restore-local` is
 *  a local action (not gated by `reachable` / `autoSync`), so it must not wait
 *  behind `reindex`, which needs the network: offline the index stays stale
 *  indefinitely, and a restore that lands late overwrites the edits made since. */
export function decideSection(s: SectionSyncState, ctx: SectionContext): SectionAction {
  const online = ctx.reachable && ctx.autoSync
  /* 0a */ if (isLocked(s)) return NOTHING
  /* 0b */ if (s.inFlight !== null) return NOTHING
  /* 0c */ if (s.restoreLocal !== null && s.currentHash !== s.restoreLocal.hash) return { do: 'restore-local', hash: s.restoreLocal.hash }
  /* 0d */ if (s.indexStale) return online ? { do: 'reindex', indexEpoch: s.indexEpoch } : NOTHING
  /* 0e */ if (s.forcePull) return online ? { do: 'pull' } : NOTHING
  /* 1  */ if (s.sot.rev < s.base.rev) return { do: 'lock-reset' }
  const dirty = isDirty(s)
  const moved = sotMoved(s)
  /* 2  */ if (!dirty && !moved) return NOTHING
  /* 3  */ if (!dirty) return online ? { do: 'pull' } : NOTHING
  if (!moved) {
    if (!online) return NOTHING
    /* 4 */ if (s.currentHash !== null) {
      return { do: 'push', token: { kind: 'put', hash: s.currentHash, baseRev: wireBaseRev(s), epoch: s.epoch } }
    }
    /* 5 */ if (s.sot.hash !== null) {
      return { do: 'delete', token: { kind: 'delete', hash: null, baseRev: wireBaseRev(s), epoch: s.epoch } }
    }
    /* 6 — unreachable: currentHash null, not moved ⇒ base.hash null ⇒ clean */
    return NOTHING
  }
  /* 7 — the reducer folds this; kept so a hand-built state cannot lock on agreement */
  if (s.sot.hash === s.currentHash) return NOTHING
  /* 8  */ return { do: 'lock-conflict' }
}

function sameHeld(a: Held, b: Held): boolean {
  return a.rev === b.rev && a.hash === b.hash
}

/** Move `sot`, and an open conflict's view of it with it. */
function withSot(s: SectionSyncState, sot: Held): SectionSyncState {
  if (sameHeld(s.sot, sot)) return s
  return {
    ...s,
    sot,
    conflict: s.conflict === null ? null : { ...s.conflict, sot },
    sotMovedWhileInFlight: s.sotMovedWhileInFlight || s.inFlight !== null,
  }
}

function tokensEqual(a: FlightToken, b: FlightToken): boolean {
  return a.kind === b.kind && a.hash === b.hash && a.baseRev === b.baseRev && a.epoch === b.epoch
}

function unchanged(a: SectionSyncState, b: SectionSyncState): boolean {
  return (
    sameHeld(a.base, b.base) &&
    a.currentHash === b.currentHash &&
    sameHeld(a.sot, b.sot) &&
    a.status === b.status &&
    a.inFlight === b.inFlight &&
    a.sotMovedWhileInFlight === b.sotMovedWhileInFlight &&
    (a.conflict === b.conflict ||
      (a.conflict !== null && b.conflict !== null && a.conflict.localHash === b.conflict.localHash && sameHeld(a.conflict.sot, b.conflict.sot))) &&
    a.forcePull === b.forcePull &&
    (a.restoreLocal === b.restoreLocal || (a.restoreLocal !== null && b.restoreLocal !== null && a.restoreLocal.hash === b.restoreLocal.hash)) &&
    a.indexStale === b.indexStale
  )
}

/** The tail of every transition: drop a finished restore, fold convergence
 *  (rule 1), derive synced/pending, bump the epoch — or hand back `prev`. */
function finish(prev: SectionSyncState, draft: SectionSyncState): SectionSyncState {
  if (draft === prev) return prev
  let next = draft
  if (next.restoreLocal !== null && next.restoreLocal.hash === next.currentHash) next = { ...next, restoreLocal: null }
  if (!isLocked(next)) {
    if (next.inFlight === null && isDirty(next) && sotMoved(next) && next.sot.hash === next.currentHash) {
      next = { ...next, base: next.sot }
    }
    const status: SectionStatus = isDirty(next) || next.inFlight !== null ? 'pending' : 'synced'
    if (next.status !== status) next = { ...next, status }
  }
  if (unchanged(prev, next)) return prev
  // `step` may already have moved indexEpoch (reconnected); otherwise it follows base / sot / the flight.
  const sotKnowledgeMoved = !sameHeld(prev.base, next.base) || !sameHeld(prev.sot, next.sot) || prev.inFlight !== next.inFlight
  const indexEpoch = next.indexEpoch === prev.indexEpoch && sotKnowledgeMoved ? prev.indexEpoch + 1 : next.indexEpoch
  return { ...next, epoch: prev.epoch + 1, indexEpoch }
}

function closeFlight(s: SectionSyncState): SectionSyncState {
  return { ...s, inFlight: null, sotMovedWhileInFlight: false }
}

function step(s: SectionSyncState, e: SectionEvent): SectionSyncState {
  switch (e.type) {
    case 'local-changed':
      if (e.hash === s.currentHash) return s
      // An edit made AFTER the user chose keep-local wins over the snapshot that
      // choice was about: the pending restore is cancelled, not merely delayed.
      return { ...s, currentHash: e.hash, restoreLocal: null }

    case 'reconnected':
      // kills an index request sent before the drop — see the header for why
      return s.indexStale ? s : { ...s, indexStale: true, indexEpoch: s.indexEpoch + 1 }

    case 'remote-event':
      if (e.own || e.rev <= s.sot.rev) return s
      return withSot(s, { rev: e.rev, hash: e.hash })

    case 'sot-index': {
      if (e.epoch !== s.indexEpoch) return s.indexStale ? s : { ...s, indexStale: true }
      const sot: Held = e.entry !== null ? { rev: e.entry.rev, hash: e.entry.hash } : { rev: Math.max(s.sot.rev, s.base.rev), hash: null }
      const next = withSot(s, sot)
      return next.indexStale ? { ...next, indexStale: false } : next
    }

    case 'push-started': {
      // Stricter than "same epoch, no flight": the token must be the one this
      // very state decides. For an honest driver the two are equivalent (any
      // change bumps the epoch); for a forged or recycled token this is what
      // keeps a flight from opening on a locked, moved or re-edited section.
      const want = decideSection(s, { reachable: true, autoSync: true })
      if (want.do !== 'push' && want.do !== 'delete') return s
      if (!tokensEqual(want.token, e.token)) return s
      return { ...s, inFlight: e.token }
    }

    case 'push-applied':
    case 'push-converged': {
      if (s.inFlight === null) return s
      // the hash that was SENT — never currentHash (finding #9)
      const base: Held = { rev: e.rev, hash: s.inFlight.hash }
      const closed = { ...closeFlight(s), base }
      return e.rev > s.sot.rev ? withSot(closed, base) : closed
    }

    case 'push-conflict': {
      if (s.inFlight === null) return s
      const sent = s.inFlight.hash
      let next = closeFlight(s)
      if (e.rev === 0) {
        // "absent" is the server's view when it ANSWERED. If a live SOT was
        // observed while the push was out, that observation is newer than this
        // answer (a create raced ours and won): keep it. Erasing it would show
        // "absent" as the other side of the conflict and make keep-local push
        // with wire baseRev 0 again — straight into the next 409.
        const learntLive = s.sotMovedWhileInFlight && s.sot.hash !== null
        if (!learntLive) next = withSot(next, { rev: Math.max(s.sot.rev, s.base.rev), hash: null })
      } else if (e.rev > s.sot.rev) next = withSot(next, { rev: e.rev, hash: e.hash })
      // the SOT already holds what we have *now* → rule 1 folds it in finish()
      if (next.sot.hash === next.currentHash) return next
      // edited back to the base during the flight: nothing unsynced is left to
      // argue about — stay unlocked and let row 3 pull
      if (!isDirty(next)) return next
      return { ...next, status: 'locked:conflict', conflict: { localHash: sent, sot: next.sot } }
    }

    case 'push-failed':
      return s.inFlight === null ? s : closeFlight(s)

    case 'pull-applied': {
      if (!canApplyPull(s)) return s
      const held: Held = { rev: e.rev, hash: e.hash }
      const next = { ...s, base: held, currentHash: e.hash, forcePull: false }
      return e.rev >= s.sot.rev ? withSot(next, held) : next
    }

    case 'local-restored':
      // Only the snapshot that is pending right now. A late restore — cancelled
      // by an edit, or for another hash — must not touch `currentHash`.
      if (!canRestoreLocal(s, e.hash)) return s
      return { ...s, currentHash: e.hash, restoreLocal: null }

    case 'locked': {
      const want = decideSection(s, { reachable: true, autoSync: true })
      if (want.do !== (e.reason === 'conflict' ? 'lock-conflict' : 'lock-reset')) return s
      return e.reason === 'conflict'
        ? { ...s, status: 'locked:conflict', conflict: { localHash: s.currentHash, sot: s.sot } }
        : { ...s, status: 'locked:reset' }
    }

    case 'resolved': {
      if (!isLocked(s)) return s
      const unlocked: SectionSyncState = { ...s, status: 'pending', conflict: null }
      if (e.keep === 'sot') return { ...unlocked, forcePull: true }
      // conflict.sot and sot move together; locked:reset has no pair and reads sot
      const target = s.conflict !== null ? s.conflict.sot : s.sot
      return { ...unlocked, base: { rev: target.rev, hash: target.hash }, restoreLocal: s.conflict !== null ? { hash: s.conflict.localHash } : null }
    }
  }
}

export function reduceSection(s: SectionSyncState, e: SectionEvent): SectionSyncState {
  return finish(s, step(s, e))
}
