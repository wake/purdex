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
//     `indexStale` included — bumps `epoch` by exactly one. A FlightToken and
//     an index request carry the epoch they were made at, so both die the
//     moment the state they were made in is gone (§4.6.2 rules 3 and 4).
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
// Driver contract (P2b), stated here because this reducer is what makes it safe:
//   - push / delete: dispatch `push-started` with the token from
//     `decideSection`, then send the request only if the resulting
//     `inFlight === token` (same object). Every outcome — timeouts and
//     malformed responses included — must end in exactly one terminal event.
//   - lock-conflict / lock-reset: dispatch `{type:'locked', reason}`. It is
//     refused if the state no longer calls for that lock.
//   - pull: fetch, then check `canApplyPull` on the *current* state before
//     touching the stores; then dispatch `pull-applied`. For an absent SOT
//     (404) the deletion is applied and reported with `rev = state.sot.rev`.
//   - sot-index: tag the response with the `epoch` read when the request was
//     *sent*.

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
  /** Newest SOT state this client has observed. `rev` decreases only through an epoch-matching index. */
  sot: Held
  /** Bumped by every state change. */
  epoch: number
  status: SectionStatus
  inFlight: FlightToken | null
  sotMovedWhileInFlight: boolean
  conflict: SectionConflict | null
  /** Set by `resolved keep:'sot'`: pull even though the section is dirty. */
  forcePull: boolean
  /** Set by `resolved keep:'local'`: hash of the sent snapshot the driver must put back. */
  restoreLocal: string | null
  /** An index response was discarded; ask again. */
  indexStale: boolean
}

export type SectionEvent =
  | { type: 'local-changed'; hash: string | null }
  | { type: 'sot-index'; epoch: number; entry: { rev: number; hash: string } | null } // null = not listed
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
  | { do: 'reindex' }
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

export function initialSectionState(currentHash: string | null): SectionSyncState {
  return {
    base: { rev: 0, hash: null },
    currentHash,
    sot: { rev: 0, hash: null },
    epoch: 0,
    status: currentHash === null ? 'synced' : 'pending',
    inFlight: null,
    sotMovedWhileInFlight: false,
    conflict: null,
    forcePull: false,
    restoreLocal: null,
    indexStale: false,
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

/** Payloads the driver must keep, by hash: deduplicated, never null. */
export function retainedHashes(s: SectionSyncState): string[] {
  const out: string[] = []
  for (const h of [s.inFlight?.hash, s.conflict?.localHash, s.conflict?.sot.hash, s.restoreLocal]) {
    if (h !== null && h !== undefined && !out.includes(h)) out.push(h)
  }
  return out
}

/** P1 accepts a create — over nothing *or* over a tombstone — only with
 *  `baseRev 0` (sections.go, decision step 2). */
function wireBaseRev(s: SectionSyncState): number {
  return s.sot.hash === null ? 0 : s.base.rev
}

export function decideSection(s: SectionSyncState, ctx: SectionContext): SectionAction {
  const online = ctx.reachable && ctx.autoSync
  /* 0a */ if (isLocked(s)) return NOTHING
  /* 0b */ if (s.inFlight !== null) return NOTHING
  /* 0c */ if (s.indexStale) return online ? { do: 'reindex' } : NOTHING
  /* 0d */ if (s.restoreLocal !== null && s.currentHash !== s.restoreLocal) return { do: 'restore-local', hash: s.restoreLocal }
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
    a.restoreLocal === b.restoreLocal &&
    a.indexStale === b.indexStale
  )
}

/** The tail of every transition: drop a finished restore, fold convergence
 *  (rule 1), derive synced/pending, bump the epoch — or hand back `prev`. */
function finish(prev: SectionSyncState, draft: SectionSyncState): SectionSyncState {
  if (draft === prev) return prev
  let next = draft
  if (next.restoreLocal !== null && next.restoreLocal === next.currentHash) next = { ...next, restoreLocal: null }
  if (!isLocked(next)) {
    if (next.inFlight === null && isDirty(next) && sotMoved(next) && next.sot.hash === next.currentHash) {
      next = { ...next, base: next.sot }
    }
    const status: SectionStatus = isDirty(next) || next.inFlight !== null ? 'pending' : 'synced'
    if (next.status !== status) next = { ...next, status }
  }
  if (unchanged(prev, next)) return prev
  return { ...next, epoch: prev.epoch + 1 }
}

function closeFlight(s: SectionSyncState): SectionSyncState {
  return { ...s, inFlight: null, sotMovedWhileInFlight: false }
}

function step(s: SectionSyncState, e: SectionEvent): SectionSyncState {
  switch (e.type) {
    case 'local-changed':
      return e.hash === s.currentHash ? s : { ...s, currentHash: e.hash }

    case 'remote-event':
      if (e.own || e.rev <= s.sot.rev) return s
      return withSot(s, { rev: e.rev, hash: e.hash })

    case 'sot-index': {
      if (e.epoch !== s.epoch) return s.indexStale ? s : { ...s, indexStale: true }
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
      if (e.rev === 0) next = withSot(next, { rev: Math.max(s.sot.rev, s.base.rev), hash: null })
      else if (e.rev > s.sot.rev) next = withSot(next, { rev: e.rev, hash: e.hash })
      // the SOT already holds what we have *now* → rule 1 folds it in finish()
      if (next.sot.hash === next.currentHash) return next
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
      if (s.restoreLocal === null) return s
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
      return { ...unlocked, base: { rev: target.rev, hash: target.hash }, restoreLocal: s.conflict !== null ? s.conflict.localHash : null }
    }
  }
}

export function reduceSection(s: SectionSyncState, e: SectionEvent): SectionSyncState {
  return finish(s, step(s, e))
}
