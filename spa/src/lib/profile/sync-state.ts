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
//     (a flight opening or closing) change, and on EVERY `reconnected`.
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
//     reconnect kills it — unconditionally: `reconnected` bumps `indexEpoch`
//     whether or not the index was already stale (it therefore always returns
//     a new state). The already-stale case is the one that matters most: the
//     first `reindex` of a section, or any one still unanswered, is out while
//     `indexStale` is true; were `reconnected` a no-op there, that pre-drop
//     response would still match and be adopted. Invalidating every index
//     request outstanding across a reconnect is a guarantee of this reducer,
//     not a duty of the driver. (A reconnect is not user-driven, so this
//     cannot starve.)
//   - `status` is stored, but only its `locked:*` values are *decided* (by the
//     `locked` event, by a 409, and by `resolved`). `synced` / `pending` are
//     re-derived at the end of every event: `pending` iff the section is dirty
//     or a flight is open, `synced` otherwise.
//   - Convergence (rule 1) is folded at the end of every event, except while a
//     flight is open (its terminal event folds), while locked (the user
//     decides; `resolved` re-runs the fold) and while `indexStale`: stale means
//     "what this client believes about the SOT is not to be trusted", and
//     `synced` must not be declared on such a belief (the event that said "they
//     hold our hash" may itself have been overtaken by one that was lost).
//     Nothing is lost by waiting: row 0d reindexes, and the `sot-index` that
//     clears `indexStale` folds in that very event.
//   - Whatever moves `sot` while a conflict is open moves `conflict.sot` with
//     it: a lock refuses to *apply*, not to *know* (plan-review finding #6).
//
//   - A 409 locks only if something unsynced is left. If the section was edited
//     back to the base while the push was in flight (`currentHash === base.hash`
//     when the 409 lands), no conflict is opened: the snapshot that was sent has
//     already been abandoned, the section is clean, and row 3 pulls. Edited to a
//     third value, it is still dirty → `locked:conflict`, the local side being
//     the snapshot that was SENT.
//   - One 409 decides nothing: `{rev:0}` ("absent / tombstone") on a flight
//     during which a `remote-event` showed a LIVE SOT. Events (WebSocket) and
//     HTTP answers travel on different channels and carry no causal order, so
//     "the event arrived while the push was out" does not say which of the two
//     is newer. Both orders are real:
//       A. our PUT was judged first (absent → 409), THEN someone created
//          {5,H8}; the event overtook the 409. The live {5,H8} is the truth.
//          Believing the 409 would show "absent" as the other side and make
//          keep-local push with wire baseRev 0 — into the next 409.
//       B. someone created {5,H8} (event seen), then it was deleted (rev 6, a
//          tombstone; event lost or late), and THEN our PUT hit the tombstone:
//          the 409 is authoritative and "absent" is the truth. Believing the
//          event would lock against a payload that no longer exists and make
//          keep-local push with wire baseRev 5, which a tombstone refuses
//          (it takes baseRev 0 only) — a conflict that re-opens forever.
//     The client cannot tell A from B, and either guess loops in the other
//     case. So it does not guess: the flight closes, `sot` is left exactly as
//     it was, NO lock and NO conflict pair are made (even when dirty), and
//     `indexStale` is set. The index is the authoritative source; once it lands
//     the table decides as always — live and not ours → row 8 `lock-conflict`
//     (decide-time, the local side being the live hash); absent → row 4 push
//     with wire baseRev 0; live and ours → folded to `synced`. The fold is held
//     back meanwhile by the `indexStale` rule above.
//   - "Absent" has no rev (`sotMoved`): the client cannot tell "never created"
//     from a tombstone, P1 accepts a create over either with baseRev 0 only, so
//     base absent + SOT absent is "not moved" whatever revs the two carry.
//   - Nothing is decided on an index this client has not seen. A fresh or
//     restored state starts `indexStale`, and `reconnected` sets it again, so the
//     first action of every connection is `reindex` (spec §4.6, reconcile on
//     connect). Row 0d (`reindex`) shadows every row below it.
//   - `locked:invalid` (P2b plan Task 6): the SOT holds a payload this client
//     REFUSES TO APPLY — ill-formed, settings with `rejected` entries, a `hosts`
//     payload that removes or re-points the master's own host. Pulling it again
//     every 30 s would fetch the same unusable bytes for ever, and pretending
//     it was applied would be a lie. So the driver says `locked{invalid, rev}`
//     and the section shuts, remembering the SOT the verdict was about
//     (`invalid`, a copy of `sot` at that moment). The event is accepted only
//     if this very state decides `pull` and `rev` is the `sot.rev` held — a
//     verdict on any other payload, or on a state that no longer wants it, is
//     stale. `forcePull` is dropped by the lock: the user's "take the SOT"
//     cannot be honoured, and forcePull and locked never coexist. (So a section
//     that was dirty when it shut — judged during a forcePull pull — and is
//     later opened by an observation comes back dirty against a moved SOT: row
//     8, `lock-conflict`, and the user is asked again.) It opens in two ways:
//       · a SOT observation (`remote-event`, or a `sot-index` made at the
//         current `indexEpoch`) that leaves `sot` different from `invalid` IN
//         ANY WAY — rev or hash. Not "a higher rev": absent has no rev to
//         compare (a section no longer listed reads `{max(...), null}`, which
//         may well be the refused rev), and a rebuilt profile makes the rev go
//         DOWN. Both mean the same thing as a higher rev does: the payload that
//         was refused is no longer what the SOT holds. The status is re-derived
//         and the table takes over — pull the new rev, pull the deletion, or
//         row 1 `lock-reset`. An observation of the SAME `{rev, hash}` never
//         opens it, so no index poll can turn into a re-fetch loop; and while
//         it is shut, `sot` equals `invalid`.
//       · `resolved keep:'local'`: `base = sot`, and the local copy is pushed
//         over the refused one. `resolved keep:'sot'` is REFUSED: that payload
//         is the one that cannot be applied.
//     It is never persisted: after a restart the section reindexes, pulls, and
//     is judged again — cheap, and it cannot go stale on disk.
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
//     `restoreSectionState`, never-synced ones with `initialSectionState`.
//     `base` is persisted, and so is an open `conflict` pair (with both
//     payloads): a 409's local side is the snapshot that was SENT, which the
//     table cannot re-derive. Flights, `locked:reset`, `locked:invalid`,
//     `forcePull` and `restoreLocal` are not — a decide-time conflict that was
//     never recorded is re-derived by the decision table (row 8) after the
//     first index.
//   - push / delete: dispatch `push-started` with the token from
//     `decideSection`, then send the request only if the resulting
//     `inFlight === token` (same object). Every outcome — timeouts and
//     malformed responses included — must end in exactly one terminal event.
//   - lock-conflict / lock-reset: dispatch `{type:'locked', reason}`. It is
//     refused if the state no longer calls for that lock.
//   - pull: fetch, then check `canApplyPull` on the *current* state before
//     touching the stores; then dispatch `pull-applied`. If the payload cannot
//     be applied, dispatch `{type:'locked', reason:'invalid', rev}` with the rev
//     that was FETCHED instead, and touch nothing. If that is refused (same
//     reference back) the verdict was stale — the SOT moved on, or a
//     `reconnected` staled the index while the fetch was out; just decide
//     again. An accepted one CLEARS `forcePull`: if the pull was the user's
//     "take the SOT" on a dirty section, that choice is spent. When a different
//     SOT is later observed the section reopens dirty against a moved SOT and
//     decides `lock-conflict` — the user is asked again, nothing is pulled over
//     their edits on the strength of an answer given about another payload.
//     Never re-fetch on a timer while `locked:invalid`: `decideSection` says
//     `nothing` until the SOT observed differs from the one refused. For an absent SOT
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

export type SectionStatus = 'synced' | 'pending' | 'locked:conflict' | 'locked:reset' | 'locked:invalid'

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
  /** Bumped only when `base`, `sot` or `inFlight` change, and by every
   *  `reconnected`. Guards index responses — local edits never move it. */
  indexEpoch: number
  status: SectionStatus
  inFlight: FlightToken | null
  sotMovedWhileInFlight: boolean
  conflict: SectionConflict | null
  /** Non-null ⇔ `status === 'locked:invalid'`: the SOT (a copy of `sot` at lock
   *  time) whose payload this client refused to apply. While it is set, `sot`
   *  equals it: observing any other SOT — rev OR hash — unlocks. */
  invalid: Held | null
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
  | LockedEvent

/** `rev` exists only on `'invalid'`: the SOT rev of the payload that was judged. */
export type LockedEvent =
  | { type: 'locked'; reason: 'conflict' | 'reset' } // the driver acting on lock-conflict / lock-reset
  | { type: 'locked'; reason: 'invalid'; rev: number } // the driver refusing to apply what a pull fetched

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

/** Rebuild a section from what P2b persists device-locally: the agreed `base`,
 *  the hash of the live payload and — if one was open — the `conflict` pair.
 *
 *  Without a conflict, `sot` starts as a copy of `base` — a placeholder that
 *  `indexStale` keeps anyone from acting on.
 *
 *  With one, the section comes back `locked:conflict`, the pair as persisted
 *  and `sot = conflict.sot`. Why it is persisted rather than re-derived (spec
 *  §4.6.2): the local side of a 409 is the snapshot that was SENT. A conflict
 *  re-derived after a restart by the decision table (row 8) would take the
 *  live hash of that moment as its local side instead, and keep-local would
 *  push something other than what the user was shown. The restored lock is an
 *  ordinary one: it keeps learning (`remote-event` / `sot-index` move `sot` and
 *  `conflict.sot`), decides `nothing` (0a precedes the 0d reindex, so the
 *  stale index waits for `resolved`), and both `resolved` directions work.
 *
 *  Flight, `locked:reset`, `locked:invalid`, `forcePull` and `restoreLocal`
 *  are deliberately NOT persisted: after a restart the section reconciles from
 *  scratch. For `locked:invalid` that means reindex → pull → judged again,
 *  which is cheap and cannot leave a verdict on disk that outlives its payload. */
export function restoreSectionState(persisted: { base: Held; currentHash: string | null; conflict?: SectionConflict }): SectionSyncState {
  const { base, currentHash, conflict } = persisted
  const sot = conflict !== undefined ? conflict.sot : base
  return {
    base: { rev: base.rev, hash: base.hash },
    currentHash,
    sot: { rev: sot.rev, hash: sot.hash },
    epoch: 0,
    indexEpoch: 0,
    status: conflict !== undefined ? 'locked:conflict' : currentHash === base.hash ? 'synced' : 'pending',
    inFlight: null,
    sotMovedWhileInFlight: false,
    conflict: conflict !== undefined ? { localHash: conflict.localHash, sot: { rev: sot.rev, hash: sot.hash } } : null,
    invalid: null,
    forcePull: false,
    restoreLocal: null,
    indexStale: true,
  }
}

export function isDirty(s: SectionSyncState): boolean {
  return s.currentHash !== s.base.hash
}

/** Did the SOT move away from what was agreed? A different hash — or, for a
 *  LIVE section, a newer rev. Absent has no rev: absent then and absent now is
 *  "not moved" even if a create and a delete went by in between, because a
 *  create over it goes out with wire baseRev 0 either way. */
export function sotMoved(s: SectionSyncState): boolean {
  return s.sot.hash !== s.base.hash || (s.sot.hash !== null && s.sot.rev > s.base.rev)
}

function isLocked(s: SectionSyncState): boolean {
  return s.status === 'locked:conflict' || s.status === 'locked:reset' || s.status === 'locked:invalid'
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

/** A SOT OBSERVATION (`remote-event`, an accepted `sot-index`): move `sot`, and
 *  open a `locked:invalid` section iff what is now known is not the SOT that
 *  was refused. "Any difference", not "a higher rev": absent carries no rev of
 *  its own (not listed ⇒ `{max(...), null}`, possibly the refused rev), and a
 *  rebuilt profile lowers the rev — either way the unusable payload is no
 *  longer the SOT. The identical `{rev, hash}` keeps it shut, which is what
 *  keeps a poll from re-fetching it for ever. `finish` re-derives the status. */
function observeSot(s: SectionSyncState, sot: Held): SectionSyncState {
  const next = withSot(s, sot)
  if (next.invalid === null || sameHeld(next.sot, next.invalid)) return next
  return { ...next, status: 'pending', invalid: null }
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
    (a.invalid === b.invalid || (a.invalid !== null && b.invalid !== null && sameHeld(a.invalid, b.invalid))) &&
    a.forcePull === b.forcePull &&
    (a.restoreLocal === b.restoreLocal || (a.restoreLocal !== null && b.restoreLocal !== null && a.restoreLocal.hash === b.restoreLocal.hash)) &&
    a.indexStale === b.indexStale &&
    // only `reconnected` moves it inside `step`; on an already-stale section it is the ONLY field that moved
    a.indexEpoch === b.indexEpoch
  )
}

/** The tail of every transition: drop a finished restore, fold convergence
 *  (rule 1), derive synced/pending, bump the epoch — or hand back `prev`. */
function finish(prev: SectionSyncState, draft: SectionSyncState): SectionSyncState {
  if (draft === prev) return prev
  let next = draft
  if (next.restoreLocal !== null && next.restoreLocal.hash === next.currentHash) next = { ...next, restoreLocal: null }
  if (!isLocked(next)) {
    // not on a stale index: `sot` is a belief there, not knowledge (see the header)
    if (next.inFlight === null && !next.indexStale && isDirty(next) && sotMoved(next) && next.sot.hash === next.currentHash) {
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
      // Always a new indexEpoch, already stale or not: every index request sent
      // before the drop is dead — see the header for why. The flight is untouched.
      return { ...s, indexStale: true, indexEpoch: s.indexEpoch + 1 }

    case 'remote-event':
      if (e.own || e.rev <= s.sot.rev) return s
      return observeSot(s, { rev: e.rev, hash: e.hash })

    case 'sot-index': {
      if (e.epoch !== s.indexEpoch) return s.indexStale ? s : { ...s, indexStale: true }
      const sot: Held = e.entry !== null ? { rev: e.entry.rev, hash: e.entry.hash } : { rev: Math.max(s.sot.rev, s.base.rev), hash: null }
      const next = observeSot(s, sot)
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
        // The server says "absent", an event seen during the flight says "live".
        // The two channels are not causally ordered: either the 409 is old and a
        // create came after it (A), or the create was deleted again unseen and
        // the 409 is the truth (B). Guessing A locks on a deleted payload and
        // re-pushes with baseRev 5 against a tombstone; guessing B re-pushes
        // with baseRev 0 against a live section — each loops in the other case.
        // So: believe neither, touch nothing, lock nothing; ask the index. The
        // table decides once it lands (finish() holds the fold back meanwhile).
        const learntLive = s.sotMovedWhileInFlight && s.sot.hash !== null
        if (learntLive) return { ...next, indexStale: true }
        next = withSot(next, { rev: Math.max(s.sot.rev, s.base.rev), hash: null })
      } else if (e.rev > s.sot.rev) next = withSot(next, { rev: e.rev, hash: e.hash })
      // the SOT already holds what we have *now* → rule 1 folds it in finish()
      // (on a stale index — a reconnect during the flight — once the index lands)
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
      if (e.reason === 'invalid') {
        // Only a verdict on the payload this state would pull right now. Decides
        // `pull` ⇒ not locked, no flight, index fresh. `forcePull` goes: locked
        // and forcePull never coexist, and that pull cannot be honoured anyway.
        if (want.do !== 'pull' || e.rev !== s.sot.rev) return s
        return { ...s, status: 'locked:invalid', invalid: { rev: s.sot.rev, hash: s.sot.hash }, forcePull: false }
      }
      if (want.do !== (e.reason === 'conflict' ? 'lock-conflict' : 'lock-reset')) return s
      return e.reason === 'conflict'
        ? { ...s, status: 'locked:conflict', conflict: { localHash: s.currentHash, sot: s.sot } }
        : { ...s, status: 'locked:reset' }
    }

    case 'resolved': {
      if (!isLocked(s)) return s
      // the SOT side of locked:invalid is the payload that cannot be applied
      if (s.status === 'locked:invalid' && e.keep === 'sot') return s
      const unlocked: SectionSyncState = { ...s, status: 'pending', conflict: null, invalid: null }
      if (e.keep === 'sot') return { ...unlocked, forcePull: true }
      // conflict.sot and sot move together; locked:reset / locked:invalid have no pair and read sot
      const target = s.conflict !== null ? s.conflict.sot : s.sot
      return { ...unlocked, base: { rev: target.rev, hash: target.hash }, restoreLocal: s.conflict !== null ? { hash: s.conflict.localHash } : null }
    }
  }
}

export function reduceSection(s: SectionSyncState, e: SectionEvent): SectionSyncState {
  return finish(s, step(s, e))
}
