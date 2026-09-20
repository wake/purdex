// spa/src/lib/profile/sync-state.test.ts — the per-section sync state machine
// (P2a plan Task 4). Sections: decision table (one test per row), reducer
// rules 1–8, the terminal-event invariant table, the plan-review regression
// sequences (spec §9.4), and seeded property tests.
import { describe, expect, it } from 'vitest'
import {
  canApplyPull,
  canRestoreLocal,
  decideSection,
  initialSectionState,
  isDirty,
  reduceSection,
  restoreSectionState,
  retainedHashes,
  sotMoved,
  type FlightToken,
  type SectionAction,
  type SectionEvent,
  type SectionSyncState,
} from './sync-state'

const ON = { reachable: true, autoSync: true }
const H0 = 'h0'
const H1 = 'h1'
const H2 = 'h2'
const H3 = 'h3'
const H8 = 'h8'

/** Build a state directly (for decision-table rows that the reducer would never leave behind).
 *  Hand-built states stand for "the index has been seen" unless the row says otherwise. */
function mk(p: Partial<SectionSyncState>): SectionSyncState {
  const s = { ...initialSectionState(null), indexStale: false, ...p }
  if (p.status === undefined) s.status = isDirty(s) || s.inFlight !== null ? 'pending' : 'synced'
  return s
}
function synced(rev: number, hash: string | null): SectionSyncState {
  return mk({ base: { rev, hash }, sot: { rev, hash }, currentHash: hash })
}
function run(s: SectionSyncState, ...events: SectionEvent[]): SectionSyncState {
  return events.reduce(reduceSection, s)
}
/** Feed the index response a driver would get for a request sent from this very state. */
function indexed(s: SectionSyncState, entry: { rev: number; hash: string } | null): SectionSyncState {
  const next = reduceSection(s, { type: 'sot-index', epoch: s.indexEpoch, entry })
  expect(next.indexStale).toBe(false)
  return next
}
function tokenOf(a: SectionAction): FlightToken {
  if (a.do !== 'push' && a.do !== 'delete') throw new Error(`expected push/delete, got ${a.do}`)
  return a.token
}
/** decide → push-started, asserting the flight opened with that very token. */
function startFlight(s: SectionSyncState): [SectionSyncState, FlightToken] {
  const token = tokenOf(decideSection(s, ON))
  const next = reduceSection(s, { type: 'push-started', token })
  expect(next.inFlight).toBe(token)
  return [next, token]
}
function deepFreeze<T>(v: T): T {
  if (v !== null && typeof v === 'object' && !Object.isFrozen(v)) {
    Object.freeze(v)
    for (const k of Object.keys(v)) deepFreeze((v as Record<string, unknown>)[k])
  }
  return v
}

describe('initialSectionState / predicates', () => {
  it('starts with nothing agreed and nothing observed', () => {
    expect(initialSectionState(null)).toEqual({
      base: { rev: 0, hash: null },
      currentHash: null,
      sot: { rev: 0, hash: null },
      epoch: 0,
      indexEpoch: 0,
      status: 'synced',
      inFlight: null,
      sotMovedWhileInFlight: false,
      conflict: null,
      invalid: null,
      forcePull: false,
      restoreLocal: null,
      indexStale: true,
    })
  })
  it('the first decision is reindex — nothing is written or read before the SOT has been asked', () => {
    for (const h of [null, H1]) {
      const s = initialSectionState(h)
      expect(decideSection(s, ON)).toMatchObject({ do: 'reindex' })
      expect(decideSection(s, { reachable: false, autoSync: true })).toEqual({ do: 'nothing' })
      expect(decideSection(s, { reachable: true, autoSync: false })).toEqual({ do: 'nothing' })
    }
    expect(initialSectionState(H1).status).toBe('pending')
  })
  it('once indexed, a fresh dirty section creates with baseRev 0; a fresh clean one pulls what is there', () => {
    expect(tokenOf(decideSection(indexed(initialSectionState(H1), null), ON))).toMatchObject({ kind: 'put', hash: H1, baseRev: 0 })
    expect(decideSection(indexed(initialSectionState(null), { rev: 3, hash: H2 }), ON)).toEqual({ do: 'pull' })
    expect(decideSection(indexed(initialSectionState(null), null), ON)).toEqual({ do: 'nothing' })
  })
  it('restoreSectionState: persisted base + live hash, sot assumed at base, everything else initial, index stale', () => {
    const base = deepFreeze({ rev: 5, hash: H0 })
    const s = restoreSectionState({ base, currentHash: H1 })
    expect(s).toEqual({
      base: { rev: 5, hash: H0 },
      currentHash: H1,
      sot: { rev: 5, hash: H0 },
      epoch: 0,
      indexEpoch: 0,
      status: 'pending',
      inFlight: null,
      sotMovedWhileInFlight: false,
      conflict: null,
      invalid: null,
      forcePull: false,
      restoreLocal: null,
      indexStale: true,
    })
    expect(s.base).not.toBe(base)
    expect(s.sot).not.toBe(s.base)
    expect(restoreSectionState({ base, currentHash: H0 }).status).toBe('synced')
    expect(decideSection(s, ON)).toMatchObject({ do: 'reindex' })
  })
  it('restoreSectionState: an unresolved conflict is re-derived by the table after the reindex', () => {
    const s = indexed(restoreSectionState({ base: { rev: 5, hash: H0 }, currentHash: H1 }), { rev: 6, hash: H2 })
    expect(decideSection(s, ON)).toEqual({ do: 'lock-conflict' })
    const t = indexed(restoreSectionState({ base: { rev: 5, hash: H0 }, currentHash: H1 }), { rev: 5, hash: H0 })
    expect(tokenOf(decideSection(t, ON))).toMatchObject({ kind: 'put', hash: H1, baseRev: 5 })
  })
  it('a local payload with no base is dirty → pending', () => {
    const s = initialSectionState(H1)
    expect(isDirty(s)).toBe(true)
    expect(s.status).toBe('pending')
  })
  it('sotMoved compares hash and rev', () => {
    expect(sotMoved(synced(5, H0))).toBe(false)
    expect(sotMoved(mk({ base: { rev: 5, hash: H0 }, sot: { rev: 6, hash: H0 }, currentHash: H0 }))).toBe(true)
    expect(sotMoved(mk({ base: { rev: 5, hash: H0 }, sot: { rev: 5, hash: null }, currentHash: H0 }))).toBe(true)
    // a lower rev with the same hash is not "moved" — it is row 1 (reset)
    expect(sotMoved(mk({ base: { rev: 5, hash: H0 }, sot: { rev: 2, hash: H0 }, currentHash: H0 }))).toBe(false)
  })
  it('sotMoved: absent has no rev — absent then and absent now is "not moved", whatever happened in between', () => {
    expect(sotMoved(mk({ base: { rev: 4, hash: null }, sot: { rev: 6, hash: null }, currentHash: null }))).toBe(false)
    // so a local create over it is a plain push (wire baseRev 0), not a conflict with nothing
    const d = decideSection(mk({ base: { rev: 4, hash: null }, sot: { rev: 6, hash: null }, currentHash: H1 }), ON)
    expect(tokenOf(d)).toMatchObject({ kind: 'put', hash: H1, baseRev: 0 })
    expect(decideSection(mk({ base: { rev: 4, hash: null }, sot: { rev: 6, hash: null }, currentHash: null }), ON)).toEqual({ do: 'nothing' })
  })
})

describe('decideSection — one test per row', () => {
  const dirtyPushable = mk({ base: { rev: 5, hash: H0 }, sot: { rev: 5, hash: H0 }, currentHash: H1 })

  it('0a: locked:conflict → nothing', () => {
    const s = mk({ ...dirtyPushable, status: 'locked:conflict', conflict: { localHash: H1, sot: { rev: 6, hash: H2 } } })
    expect(decideSection(s, ON)).toEqual({ do: 'nothing' })
  })
  it('0a: locked:reset → nothing', () => {
    expect(decideSection(mk({ ...dirtyPushable, status: 'locked:reset' }), ON)).toEqual({ do: 'nothing' })
  })
  it('0b: a flight is open → nothing', () => {
    const s = mk({ ...dirtyPushable, inFlight: { kind: 'put', hash: H1, baseRev: 5, epoch: 0 } })
    expect(decideSection(s, ON)).toEqual({ do: 'nothing' })
  })
  it('0c: restoreLocal differs from currentHash → restore-local', () => {
    const s = mk({ ...dirtyPushable, restoreLocal: { hash: H2 } })
    expect(decideSection(s, ON)).toEqual({ do: 'restore-local', hash: H2 })
  })
  it('0c is a local action: not gated by reachable/autoSync', () => {
    const s = mk({ ...dirtyPushable, restoreLocal: { hash: H2 } })
    expect(decideSection(s, { reachable: false, autoSync: false })).toEqual({ do: 'restore-local', hash: H2 })
  })
  it('0c precedes 0d: a pending restore is not held back by a stale index, online or off', () => {
    const s = mk({ ...dirtyPushable, restoreLocal: { hash: H2 }, indexStale: true })
    expect(decideSection(s, { reachable: false, autoSync: false })).toEqual({ do: 'restore-local', hash: H2 })
    expect(decideSection(s, ON)).toEqual({ do: 'restore-local', hash: H2 })
    const restored = reduceSection(s, { type: 'local-restored', hash: H2 })
    expect(restored.indexStale).toBe(true)
    expect(decideSection(restored, { reachable: false, autoSync: false })).toEqual({ do: 'nothing' })
    expect(decideSection(restored, ON)).toMatchObject({ do: 'reindex' })
  })
  it('0a / 0b still precede 0c: no restore while locked or in flight', () => {
    const stale = { ...dirtyPushable, restoreLocal: { hash: H2 }, indexStale: true }
    expect(decideSection(mk({ ...stale, status: 'locked:reset' }), ON)).toEqual({ do: 'nothing' })
    expect(decideSection(mk({ ...stale, inFlight: { kind: 'put', hash: H1, baseRev: 5, epoch: 0 } }), ON)).toEqual({ do: 'nothing' })
  })
  it('0d: indexStale → reindex, ahead of every sync row', () => {
    expect(decideSection(mk({ ...dirtyPushable, indexStale: true }), ON)).toMatchObject({ do: 'reindex' })
  })
  it('0e: forcePull → pull even though dirty', () => {
    expect(decideSection(mk({ ...dirtyPushable, forcePull: true }), ON)).toEqual({ do: 'pull' })
  })
  it('1: sot.rev < base.rev → lock-reset (clean or dirty)', () => {
    expect(decideSection(mk({ base: { rev: 5, hash: H0 }, sot: { rev: 2, hash: H0 }, currentHash: H0 }), ON)).toEqual({ do: 'lock-reset' })
    expect(decideSection(mk({ base: { rev: 5, hash: H0 }, sot: { rev: 2, hash: H8 }, currentHash: H1 }), ON)).toEqual({ do: 'lock-reset' })
  })
  it('2: clean, SOT not moved → nothing', () => {
    expect(decideSection(synced(5, H0), ON)).toEqual({ do: 'nothing' })
  })
  it('3: clean, SOT moved → pull', () => {
    expect(decideSection(mk({ base: { rev: 5, hash: H0 }, sot: { rev: 6, hash: H1 }, currentHash: H0 }), ON)).toEqual({ do: 'pull' })
  })
  it('3: clean, SOT moved to absent → pull (applies a deletion)', () => {
    expect(decideSection(mk({ base: { rev: 5, hash: H0 }, sot: { rev: 6, hash: null }, currentHash: H0 }), ON)).toEqual({ do: 'pull' })
  })
  it('4: dirty, SOT not moved, live payload → push with a token for this exact state', () => {
    const s = mk({ ...dirtyPushable, epoch: 17 })
    expect(decideSection(s, ON)).toEqual({ do: 'push', token: { kind: 'put', hash: H1, baseRev: 5, epoch: 17 } })
  })
  it('5: dirty, SOT not moved, deleted locally, SOT live → delete', () => {
    const s = mk({ base: { rev: 5, hash: H0 }, sot: { rev: 5, hash: H0 }, currentHash: null, epoch: 3 })
    expect(decideSection(s, ON)).toEqual({ do: 'delete', token: { kind: 'delete', hash: null, baseRev: 5, epoch: 3 } })
  })
  it('6: both-null is clean, so the "dirty, both null" row is unreachable', () => {
    const s = mk({ base: { rev: 6, hash: null }, sot: { rev: 6, hash: null }, currentHash: null })
    expect(isDirty(s)).toBe(false)
    expect(s.status).toBe('synced')
    expect(decideSection(s, ON)).toEqual({ do: 'nothing' })
  })
  it('7: dirty, SOT moved to exactly our hash → nothing (the reducer folds it)', () => {
    const s = mk({ base: { rev: 5, hash: H0 }, sot: { rev: 6, hash: H1 }, currentHash: H1 })
    expect(decideSection(s, ON)).toEqual({ do: 'nothing' })
  })
  it('8: dirty, SOT moved elsewhere → lock-conflict', () => {
    const s = mk({ base: { rev: 5, hash: H0 }, sot: { rev: 6, hash: H2 }, currentHash: H1 })
    expect(decideSection(s, ON)).toEqual({ do: 'lock-conflict' })
  })
  it('8: not gated — an unreachable host does not hide a known conflict', () => {
    const s = mk({ base: { rev: 5, hash: H0 }, sot: { rev: 6, hash: H2 }, currentHash: H1 })
    expect(decideSection(s, { reachable: false, autoSync: false })).toEqual({ do: 'lock-conflict' })
  })

  describe('!reachable || !autoSync turns 0d / 0e / 3 / 4 / 5 into nothing', () => {
    const rows: [string, SectionSyncState][] = [
      ['0d', mk({ ...dirtyPushable, indexStale: true })],
      ['0e', mk({ ...dirtyPushable, forcePull: true })],
      ['3', mk({ base: { rev: 5, hash: H0 }, sot: { rev: 6, hash: H1 }, currentHash: H0 })],
      ['4', dirtyPushable],
      ['5', mk({ base: { rev: 5, hash: H0 }, sot: { rev: 5, hash: H0 }, currentHash: null })],
    ]
    for (const [row, s] of rows) {
      it(`row ${row}: unreachable → nothing`, () => {
        expect(decideSection(s, { reachable: false, autoSync: true })).toEqual({ do: 'nothing' })
      })
      it(`row ${row}: autoSync off → nothing`, () => {
        expect(decideSection(s, { reachable: true, autoSync: false })).toEqual({ do: 'nothing' })
      })
    }
    it('a dirty section that may not flush reads pending', () => {
      const s = run(synced(5, H0), { type: 'local-changed', hash: H1 })
      expect(decideSection(s, { reachable: false, autoSync: true })).toEqual({ do: 'nothing' })
      expect(s.status).toBe('pending')
    })
    it('0d first-match-wins: a stale index that cannot be refreshed does not fall through to push', () => {
      const s = mk({ ...dirtyPushable, indexStale: true })
      expect(decideSection(s, { reachable: false, autoSync: true })).toEqual({ do: 'nothing' })
    })
  })
})

describe('wire baseRev', () => {
  it('create over nothing sends baseRev 0', () => {
    const s = indexed(initialSectionState(H1), null)
    expect(tokenOf(decideSection(s, ON))).toEqual({ kind: 'put', hash: H1, baseRev: 0, epoch: s.epoch })
  })
  it('create over a tombstone sends baseRev 0, not the tombstone rev', () => {
    const s = mk({ base: { rev: 6, hash: null }, sot: { rev: 6, hash: null }, currentHash: H1 })
    expect(tokenOf(decideSection(s, ON)).baseRev).toBe(0)
  })
  it('update of a live section sends base.rev', () => {
    const s = mk({ base: { rev: 5, hash: H0 }, sot: { rev: 5, hash: H0 }, currentHash: H1 })
    expect(tokenOf(decideSection(s, ON)).baseRev).toBe(5)
  })
})

describe('reduceSection — epoch', () => {
  it('local-changed bumps the epoch; a no-op returns the same reference', () => {
    const s = synced(5, H0)
    const s1 = reduceSection(s, { type: 'local-changed', hash: H1 })
    expect(s1.epoch).toBe(s.epoch + 1)
    expect(s1.currentHash).toBe(H1)
    expect(reduceSection(s1, { type: 'local-changed', hash: H1 })).toBe(s1)
  })
  it('a push token is dead once currentHash moved (epoch AND hash are checked)', () => {
    const s = run(synced(5, H0), { type: 'local-changed', hash: H1 })
    const token = tokenOf(decideSection(s, ON))
    const s2 = reduceSection(s, { type: 'local-changed', hash: H2 })
    expect(reduceSection(s2, { type: 'push-started', token })).toBe(s2)
    // even a token forged with the right epoch is refused when its hash is not the live one
    expect(reduceSection(s2, { type: 'push-started', token: { ...token, epoch: s2.epoch } })).toBe(s2)
  })
  it('every state-changing event bumps by exactly one', () => {
    let s = synced(5, H0)
    const events: SectionEvent[] = [
      { type: 'remote-event', rev: 6, hash: H1, own: false },
      { type: 'pull-applied', rev: 6, hash: H1, localHash: H1 },
      { type: 'local-changed', hash: H2 },
    ]
    for (const e of events) {
      const next = reduceSection(s, e)
      expect(next.epoch).toBe(s.epoch + 1)
      s = next
    }
  })
})

describe('reduceSection — rule 1: convergence is folded in the reducer', () => {
  it('dirty + SOT moved to our hash → base = sot, synced', () => {
    const s = run(synced(5, H0), { type: 'local-changed', hash: H2 }, { type: 'remote-event', rev: 6, hash: H2, own: false })
    expect(s.base).toEqual({ rev: 6, hash: H2 })
    expect(s.status).toBe('synced')
  })
  it('folds when the local edit arrives after the remote one, too', () => {
    const s = run(synced(5, H0), { type: 'remote-event', rev: 6, hash: H2, own: false }, { type: 'local-changed', hash: H2 })
    expect(s.base).toEqual({ rev: 6, hash: H2 })
    expect(s.status).toBe('synced')
  })
  it('does not fold while a flight is open — waits for the terminal event', () => {
    const [s] = startFlight(run(synced(5, H0), { type: 'local-changed', hash: H1 }))
    const s2 = reduceSection(s, { type: 'remote-event', rev: 6, hash: H1, own: false })
    expect(s2.base).toEqual({ rev: 5, hash: H0 })
    expect(s2.inFlight).not.toBeNull()
    const s3 = reduceSection(s2, { type: 'push-failed' })
    expect(s3.base).toEqual({ rev: 6, hash: H1 })
    expect(s3.status).toBe('synced')
  })
  it('does not fold on a stale index — "synced" is not declared on a view of the SOT that is not trusted; the index landing folds', () => {
    // restored (so: stale), dirty H2; an event says the SOT moved to H2 as well
    const s = run(restoreSectionState({ base: { rev: 5, hash: H0 }, currentHash: H2 }), { type: 'remote-event', rev: 6, hash: H2, own: false })
    expect(s.indexStale).toBe(true)
    expect(s.sot).toEqual({ rev: 6, hash: H2 })
    expect(s.base).toEqual({ rev: 5, hash: H0 })
    expect(s.status).toBe('pending')
    expect(decideSection(s, ON)).toEqual({ do: 'reindex', indexEpoch: s.indexEpoch })
    const ok = indexed(s, { rev: 6, hash: H2 })
    expect(ok.base).toEqual({ rev: 6, hash: H2 })
    expect(ok.status).toBe('synced')
    // had the event been overtaken (H2 replaced by H8 unseen), the fold would have been wrong
    const not = indexed(s, { rev: 7, hash: H8 })
    expect(not.base).toEqual({ rev: 5, hash: H0 })
    expect(decideSection(not, ON)).toEqual({ do: 'lock-conflict' })
  })
  it('a reconnect in the middle of agreement: the fold waits for the index too', () => {
    const s = run(synced(5, H0), { type: 'reconnected' }, { type: 'local-changed', hash: H2 }, { type: 'remote-event', rev: 6, hash: H2, own: false })
    expect(s.base).toEqual({ rev: 5, hash: H0 })
    expect(indexed(s, { rev: 6, hash: H2 }).status).toBe('synced')
  })
  it('does not fold while locked', () => {
    const [s] = startFlight(run(synced(5, H0), { type: 'local-changed', hash: H1 }))
    const locked = run(s, { type: 'push-conflict', rev: 6, hash: H2 }, { type: 'local-changed', hash: H2 })
    expect(locked.status).toBe('locked:conflict')
    expect(locked.base).toEqual({ rev: 5, hash: H0 })
  })
})

describe('reduceSection — rule 2: remote-event', () => {
  it('own:true is ignored entirely', () => {
    const s = synced(5, H0)
    expect(reduceSection(s, { type: 'remote-event', rev: 6, hash: H1, own: true })).toBe(s)
  })
  it('advances sot only when rev > sot.rev', () => {
    const s = synced(5, H0)
    expect(reduceSection(s, { type: 'remote-event', rev: 5, hash: H1, own: false })).toBe(s)
    expect(reduceSection(s, { type: 'remote-event', rev: 4, hash: H1, own: false })).toBe(s)
    expect(reduceSection(s, { type: 'remote-event', rev: 6, hash: H1, own: false }).sot).toEqual({ rev: 6, hash: H1 })
  })
  it('on a dirty section nothing is applied; the next decision is row 8', () => {
    const s = run(synced(5, H0), { type: 'local-changed', hash: H1 }, { type: 'remote-event', rev: 6, hash: H2, own: false })
    expect(s.currentHash).toBe(H1)
    expect(s.base).toEqual({ rev: 5, hash: H0 })
    expect(decideSection(s, ON)).toEqual({ do: 'lock-conflict' })
  })
  it('a delete event (hash null) is recorded as an absent SOT', () => {
    const s = reduceSection(synced(5, H0), { type: 'remote-event', rev: 6, hash: null, own: false })
    expect(s.sot).toEqual({ rev: 6, hash: null })
    expect(decideSection(s, ON)).toEqual({ do: 'pull' })
  })
  it('while in flight it sets sotMovedWhileInFlight', () => {
    const [s] = startFlight(run(synced(5, H0), { type: 'local-changed', hash: H1 }))
    const s2 = reduceSection(s, { type: 'remote-event', rev: 6, hash: H2, own: false })
    expect(s2.sotMovedWhileInFlight).toBe(true)
    expect(s2.sot).toEqual({ rev: 6, hash: H2 })
  })
  it('while locked:conflict it advances sot AND conflict.sot', () => {
    const [s] = startFlight(run(synced(5, H0), { type: 'local-changed', hash: H1 }))
    const s2 = run(s, { type: 'push-conflict', rev: 6, hash: H2 }, { type: 'remote-event', rev: 7, hash: H8, own: false })
    expect(s2.status).toBe('locked:conflict')
    expect(s2.sot).toEqual({ rev: 7, hash: H8 })
    expect(s2.conflict).toEqual({ localHash: H1, sot: { rev: 7, hash: H8 } })
  })
})

describe('reduceSection — rule 3: sot-index', () => {
  it('epoch mismatch → discarded, indexStale', () => {
    const s = mk({ ...synced(5, H0), indexEpoch: 4 })
    const s2 = reduceSection(s, { type: 'sot-index', epoch: 3, entry: { rev: 9, hash: H8 } })
    expect(s2.sot).toEqual({ rev: 5, hash: H0 })
    expect(s2.indexStale).toBe(true)
    // a second stale response changes nothing → same reference
    expect(reduceSection(s2, { type: 'sot-index', epoch: 3, entry: null })).toBe(s2)
  })
  it('matching epoch is authoritative, even when it lowers sot.rev (the only road to lock-reset)', () => {
    const s = synced(5, H0)
    const s2 = reduceSection(s, { type: 'sot-index', epoch: s.indexEpoch, entry: { rev: 2, hash: H8 } })
    expect(s2.sot).toEqual({ rev: 2, hash: H8 })
    expect(decideSection(s2, ON)).toEqual({ do: 'lock-reset' })
  })
  it('matching epoch, not listed → sot = {max(sot.rev, base.rev), null}', () => {
    const s = mk({ base: { rev: 5, hash: H0 }, sot: { rev: 7, hash: H1 }, currentHash: H0 })
    expect(reduceSection(s, { type: 'sot-index', epoch: s.indexEpoch, entry: null }).sot).toEqual({ rev: 7, hash: null })
    const t = mk({ base: { rev: 5, hash: H0 }, sot: { rev: 2, hash: H1 }, currentHash: H0 })
    expect(reduceSection(t, { type: 'sot-index', epoch: t.indexEpoch, entry: null }).sot).toEqual({ rev: 5, hash: null })
  })
  it('matching epoch clears indexStale', () => {
    const s = mk({ ...synced(5, H0), indexStale: true })
    const s2 = reduceSection(s, { type: 'sot-index', epoch: s.indexEpoch, entry: { rev: 5, hash: H0 } })
    expect(s2.indexStale).toBe(false)
    expect(s2.epoch).toBe(s.epoch + 1)
  })
  it('an index that confirms what we know returns the same reference', () => {
    const s = synced(5, H0)
    expect(reduceSection(s, { type: 'sot-index', epoch: s.indexEpoch, entry: { rev: 5, hash: H0 } })).toBe(s)
  })
  describe('indexEpoch — freshness of the index is about the SOT, not about local edits (attack finding: starvation)', () => {
    it('reindex carries indexEpoch; the driver echoes it in sot-index', () => {
      const s = mk({ ...synced(5, H0), indexStale: true, epoch: 40, indexEpoch: 10 })
      expect(decideSection(s, ON)).toEqual({ do: 'reindex', indexEpoch: 10 })
    })

    it('the user keeps typing while the index is out: the FIRST response is accepted, and the next decision is the push', () => {
      let s = mk({ ...synced(5, H0), indexStale: true, epoch: 10, indexEpoch: 10 })
      const d = decideSection(s, ON)
      if (d.do !== 'reindex') throw new Error('expected reindex')
      s = run(s, { type: 'local-changed', hash: H1 }, { type: 'local-changed', hash: H2 })
      expect(s.epoch).toBe(12) // the flight-token epoch still moves with every edit
      expect(s.indexEpoch).toBe(10)
      expect(decideSection(s, ON)).toEqual({ do: 'reindex', indexEpoch: 10 }) // still shadowing the push
      s = reduceSection(s, { type: 'sot-index', epoch: d.indexEpoch, entry: { rev: 5, hash: H0 } })
      expect(s.indexStale).toBe(false)
      s = reduceSection(s, { type: 'local-changed', hash: H3 })
      expect(s.indexStale).toBe(false)
      expect(tokenOf(decideSection(s, ON))).toMatchObject({ kind: 'put', hash: H3, baseRev: 5, epoch: s.epoch })
    })

    it('many rounds of typing interleaved with index responses never discard one', () => {
      let s = mk({ ...synced(5, H0), indexStale: true })
      for (let round = 0; round < 5; round++) {
        s = reduceSection(s, { type: 'reconnected' }) // stale again; every reconnect opens a new indexEpoch, so `d` is read after it
        const d = decideSection(s, ON)
        if (d.do !== 'reindex') throw new Error('expected reindex')
        for (let k = 0; k <= round; k++) s = reduceSection(s, { type: 'local-changed', hash: `typed-${round}-${k}` })
        s = reduceSection(s, { type: 'sot-index', epoch: d.indexEpoch, entry: { rev: 5, hash: H0 } })
        expect(s.indexStale).toBe(false)
        expect(decideSection(s, ON).do).toBe('push')
      }
    })

    it('bounded progress (seeded): with no base / sot / flight change, the first response made at the current indexEpoch is accepted', () => {
      const pool: (string | null)[] = [H0, H1, H2, H3, H8, null]
      for (let seed = 1; seed <= 300; seed++) {
        const rnd = mulberry32(seed)
        const pick = <T,>(xs: readonly T[]): T => xs[Math.floor(rnd() * xs.length)]
        const sotHash = pick([H0, H8])
        let s = mk({ base: { rev: 5, hash: H0 }, sot: { rev: sotHash === H0 ? 5 : 6, hash: sotHash }, currentHash: pick(pool), indexStale: true, restoreLocal: rnd() < 0.2 ? { hash: H3 } : null })
        const before = s
        const requestedAt = s.indexEpoch
        const edits = Math.floor(rnd() * 12)
        // never the hash that would fold into the SOT: that moves the base, which is outside this property
        for (let k = 0; k < edits; k++) s = reduceSection(s, { type: 'local-changed', hash: pick(pool.filter((h) => h !== sotHash)) })
        expect(s.base).toEqual(before.base)
        expect(s.indexEpoch, `seed=${seed}`).toBe(requestedAt)
        const entry = rnd() < 0.2 ? null : { rev: 5 + Math.floor(rnd() * 4), hash: pick([H0, H1, H8]) }
        s = reduceSection(s, { type: 'sot-index', epoch: requestedAt, entry })
        expect(s.indexStale, `seed=${seed}`).toBe(false)
        expect(s.sot).toEqual(entry ?? { rev: Math.max(before.sot.rev, before.base.rev), hash: null })
      }
    })

    it('what does move indexEpoch: a remote event, a flight opening and closing, a pull — and a reconnect', () => {
      const s = run(synced(5, H0), { type: 'local-changed', hash: H1 })
      expect(s.indexEpoch).toBe(0)
      expect(reduceSection(s, { type: 'remote-event', rev: 6, hash: H8, own: false }).indexEpoch).toBe(1)
      const [f] = startFlight(s)
      expect(f.indexEpoch).toBe(1)
      expect(reduceSection(f, { type: 'push-failed' }).indexEpoch).toBe(2)
      expect(reduceSection(f, { type: 'push-applied', rev: 6 }).indexEpoch).toBe(2)
      const moved = reduceSection(synced(5, H0), { type: 'remote-event', rev: 6, hash: H8, own: false })
      expect(reduceSection(moved, { type: 'pull-applied', rev: 6, hash: H8, localHash: H8 }).indexEpoch).toBe(moved.indexEpoch + 1)
      expect(reduceSection(s, { type: 'reconnected' }).indexEpoch).toBe(1)
    })

    it('what does not: locked, resolved keep:sot, local-restored, a discarded index', () => {
      const conflicted = run(synced(5, H0), { type: 'local-changed', hash: H1 }, { type: 'remote-event', rev: 6, hash: H8, own: false })
      const locked = reduceSection(conflicted, { type: 'locked', reason: 'conflict' })
      expect(locked.status).toBe('locked:conflict')
      expect(locked.indexEpoch).toBe(conflicted.indexEpoch)
      const takeSot = reduceSection(locked, { type: 'resolved', keep: 'sot' })
      expect(takeSot.forcePull).toBe(true)
      expect(takeSot.indexEpoch).toBe(locked.indexEpoch)
      const restoring = mk({ ...synced(5, H0), currentHash: H2, restoreLocal: { hash: H1 } })
      const restored = reduceSection(restoring, { type: 'local-restored', hash: H1 })
      expect(restored.currentHash).toBe(H1)
      expect(restored.indexEpoch).toBe(restoring.indexEpoch)
      const discarded = reduceSection(mk({ ...synced(5, H0), indexEpoch: 4 }), { type: 'sot-index', epoch: 3, entry: null })
      expect(discarded.indexStale).toBe(true)
      expect(discarded.indexEpoch).toBe(4)
    })
  })

  it('while locked:conflict it keeps conflict.sot in step', () => {
    const [s] = startFlight(run(synced(5, H0), { type: 'local-changed', hash: H1 }))
    const locked = reduceSection(s, { type: 'push-conflict', rev: 6, hash: H2 })
    const s2 = reduceSection(locked, { type: 'sot-index', epoch: locked.indexEpoch, entry: { rev: 9, hash: H8 } })
    expect(s2.conflict?.sot).toEqual({ rev: 9, hash: H8 })
  })
})

describe('reduceSection — reconnected (reconcile on connect, spec §4.6)', () => {
  it('marks the index stale, bumps the epoch; the next decision is reindex, whatever the table would say', () => {
    const rows: SectionSyncState[] = [
      synced(5, H0),
      run(synced(5, H0), { type: 'local-changed', hash: H1 }), // would push
      run(synced(5, H0), { type: 'remote-event', rev: 6, hash: H1, own: false }), // would pull
      run(synced(5, H0), { type: 'local-changed', hash: H1 }, { type: 'remote-event', rev: 6, hash: H2, own: false }), // would lock
    ]
    for (const s of rows) {
      const r = reduceSection(deepFreeze(s), { type: 'reconnected' })
      expect(r).toEqual({ ...s, indexStale: true, epoch: s.epoch + 1, indexEpoch: s.indexEpoch + 1 })
      expect(decideSection(r, ON)).toEqual({ do: 'reindex', indexEpoch: r.indexEpoch })
      expect(indexed(r, r.sot.hash === null ? null : { rev: r.sot.rev, hash: r.sot.hash })).toEqual({ ...s, epoch: s.epoch + 2, indexEpoch: s.indexEpoch + 1 })
    }
  })
  it('already stale → still a new state: indexEpoch + 1, the index stays stale', () => {
    const s = reduceSection(synced(5, H0), { type: 'reconnected' })
    expect(reduceSection(deepFreeze(s), { type: 'reconnected' })).toEqual({ ...s, epoch: s.epoch + 1, indexEpoch: s.indexEpoch + 1 })
    const fresh = initialSectionState(H1)
    const r = reduceSection(deepFreeze(fresh), { type: 'reconnected' })
    expect(r).not.toBe(fresh)
    expect(r).toEqual({ ...fresh, epoch: fresh.epoch + 1, indexEpoch: fresh.indexEpoch + 1 })
    expect(r.indexStale).toBe(true)
  })
  it('regression: the FIRST index request, outstanding across a disconnect, is dead too — the section was already stale', () => {
    let s = initialSectionState(H1)
    const before = s
    const d0 = decideSection(s, ON)
    if (d0.do !== 'reindex') throw new Error('expected reindex')
    expect(d0.indexEpoch).toBe(s.indexEpoch)
    s = reduceSection(s, { type: 'reconnected' }) // the connection dropped and came back while that request was out
    s = reduceSection(s, { type: 'sot-index', epoch: d0.indexEpoch, entry: { rev: 9, hash: H8 } }) // the pre-drop answer, late
    expect(s.indexStale).toBe(true)
    expect(s.sot).toEqual(before.sot)
    const d1 = decideSection(s, ON)
    if (d1.do !== 'reindex') throw new Error('expected reindex')
    expect(d1.indexEpoch).not.toBe(d0.indexEpoch)
    s = reduceSection(s, { type: 'sot-index', epoch: d1.indexEpoch, entry: { rev: 9, hash: H8 } })
    expect(s.indexStale).toBe(false)
    expect(s.sot).toEqual({ rev: 9, hash: H8 })
  })
  it('does not touch an open flight — its terminal event still has to come from the driver', () => {
    const [f, token] = startFlight(run(synced(5, H0), { type: 'local-changed', hash: H1 }))
    const r = reduceSection(f, { type: 'reconnected' })
    expect(r.inFlight).toBe(token)
    expect(r.indexStale).toBe(true)
    expect(decideSection(r, ON)).toEqual({ do: 'nothing' })
    const done = reduceSection(r, { type: 'push-failed' })
    expect(done.inFlight).toBeNull()
    expect(decideSection(done, ON)).toMatchObject({ do: 'reindex' })
  })
  it('an index request sent before the reconnect is dead (its epoch is gone)', () => {
    const s = synced(5, H0)
    const r = run(s, { type: 'reconnected' }, { type: 'sot-index', epoch: s.indexEpoch, entry: { rev: 9, hash: H8 } })
    expect(r.sot).toEqual({ rev: 5, hash: H0 })
    expect(r.indexStale).toBe(true)
  })
  it('while locked it is recorded, and the unlock reindexes before anything else', () => {
    const [f] = startFlight(run(synced(5, H0), { type: 'local-changed', hash: H1 }))
    const l = run(f, { type: 'push-conflict', rev: 6, hash: H2 }, { type: 'reconnected' })
    expect(l.status).toBe('locked:conflict')
    expect(decideSection(l, ON)).toEqual({ do: 'nothing' })
    expect(decideSection(reduceSection(l, { type: 'resolved', keep: 'sot' }), ON)).toMatchObject({ do: 'reindex' })
  })
})

describe('reduceSection — rule 4: push-started', () => {
  const dirty = () => run(synced(5, H0), { type: 'local-changed', hash: H1 })
  it('accepts the token decideSection just produced; stores that very object; bumps the epoch', () => {
    const s = dirty()
    const token = tokenOf(decideSection(s, ON))
    const s2 = reduceSection(s, { type: 'push-started', token })
    expect(s2.inFlight).toBe(token)
    expect(s2.epoch).toBe(s.epoch + 1)
    expect(s2.status).toBe('pending')
  })
  it('rejects a token from another epoch', () => {
    const s = dirty()
    const token = { ...tokenOf(decideSection(s, ON)), epoch: s.epoch - 1 }
    expect(reduceSection(s, { type: 'push-started', token })).toBe(s)
  })
  it('rejects a second flight', () => {
    const [s, token] = startFlight(dirty())
    expect(reduceSection(s, { type: 'push-started', token: { ...token, epoch: s.epoch } })).toBe(s)
  })
  it('rejects a token with the wrong baseRev or kind even at the right epoch', () => {
    const s = dirty()
    const token = tokenOf(decideSection(s, ON))
    expect(reduceSection(s, { type: 'push-started', token: { ...token, baseRev: 4 } })).toBe(s)
    expect(reduceSection(s, { type: 'push-started', token: { ...token, kind: 'delete' } })).toBe(s)
  })
  it('rejects any flight on a locked section', () => {
    const [s] = startFlight(dirty())
    const locked = reduceSection(s, { type: 'push-conflict', rev: 6, hash: H2 })
    const forged: FlightToken = { kind: 'put', hash: H1, baseRev: 5, epoch: locked.epoch }
    expect(reduceSection(locked, { type: 'push-started', token: forged })).toBe(locked)
  })
})

describe('reduceSection — rule 5: terminal events', () => {
  const inFlightPut = () => startFlight(run(synced(5, H0), { type: 'local-changed', hash: H1 }))[0]

  it('push-applied → base = {rev, sent hash}, sot follows', () => {
    const s = reduceSection(inFlightPut(), { type: 'push-applied', rev: 6 })
    expect(s.base).toEqual({ rev: 6, hash: H1 })
    expect(s.sot).toEqual({ rev: 6, hash: H1 })
    expect(s.status).toBe('synced')
  })
  it('push-applied does not rewind a sot that already moved past it', () => {
    const s = run(inFlightPut(), { type: 'remote-event', rev: 7, hash: H8, own: false }, { type: 'push-applied', rev: 6 })
    expect(s.base).toEqual({ rev: 6, hash: H1 })
    expect(s.sot).toEqual({ rev: 7, hash: H8 })
    expect(decideSection(s, ON)).toEqual({ do: 'pull' })
  })
  it('push-applied with currentHash moved during the flight → dirty against the new base', () => {
    const s = run(inFlightPut(), { type: 'local-changed', hash: H2 }, { type: 'push-applied', rev: 6 })
    expect(s.base).toEqual({ rev: 6, hash: H1 })
    expect(s.status).toBe('pending')
    expect(tokenOf(decideSection(s, ON))).toMatchObject({ hash: H2, baseRev: 6 })
  })
  it('push-applied for a delete → base {rev, null}', () => {
    const [s, token] = startFlight(run(synced(5, H0), { type: 'local-changed', hash: null }))
    expect(token).toMatchObject({ kind: 'delete', hash: null, baseRev: 5 })
    const s2 = reduceSection(s, { type: 'push-applied', rev: 6 })
    expect(s2.base).toEqual({ rev: 6, hash: null })
    expect(s2.sot).toEqual({ rev: 6, hash: null })
    expect(s2.status).toBe('synced')
  })
  it('push-converged → base = {rev, SENT hash}', () => {
    const s = reduceSection(inFlightPut(), { type: 'push-converged', rev: 9 })
    expect(s.base).toEqual({ rev: 9, hash: H1 })
    expect(s.sot).toEqual({ rev: 9, hash: H1 })
    expect(s.status).toBe('synced')
  })
  it('push-conflict on a section edited back to the base during the flight → clean, so no lock: row 3 pulls', () => {
    const s = run(inFlightPut(), { type: 'local-changed', hash: H0 }, { type: 'push-conflict', rev: 6, hash: H8 })
    expect(isDirty(s)).toBe(false)
    expect(s.status).toBe('synced')
    expect(s.conflict).toBeNull()
    expect(s.inFlight).toBeNull()
    expect(s.sot).toEqual({ rev: 6, hash: H8 })
    expect(s.base).toEqual({ rev: 5, hash: H0 })
    expect(retainedHashes(s)).toEqual([])
    expect(canApplyPull(s)).toBe(true)
    expect(decideSection(s, ON)).toEqual({ do: 'pull' })
  })
  it('push-conflict on a delete undone during the flight → clean, no lock (rev 0 too)', () => {
    const [f] = startFlight(run(synced(5, H0), { type: 'local-changed', hash: null }))
    const s = run(f, { type: 'local-changed', hash: H0 }, { type: 'push-conflict', rev: 0, hash: null })
    expect(s.status).toBe('synced')
    expect(s.conflict).toBeNull()
    expect(s.sot).toEqual({ rev: 5, hash: null })
    expect(decideSection(s, ON)).toEqual({ do: 'pull' })
  })
  it('push-conflict on a section edited to a THIRD value during the flight → still dirty → locks, local side = the sent snapshot', () => {
    const s = run(inFlightPut(), { type: 'local-changed', hash: H2 }, { type: 'push-conflict', rev: 6, hash: H8 })
    expect(isDirty(s)).toBe(true)
    expect(s.status).toBe('locked:conflict')
    expect(s.conflict).toEqual({ localHash: H1, sot: { rev: 6, hash: H8 } })
    expect([...retainedHashes(s)].sort()).toEqual([H1, H8])
    expect(decideSection(s, ON)).toEqual({ do: 'nothing' })
  })
  it('push-conflict → locked:conflict with the SENT snapshot as the local side', () => {
    const s = run(inFlightPut(), { type: 'local-changed', hash: H2 }, { type: 'push-conflict', rev: 6, hash: H8 })
    expect(s.status).toBe('locked:conflict')
    expect(s.conflict).toEqual({ localHash: H1, sot: { rev: 6, hash: H8 } })
    expect(s.sot).toEqual({ rev: 6, hash: H8 })
    expect(s.currentHash).toBe(H2)
  })
  it('push-conflict rev 0 (absent / tombstone) → sot = {max(sot.rev, base.rev), null}', () => {
    const s = reduceSection(inFlightPut(), { type: 'push-conflict', rev: 0, hash: null })
    expect(s.sot).toEqual({ rev: 5, hash: null })
    expect(s.status).toBe('locked:conflict')
    expect(s.conflict).toEqual({ localHash: H1, sot: { rev: 5, hash: null } })
  })
  it('push-conflict whose SOT hash equals the CURRENT hash converges instead of locking', () => {
    const s = run(inFlightPut(), { type: 'local-changed', hash: H2 }, { type: 'push-conflict', rev: 6, hash: H2 })
    expect(s.status).toBe('synced')
    expect(s.conflict).toBeNull()
    expect(s.base).toEqual({ rev: 6, hash: H2 })
  })
  it('push-conflict never rewinds a newer sot learnt during the flight', () => {
    const s = run(inFlightPut(), { type: 'remote-event', rev: 9, hash: H8, own: false }, { type: 'push-conflict', rev: 8, hash: H2 })
    expect(s.sot).toEqual({ rev: 9, hash: H8 })
    expect(s.conflict).toEqual({ localHash: H1, sot: { rev: 9, hash: H8 } })
  })
  it('push-conflict at exactly the known rev leaves sot alone; the conflict shows the sot we hold', () => {
    const s = run(inFlightPut(), { type: 'remote-event', rev: 9, hash: H8, own: false }, { type: 'push-conflict', rev: 9, hash: H2 })
    expect(s.sot).toEqual({ rev: 9, hash: H8 })
    expect(s.conflict).toEqual({ localHash: H1, sot: { rev: 9, hash: H8 } })
  })

  describe('a 409 {rev:0} after a live SOT was learnt in flight is AMBIGUOUS: neither side is believed, the index is asked (R1 + critic findings)', () => {
    const OFF = { reachable: false, autoSync: true }
    /** absent on both sides and agreed so ({baseRev,null}); we create `sent`; while in flight an event says another machine created it: {5,H8}. */
    function creatingWhileTheyCreate(sent: string, baseRev = 0): SectionSyncState {
      const s0 = run(mk({ base: { rev: baseRev, hash: null }, sot: { rev: baseRev, hash: null }, currentHash: null }), { type: 'local-changed', hash: sent })
      expect(s0.indexStale).toBe(false)
      const [f, token] = startFlight(s0)
      expect(token).toMatchObject({ kind: 'put', hash: sent, baseRev: 0 })
      const moved = reduceSection(f, { type: 'remote-event', rev: 5, hash: H8, own: false })
      expect(moved.sot).toEqual({ rev: 5, hash: H8 })
      expect(moved.sotMovedWhileInFlight).toBe(true)
      return moved
    }
    /** …and then our PUT comes back 409 {rev:0}. Events and HTTP answers are not causally ordered: nobody can tell which is newer. */
    function ambiguous(sent: string, baseRev = 0): SectionSyncState {
      const before = deepFreeze(creatingWhileTheyCreate(sent, baseRev))
      const s = reduceSection(before, { type: 'push-conflict', rev: 0, hash: null })
      expect(s.inFlight).toBeNull()
      expect(s.sotMovedWhileInFlight).toBe(false)
      expect(s.sot).toEqual({ rev: 5, hash: H8 }) // untouched: neither erased nor trusted
      expect(s.base).toEqual({ rev: baseRev, hash: null })
      expect(s.status).toBe('pending')
      expect(s.conflict).toBeNull()
      expect(s.indexStale).toBe(true)
      expect(s.indexEpoch).toBe(before.indexEpoch + 1) // the flight closed
      expect(decideSection(s, ON)).toEqual({ do: 'reindex', indexEpoch: s.indexEpoch })
      return s
    }

    for (const baseRev of [0, 4]) {
      it(`direction B (critic): created {5,H8}, then deleted unseen — the 409 was authoritative. No lock on the deleted H8; the index says absent → push with baseRev 0 (base rev ${baseRev})`, () => {
        const s = indexed(ambiguous(H1, baseRev), null)
        expect(s.sot.hash).toBeNull()
        expect(s.status).toBe('pending')
        expect(s.conflict).toBeNull()
        const d = decideSection(s, ON)
        expect(d.do).toBe('push')
        expect(tokenOf(d)).toMatchObject({ kind: 'put', hash: H1, baseRev: 0 })
      })
    }

    it('direction A (R1): the 409 was old, the create is real. The index says {5,H8} → lock-conflict against the LIVE sot; keep-local pushes with baseRev 5, not 0', () => {
      let s = indexed(ambiguous(H1), { rev: 5, hash: H8 })
      expect(decideSection(s, ON)).toEqual({ do: 'lock-conflict' })
      s = reduceSection(s, { type: 'locked', reason: 'conflict' })
      expect(s.status).toBe('locked:conflict')
      expect(s.conflict).toEqual({ localHash: H1, sot: { rev: 5, hash: H8 } })
      s = reduceSection(s, { type: 'resolved', keep: 'local' })
      expect(s.base).toEqual({ rev: 5, hash: H8 })
      expect(tokenOf(decideSection(s, ON))).toMatchObject({ kind: 'put', hash: H1, baseRev: 5 })
    })

    it('what we sent is exactly what the event said they created: NOT folded on the unverified sot; the index decides', () => {
      const s = ambiguous(H8)
      expect(isDirty(s)).toBe(true)
      // live {5,H8} confirmed → now it folds
      const live = indexed(s, { rev: 5, hash: H8 })
      expect(live.status).toBe('synced')
      expect(live.base).toEqual({ rev: 5, hash: H8 })
      expect(decideSection(live, ON)).toEqual({ do: 'nothing' })
      // absent → still ours to create
      const gone = indexed(s, null)
      expect(gone.status).toBe('pending')
      expect(gone.base.hash).toBeNull()
      expect(tokenOf(decideSection(gone, ON))).toMatchObject({ kind: 'put', hash: H8, baseRev: 0 })
    })

    it('offline: nothing to do but wait — pending, not locked, not synced', () => {
      for (const sent of [H1, H8]) {
        const s = ambiguous(sent)
        expect(decideSection(s, OFF)).toEqual({ do: 'nothing' })
        expect(s.status).toBe('pending')
      }
    })

    it('a flight during which the SOT was seen DELETED still takes the rev 0 answer', () => {
      const moved = reduceSection(inFlightPut(), { type: 'remote-event', rev: 7, hash: null, own: false })
      expect(moved.sotMovedWhileInFlight).toBe(true)
      const s = reduceSection(moved, { type: 'push-conflict', rev: 0, hash: null })
      expect(s.sot).toEqual({ rev: 7, hash: null })
      expect(s.conflict).toEqual({ localHash: H1, sot: { rev: 7, hash: null } })
    })

    it('an ordinary rev 0 conflict (nothing learnt during the flight) is unchanged: the SOT is absent', () => {
      const before = inFlightPut()
      expect(before.sotMovedWhileInFlight).toBe(false)
      const s = reduceSection(before, { type: 'push-conflict', rev: 0, hash: null })
      expect(s.sot).toEqual({ rev: 5, hash: null })
      expect(s.conflict).toEqual({ localHash: H1, sot: { rev: 5, hash: null } })
      // keep-local then recreates over the tombstone with baseRev 0
      expect(tokenOf(decideSection(reduceSection(s, { type: 'resolved', keep: 'local' }), ON))).toMatchObject({ kind: 'put', hash: H1, baseRev: 0 })
    })
  })

  it('push-failed → back to dirty/pending, nothing else changes', () => {
    const before = inFlightPut()
    const s = reduceSection(before, { type: 'push-failed' })
    expect(s).toEqual({ ...before, inFlight: null, epoch: before.epoch + 1, indexEpoch: before.indexEpoch + 1 }) // the flight closed
    expect(s.status).toBe('pending')
    expect(decideSection(s, ON).do).toBe('push')
  })

  describe('shared invariant: every terminal event closes the flight, from every pre-state', () => {
    const terminals: SectionEvent[] = [
      { type: 'push-applied', rev: 6 },
      { type: 'push-converged', rev: 9 },
      { type: 'push-conflict', rev: 6, hash: H8 },
      { type: 'push-conflict', rev: 0, hash: null },
      { type: 'push-failed' },
    ]
    const kinds: ('put' | 'delete')[] = ['put', 'delete']
    for (const kind of kinds) {
      for (const moved of [false, true]) {
        for (const edited of [false, true]) {
          for (const e of terminals) {
            it(`${e.type}${e.type === 'push-conflict' ? `(rev ${e.rev})` : ''} · ${kind} · sotMoved=${moved} · editedInFlight=${edited}`, () => {
              let [s] = startFlight(run(synced(5, H0), { type: 'local-changed', hash: kind === 'put' ? H1 : null }))
              expect(s.inFlight?.kind).toBe(kind)
              if (moved) s = reduceSection(s, { type: 'remote-event', rev: 7, hash: 'h7', own: false })
              if (edited) s = reduceSection(s, { type: 'local-changed', hash: H2 })
              expect(s.sotMovedWhileInFlight).toBe(moved)
              const next = reduceSection(deepFreeze(s), e)
              expect(next.inFlight).toBeNull()
              expect(next.sotMovedWhileInFlight).toBe(false)
              expect(next.epoch).toBe(s.epoch + 1)
            })
          }
        }
      }
    }
    for (const e of terminals) {
      it(`${e.type}${e.type === 'push-conflict' ? `(rev ${e.rev})` : ''} with no flight open → same state reference`, () => {
        for (const s of [synced(5, H0), run(synced(5, H0), { type: 'local-changed', hash: H1 }), initialSectionState(null)]) {
          expect(reduceSection(s, e)).toBe(s)
        }
      })
    }
  })
})

describe('reduceSection — rule 6: pull-applied', () => {
  it('base = sot = {rev, hash}, currentHash = hash, forcePull cleared', () => {
    const s = run(synced(5, H0), { type: 'remote-event', rev: 6, hash: H1, own: false }, { type: 'pull-applied', rev: 6, hash: H1, localHash: H1 })
    expect(s.base).toEqual({ rev: 6, hash: H1 })
    expect(s.sot).toEqual({ rev: 6, hash: H1 })
    expect(s.currentHash).toBe(H1)
    expect(s.status).toBe('synced')
  })
  it('TWO HASHES, equal (the normal case): what was fetched is what the stores hold → clean', () => {
    const s = run(synced(5, H0), { type: 'remote-event', rev: 6, hash: H1, own: false }, { type: 'pull-applied', rev: 6, hash: H1, localHash: H1 })
    expect(isDirty(s)).toBe(false)
    expect(decideSection(s, ON)).toEqual({ do: 'nothing' })
  })
  it('TWO HASHES, different (a sanitiser changed what arrived): base and sot are the SOT’s, currentHash is the stores’ → dirty → push over the pulled rev', () => {
    const s = run(synced(5, H0), { type: 'remote-event', rev: 6, hash: H1, own: false }, { type: 'pull-applied', rev: 6, hash: H1, localHash: H2 })
    expect(s.base).toEqual({ rev: 6, hash: H1 })
    expect(s.sot).toEqual({ rev: 6, hash: H1 }) // the rebuilt hash is NOT recorded as the SOT's
    expect(s.currentHash).toBe(H2)
    expect(s.status).toBe('pending')
    expect(decideSection(s, ON)).toEqual({ do: 'push', token: { kind: 'put', hash: H2, baseRev: 6, epoch: s.epoch } })
  })
  it('TWO HASHES, a deletion applied while the workspace is still here (hash null, localHash = the empty tabs) → dirty against absent → create with wire baseRev 0', () => {
    const s = run(synced(5, H0), { type: 'remote-event', rev: 6, hash: null, own: false }, { type: 'pull-applied', rev: 6, hash: null, localHash: H3 })
    expect(s.base).toEqual({ rev: 6, hash: null })
    expect(s.sot).toEqual({ rev: 6, hash: null })
    expect(s.currentHash).toBe(H3)
    expect(decideSection(s, ON)).toEqual({ do: 'push', token: { kind: 'put', hash: H3, baseRev: 0, epoch: s.epoch } })
  })
  it('a pull of an absent SOT applies a deletion', () => {
    const s = run(synced(5, H0), { type: 'remote-event', rev: 6, hash: null, own: false }, { type: 'pull-applied', rev: 6, hash: null, localHash: null })
    expect(s.base).toEqual({ rev: 6, hash: null })
    expect(s.currentHash).toBeNull()
    expect(decideSection(s, ON)).toEqual({ do: 'nothing' })
  })
  it('a pull older than the known sot sets the base but not the sot → pulls again', () => {
    const s = run(
      synced(5, H0),
      { type: 'remote-event', rev: 6, hash: H1, own: false },
      { type: 'remote-event', rev: 7, hash: H2, own: false },
      { type: 'pull-applied', rev: 6, hash: H1, localHash: H1 },
    )
    expect(s.base).toEqual({ rev: 6, hash: H1 })
    expect(s.sot).toEqual({ rev: 7, hash: H2 })
    expect(decideSection(s, ON)).toEqual({ do: 'pull' })
  })
  it('is refused on a dirty section without forcePull (§4.6.2 rule 1) — canApplyPull says so first', () => {
    const s = run(synced(5, H0), { type: 'remote-event', rev: 6, hash: H1, own: false }, { type: 'local-changed', hash: H2 })
    expect(canApplyPull(s)).toBe(false)
    expect(reduceSection(s, { type: 'pull-applied', rev: 6, hash: H1, localHash: H1 })).toBe(s)
  })
  it('is refused while a flight is open', () => {
    const [s] = startFlight(run(synced(5, H0), { type: 'local-changed', hash: H1 }))
    expect(canApplyPull(s)).toBe(false)
    expect(reduceSection(s, { type: 'pull-applied', rev: 6, hash: H2, localHash: H2 })).toBe(s)
  })
  it('is refused while locked (a locked section refuses inbound)', () => {
    const [s] = startFlight(run(synced(5, H0), { type: 'local-changed', hash: H1 }))
    const locked = reduceSection(s, { type: 'push-conflict', rev: 6, hash: H2 })
    expect(canApplyPull(locked)).toBe(false)
    expect(reduceSection(locked, { type: 'pull-applied', rev: 6, hash: H2, localHash: H2 })).toBe(locked)
  })
  it('forcePull is only ever set by an unlock, so forcePull and locked never coexist', () => {
    const [s] = startFlight(run(synced(5, H0), { type: 'local-changed', hash: H1 }))
    const resolved = run(s, { type: 'push-conflict', rev: 6, hash: H2 }, { type: 'resolved', keep: 'sot' })
    expect(resolved.forcePull).toBe(true)
    expect(resolved.status).toBe('pending')
    expect(canApplyPull(resolved)).toBe(true)
  })
})

describe('reduceSection — rule 7: locked / resolved / local-restored', () => {
  const conflicted = () => run(synced(5, H0), { type: 'local-changed', hash: H1 }, { type: 'remote-event', rev: 6, hash: H2, own: false })

  it('locked{conflict} (decide-time) → locked:conflict; the local side is the live hash', () => {
    const s = conflicted()
    expect(decideSection(s, ON)).toEqual({ do: 'lock-conflict' })
    const l = reduceSection(s, { type: 'locked', reason: 'conflict' })
    expect(l.status).toBe('locked:conflict')
    expect(l.conflict).toEqual({ localHash: H1, sot: { rev: 6, hash: H2 } })
    expect(l.epoch).toBe(s.epoch + 1)
  })
  it('locked{reset} → locked:reset, no conflict pair', () => {
    const s0 = synced(5, H0)
    const s = reduceSection(s0, { type: 'sot-index', epoch: s0.indexEpoch, entry: { rev: 2, hash: H8 } })
    const l = reduceSection(s, { type: 'locked', reason: 'reset' })
    expect(l.status).toBe('locked:reset')
    expect(l.conflict).toBeNull()
  })
  it('a lock the current state does not call for is refused (stale lock decision)', () => {
    const s = synced(5, H0)
    expect(reduceSection(s, { type: 'locked', reason: 'conflict' })).toBe(s)
    expect(reduceSection(s, { type: 'locked', reason: 'reset' })).toBe(s)
    const c = conflicted()
    expect(reduceSection(c, { type: 'locked', reason: 'reset' })).toBe(c)
    // decided lock-conflict, then the other side turned out to have made the same edit
    const converged = reduceSection(c, { type: 'remote-event', rev: 7, hash: H1, own: false })
    expect(converged.status).toBe('synced')
    expect(reduceSection(converged, { type: 'locked', reason: 'conflict' })).toBe(converged)
  })
  it('resolved on an unlocked section is ignored', () => {
    const s = conflicted()
    expect(reduceSection(s, { type: 'resolved', keep: 'local' })).toBe(s)
    expect(reduceSection(s, { type: 'resolved', keep: 'sot' })).toBe(s)
  })
  it('resolved keep:sot → unlock, forcePull', () => {
    const s = run(conflicted(), { type: 'locked', reason: 'conflict' }, { type: 'resolved', keep: 'sot' })
    expect(s.status).toBe('pending')
    expect(s.conflict).toBeNull()
    expect(s.forcePull).toBe(true)
    expect(decideSection(s, ON)).toEqual({ do: 'pull' })
  })
  it('resolved keep:local → base = conflict.sot, restoreLocal = the sent snapshot', () => {
    const [f] = startFlight(run(synced(5, H0), { type: 'local-changed', hash: H1 }))
    const s = run(f, { type: 'local-changed', hash: H2 }, { type: 'push-conflict', rev: 6, hash: H8 }, { type: 'resolved', keep: 'local' })
    expect(s.status).toBe('pending')
    expect(s.conflict).toBeNull()
    expect(s.base).toEqual({ rev: 6, hash: H8 })
    expect(s.restoreLocal).toEqual({ hash: H1 })
    expect(decideSection(s, { reachable: false, autoSync: false })).toEqual({ do: 'restore-local', hash: H1 })
  })
  it('restoreLocal is dropped as soon as the live hash already is the snapshot (nothing to put back)', () => {
    const s = run(conflicted(), { type: 'locked', reason: 'conflict' }, { type: 'resolved', keep: 'local' })
    expect(s.restoreLocal).toBeNull()
    expect(tokenOf(decideSection(s, ON))).toMatchObject({ hash: H1, baseRev: 6 })
  })
  it('a DELETE that hit a 409: keep-local restores "absent" and re-sends the delete against the newest rev', () => {
    const [f, token] = startFlight(run(synced(5, H0), { type: 'local-changed', hash: null }))
    expect(token).toMatchObject({ kind: 'delete', hash: null, baseRev: 5 })
    // they edited it while we were deleting it: the SOT is live
    let s = reduceSection(f, { type: 'push-conflict', rev: 6, hash: H2 })
    expect(s.status).toBe('locked:conflict')
    expect(s.conflict).toEqual({ localHash: null, sot: { rev: 6, hash: H2 } })
    // while locked the user re-creates it locally, and the SOT moves again
    s = run(s, { type: 'local-changed', hash: H1 }, { type: 'remote-event', rev: 7, hash: H8, own: false })
    s = reduceSection(s, { type: 'resolved', keep: 'local' })
    expect(s.restoreLocal).toEqual({ hash: null })
    expect(s.base).toEqual({ rev: 7, hash: H8 })
    expect(retainedHashes(s)).toEqual([])
    expect(decideSection(s, ON)).toEqual({ do: 'restore-local', hash: null })
    expect(decideSection(s, { reachable: false, autoSync: false })).toEqual({ do: 'restore-local', hash: null })
    s = reduceSection(s, { type: 'local-restored', hash: null })
    expect(s.currentHash).toBeNull()
    expect(s.restoreLocal).toBeNull()
    const d = decideSection(s, ON)
    expect(d.do).toBe('delete')
    expect(tokenOf(d)).toEqual({ kind: 'delete', hash: null, baseRev: 7, epoch: s.epoch })
  })
  it('restoreLocal {hash:null} is dropped at once when the section already is absent locally', () => {
    const [f] = startFlight(run(synced(5, H0), { type: 'local-changed', hash: null }))
    const s = run(f, { type: 'push-conflict', rev: 6, hash: H2 }, { type: 'resolved', keep: 'local' })
    expect(s.restoreLocal).toBeNull()
    expect(tokenOf(decideSection(s, ON))).toMatchObject({ kind: 'delete', hash: null, baseRev: 6 })
  })
  it('local-restored sets currentHash and clears restoreLocal; without a pending restore it is ignored', () => {
    const [f] = startFlight(run(synced(5, H0), { type: 'local-changed', hash: H1 }))
    const s = run(f, { type: 'local-changed', hash: H2 }, { type: 'push-conflict', rev: 6, hash: H8 }, { type: 'resolved', keep: 'local' })
    const r = reduceSection(s, { type: 'local-restored', hash: H1 })
    expect(r.currentHash).toBe(H1)
    expect(r.restoreLocal).toBeNull()
    expect(reduceSection(r, { type: 'local-restored', hash: H2 })).toBe(r)
  })
  describe('a keep-local restore is cancelled by an edit made after the user resolved (attack finding)', () => {
    /** base = sot = {5,H0} → H1 sent → H2 edited in flight → 409 {6,H8} → keep-local: restoreLocal = H1, live = H2. */
    function awaitingRestore(): SectionSyncState {
      const [f] = startFlight(run(synced(5, H0), { type: 'local-changed', hash: H1 }))
      const locked = run(f, { type: 'local-changed', hash: H2 }, { type: 'push-conflict', rev: 6, hash: H8 })
      expect(locked.status).toBe('locked:conflict')
      expect(locked.conflict?.localHash).toBe(H1)
      const s = reduceSection(locked, { type: 'resolved', keep: 'local' })
      expect(s.restoreLocal).toEqual({ hash: H1 })
      expect(decideSection(s, ON)).toEqual({ do: 'restore-local', hash: H1 })
      return s
    }

    it('H1 → H2 → 409 → keep-local → H3: the restore is dropped, H3 is what gets pushed, against the newest rev', () => {
      const s = reduceSection(deepFreeze(awaitingRestore()), { type: 'local-changed', hash: H3 })
      expect(s.restoreLocal).toBeNull()
      expect(s.currentHash).toBe(H3)
      expect(decideSection(s, ON).do).not.toBe('restore-local')
      expect(retainedHashes(s)).not.toContain(H1)
      const token = tokenOf(decideSection(s, ON))
      expect(token).toMatchObject({ kind: 'put', hash: H3, baseRev: 6 })
      expect(token.baseRev).toBe(s.sot.rev)
      // the restore the driver had already started lands late: ignored, H3 survives
      const late = reduceSection(s, { type: 'local-restored', hash: H1 })
      expect(late).toBe(s)
      expect(late.currentHash).toBe(H3)
      const [f] = startFlight(late)
      const done = reduceSection(f, { type: 'push-applied', rev: 7 })
      expect(done.base).toEqual({ rev: 7, hash: H3 })
      expect(done.status).toBe('synced')
    })

    it('local-restored with a hash that is not the pending one is ignored', () => {
      const s = awaitingRestore()
      for (const hash of [H2, H3, H8, null]) expect(reduceSection(s, { type: 'local-restored', hash })).toBe(s)
      const r = reduceSection(s, { type: 'local-restored', hash: H1 })
      expect(r.currentHash).toBe(H1)
      expect(r.restoreLocal).toBeNull()
    })

    it('a local-changed that changes nothing (same hash) does not cancel the restore', () => {
      const s = awaitingRestore()
      const same = reduceSection(s, { type: 'local-changed', hash: H2 })
      expect(same).toBe(s)
      expect(decideSection(same, ON)).toEqual({ do: 'restore-local', hash: H1 })
    })

    it('a pending restore to "absent" is cancelled the same way', () => {
      let [s] = startFlight(run(synced(5, H0), { type: 'local-changed', hash: null }))
      s = run(s, { type: 'local-changed', hash: H2 }, { type: 'push-conflict', rev: 6, hash: H8 }, { type: 'resolved', keep: 'local' })
      expect(s.restoreLocal).toEqual({ hash: null })
      s = reduceSection(s, { type: 'local-changed', hash: H3 })
      expect(s.restoreLocal).toBeNull()
      expect(reduceSection(s, { type: 'local-restored', hash: null })).toBe(s)
      expect(tokenOf(decideSection(s, ON))).toMatchObject({ kind: 'put', hash: H3, baseRev: 6 })
    })

    it('canRestoreLocal: true only for the hash that is pending, so the driver can check BEFORE it writes the stores', () => {
      const s = awaitingRestore()
      expect(canRestoreLocal(s, H1)).toBe(true)
      expect(canRestoreLocal(s, H2)).toBe(false)
      expect(canRestoreLocal(s, null)).toBe(false)
      const cancelled = reduceSection(s, { type: 'local-changed', hash: H3 })
      expect(canRestoreLocal(cancelled, H1)).toBe(false)
      expect(canRestoreLocal(synced(5, H0), H0)).toBe(false)
      expect(canRestoreLocal(synced(5, null), null)).toBe(false) // no pending restore: null does not match "nothing"
      expect(canRestoreLocal(mk({ ...synced(5, H0), currentHash: H1, restoreLocal: { hash: null } }), null)).toBe(true)
    })
  })

  it('locked:reset keeps learning; keep:local rebases on the newest sot and pushes', () => {
    const s0 = run(synced(5, H0), { type: 'local-changed', hash: H1 })
    const l = run(
      s0,
      { type: 'sot-index', epoch: s0.indexEpoch, entry: { rev: 2, hash: H8 } },
      { type: 'locked', reason: 'reset' },
      { type: 'remote-event', rev: 3, hash: H2, own: false },
      { type: 'resolved', keep: 'local' },
    )
    expect(l.status).toBe('pending')
    expect(l.base).toEqual({ rev: 3, hash: H2 })
    expect(tokenOf(decideSection(l, ON))).toMatchObject({ kind: 'put', hash: H1, baseRev: 3 })
  })
  it('locked:reset, keep:sot → forcePull', () => {
    const s0 = synced(5, H0)
    const l = run(s0, { type: 'sot-index', epoch: s0.indexEpoch, entry: { rev: 2, hash: H8 } }, { type: 'locked', reason: 'reset' }, { type: 'resolved', keep: 'sot' })
    expect(decideSection(l, ON)).toEqual({ do: 'pull' })
    const p = reduceSection(l, { type: 'pull-applied', rev: 2, hash: H8, localHash: H8 })
    expect(p.base).toEqual({ rev: 2, hash: H8 })
    expect(p.status).toBe('synced')
    expect(decideSection(p, ON)).toEqual({ do: 'nothing' })
  })
  it('local edits while locked only move currentHash', () => {
    const l = run(conflicted(), { type: 'locked', reason: 'conflict' })
    const l2 = reduceSection(l, { type: 'local-changed', hash: 'h9' })
    expect(l2).toEqual({ ...l, currentHash: 'h9', epoch: l.epoch + 1 })
  })
})

describe('reduceSection — locked:invalid (P2b plan Task 6): the SOT holds a payload this client refuses to apply', () => {
  /** clean, SOT moved to {6,H2}: row 3 says pull — the only state a driver can be judging a payload in */
  const pullable = () => run(synced(5, H0), { type: 'remote-event', rev: 6, hash: H2, own: false })
  const invalid = () => reduceSection(pullable(), { type: 'locked', reason: 'invalid', rev: 6 })

  it('locked{invalid, rev} on a state that decides pull, for the very rev held → locked:invalid, invalid = the sot that was refused', () => {
    const s = pullable()
    expect(decideSection(s, ON)).toEqual({ do: 'pull' })
    const l = reduceSection(s, { type: 'locked', reason: 'invalid', rev: 6 })
    expect(l).toEqual({ ...s, status: 'locked:invalid', invalid: { rev: 6, hash: H2 }, epoch: s.epoch + 1 })
    expect(l.conflict).toBeNull()
    expect(l.indexEpoch).toBe(s.indexEpoch)
  })
  it('while locked:invalid: decide is nothing (0a) online and off, no pull may be applied, no flight opens, nothing is retained', () => {
    const l = invalid()
    expect(decideSection(l, ON)).toEqual({ do: 'nothing' })
    expect(decideSection(l, { reachable: false, autoSync: false })).toEqual({ do: 'nothing' })
    expect(canApplyPull(l)).toBe(false)
    expect(reduceSection(l, { type: 'pull-applied', rev: 6, hash: H2, localHash: H2 })).toBe(l)
    expect(reduceSection(l, { type: 'push-started', token: { kind: 'put', hash: H0, baseRev: 5, epoch: l.epoch } })).toBe(l)
    expect(retainedHashes(l)).toEqual([])
  })
  it('0a precedes 0d for locked:invalid too: a stale index does not make it reindex', () => {
    const l = reduceSection(invalid(), { type: 'reconnected' })
    expect(l.status).toBe('locked:invalid')
    expect(decideSection(l, ON)).toEqual({ do: 'nothing' })
  })

  describe('refused (same reference) unless the state decides pull for exactly that rev', () => {
    it('rev is not sot.rev — the judgement was about another payload', () => {
      const s = pullable()
      expect(reduceSection(s, { type: 'locked', reason: 'invalid', rev: 5 })).toBe(s)
      expect(reduceSection(s, { type: 'locked', reason: 'invalid', rev: 7 })).toBe(s)
    })
    it('the SOT moved on while the payload was being judged', () => {
      const s = reduceSection(pullable(), { type: 'remote-event', rev: 7, hash: H3, own: false })
      expect(reduceSection(s, { type: 'locked', reason: 'invalid', rev: 6 })).toBe(s)
    })
    it('decide is nothing (synced)', () => {
      const s = synced(5, H0)
      expect(reduceSection(s, { type: 'locked', reason: 'invalid', rev: 5 })).toBe(s)
    })
    it('dirty, SOT not moved (decide is push)', () => {
      const s = run(synced(5, H0), { type: 'local-changed', hash: H1 })
      expect(reduceSection(s, { type: 'locked', reason: 'invalid', rev: 5 })).toBe(s)
    })
    it('dirty, SOT moved (decide is lock-conflict)', () => {
      const s = run(pullable(), { type: 'local-changed', hash: H1 })
      expect(decideSection(s, ON)).toEqual({ do: 'lock-conflict' })
      expect(reduceSection(s, { type: 'locked', reason: 'invalid', rev: 6 })).toBe(s)
    })
    it('a flight is open', () => {
      const [f] = startFlight(run(synced(5, H0), { type: 'local-changed', hash: H1 }))
      const s = reduceSection(f, { type: 'remote-event', rev: 6, hash: H2, own: false })
      expect(reduceSection(s, { type: 'locked', reason: 'invalid', rev: 6 })).toBe(s)
    })
    it('already locked — conflict, reset, or invalid itself', () => {
      const c = run(pullable(), { type: 'local-changed', hash: H1 }, { type: 'locked', reason: 'conflict' })
      expect(reduceSection(c, { type: 'locked', reason: 'invalid', rev: 6 })).toBe(c)
      const s0 = synced(5, H0)
      const r = run(s0, { type: 'sot-index', epoch: s0.indexEpoch, entry: { rev: 2, hash: H8 } }, { type: 'locked', reason: 'reset' })
      expect(reduceSection(r, { type: 'locked', reason: 'invalid', rev: 2 })).toBe(r)
      const l = invalid()
      expect(reduceSection(l, { type: 'locked', reason: 'invalid', rev: 6 })).toBe(l)
    })
    it('the index is stale (decide is reindex): the driver re-judges after the reindex', () => {
      const s = reduceSection(pullable(), { type: 'reconnected' })
      expect(reduceSection(s, { type: 'locked', reason: 'invalid', rev: 6 })).toBe(s)
    })
    it('locked{conflict|reset} are unchanged by the new reason: neither is accepted on a pull state', () => {
      const s = pullable()
      expect(reduceSection(s, { type: 'locked', reason: 'conflict' })).toBe(s)
      expect(reduceSection(s, { type: 'locked', reason: 'reset' })).toBe(s)
    })
  })

  describe('auto-unlock: a SOT observation that differs from `invalid` in ANY way — and only that', () => {
    it('THE loop guard: the same rev and the same hash, by index or by event, never unlocks — same reference back', () => {
      const l = invalid()
      expect(reduceSection(l, { type: 'sot-index', epoch: l.indexEpoch, entry: { rev: 6, hash: H2 } })).toBe(l)
      expect(reduceSection(l, { type: 'remote-event', rev: 6, hash: H2, own: false })).toBe(l)
      // … also when the index was stale: it lands, clears indexStale, and the section stays shut
      const stale = reduceSection(l, { type: 'reconnected' })
      const landed = reduceSection(stale, { type: 'sot-index', epoch: stale.indexEpoch, entry: { rev: 6, hash: H2 } })
      expect(landed.indexStale).toBe(false)
      expect(landed.status).toBe('locked:invalid')
      expect(landed.invalid).toEqual({ rev: 6, hash: H2 })
      expect(decideSection(landed, ON)).toEqual({ do: 'nothing' })
    })
    it('remote-event with a higher rev → unlocked, invalid cleared, status re-derived, the next decision pulls again', () => {
      const l = invalid()
      const u = reduceSection(l, { type: 'remote-event', rev: 7, hash: H3, own: false })
      expect(u.status).toBe('synced')
      expect(u.invalid).toBeNull()
      expect(u.sot).toEqual({ rev: 7, hash: H3 })
      expect(u.epoch).toBe(l.epoch + 1)
      expect(decideSection(u, ON)).toEqual({ do: 'pull' })
    })
    it('a higher rev carrying the SAME hash still unlocks: it is another write', () => {
      const u = reduceSection(invalid(), { type: 'remote-event', rev: 7, hash: H2, own: false })
      expect(u.invalid).toBeNull()
      expect(decideSection(u, ON)).toEqual({ do: 'pull' })
    })
    it('an epoch-matching sot-index with a higher rev → unlocked', () => {
      const l = invalid()
      const u = reduceSection(l, { type: 'sot-index', epoch: l.indexEpoch, entry: { rev: 7, hash: H3 } })
      expect(u.status).toBe('synced')
      expect(u.invalid).toBeNull()
      expect(decideSection(u, ON)).toEqual({ do: 'pull' })
    })
    it('same rev, another hash (index) → unlocked: that payload is no longer the SOT', () => {
      const l = invalid()
      const u = reduceSection(l, { type: 'sot-index', epoch: l.indexEpoch, entry: { rev: 6, hash: H3 } })
      expect(u.sot).toEqual({ rev: 6, hash: H3 })
      expect(u.invalid).toBeNull()
      expect(u.status).toBe('synced')
      expect(decideSection(u, ON)).toEqual({ do: 'pull' })
    })
    it('not listed any more (index → absent, rev = max(...) = the refused rev) → unlocked; clean + moved → pull, i.e. apply the deletion', () => {
      const l = invalid()
      const u = reduceSection(l, { type: 'sot-index', epoch: l.indexEpoch, entry: null })
      expect(u.sot).toEqual({ rev: 6, hash: null })
      expect(u.invalid).toBeNull()
      expect(u.status).toBe('synced')
      expect(decideSection(u, ON)).toEqual({ do: 'pull' })
      const p = reduceSection(u, { type: 'pull-applied', rev: 6, hash: null, localHash: null })
      expect(p.currentHash).toBeNull()
      expect(decideSection(p, ON)).toEqual({ do: 'nothing' })
    })
    it('the profile was rebuilt (index lowers the rev below the base) → unlocked, and row 1 asks for lock-reset', () => {
      const l = invalid()
      const u = reduceSection(l, { type: 'sot-index', epoch: l.indexEpoch, entry: { rev: 2, hash: H8 } })
      expect(u.sot).toEqual({ rev: 2, hash: H8 })
      expect(u.invalid).toBeNull()
      expect(decideSection(u, ON)).toEqual({ do: 'lock-reset' })
      expect(reduceSection(u, { type: 'locked', reason: 'reset' }).status).toBe('locked:reset')
    })
    it('a lower rev with the same hash unlocks too', () => {
      const l = invalid()
      const u = reduceSection(l, { type: 'sot-index', epoch: l.indexEpoch, entry: { rev: 2, hash: H2 } })
      expect(u.invalid).toBeNull()
      expect(decideSection(u, ON)).toEqual({ do: 'lock-reset' })
    })
    it('while locked:invalid, sot is exactly what was refused', () => {
      const l = invalid()
      expect(l.invalid).toEqual(l.sot)
      expect(l.invalid).not.toBe(l.sot)
    })
    it('not an observation: own events, a discarded index, reconnected, local edits', () => {
      const l = invalid()
      expect(reduceSection(l, { type: 'remote-event', rev: 9, hash: H3, own: true })).toBe(l)
      const discarded = reduceSection(l, { type: 'sot-index', epoch: l.indexEpoch - 1, entry: { rev: 9, hash: H3 } })
      expect(discarded.status).toBe('locked:invalid')
      expect(discarded.invalid).toEqual({ rev: 6, hash: H2 })
      expect(discarded.indexStale).toBe(true)
      expect(reduceSection(l, { type: 'reconnected' }).status).toBe('locked:invalid')
      const edited = reduceSection(l, { type: 'local-changed', hash: H1 })
      expect(edited).toEqual({ ...l, currentHash: H1, epoch: l.epoch + 1 })
    })
    it('edited while locked, then unlocked → dirty against a moved SOT: the table asks for lock-conflict', () => {
      const u = run(invalid(), { type: 'local-changed', hash: H1 }, { type: 'remote-event', rev: 7, hash: H3, own: false })
      expect(u.status).toBe('pending')
      expect(u.invalid).toBeNull()
      expect(decideSection(u, ON)).toEqual({ do: 'lock-conflict' })
    })
    it('a newer rev does not touch the other locks', () => {
      const c = run(pullable(), { type: 'local-changed', hash: H1 }, { type: 'locked', reason: 'conflict' })
      expect(reduceSection(c, { type: 'remote-event', rev: 9, hash: H3, own: false }).status).toBe('locked:conflict')
    })
  })

  describe('resolved', () => {
    it('keep:sot is REFUSED — that payload is the one that cannot be applied', () => {
      const l = invalid()
      expect(reduceSection(l, { type: 'resolved', keep: 'sot' })).toBe(l)
    })
    it('keep:local → unlocked, base = sot; the clean section is now dirty and the whole road to the push is open', () => {
      const l = invalid()
      const r = reduceSection(l, { type: 'resolved', keep: 'local' })
      expect(r).toEqual({ ...l, status: 'pending', invalid: null, base: { rev: 6, hash: H2 }, epoch: l.epoch + 1, indexEpoch: l.indexEpoch + 1 })
      expect(r.base).not.toBe(l.sot)
      expect(r.restoreLocal).toBeNull()
      expect(r.forcePull).toBe(false)
      const [f, token] = startFlight(r)
      expect(token).toMatchObject({ kind: 'put', hash: H0, baseRev: 6 })
      const done = reduceSection(f, { type: 'push-applied', rev: 7 })
      expect(done.status).toBe('synced')
      expect(done.base).toEqual({ rev: 7, hash: H0 })
      expect(decideSection(done, ON)).toEqual({ do: 'nothing' })
    })
    it('keep:local over an ABSENT sot cannot arise from this lock: seeing the SOT absent already unlocked it (the deletion is pulled)', () => {
      const gone = reduceSection(invalid(), { type: 'sot-index', epoch: invalid().indexEpoch, entry: null })
      expect(gone.status).toBe('synced')
      expect(reduceSection(gone, { type: 'resolved', keep: 'local' })).toBe(gone)
    })
    it('keep:local on a section that does not exist locally → delete against the rev that was refused', () => {
      const s = indexed(initialSectionState(null), { rev: 3, hash: H2 })
      const l = reduceSection(s, { type: 'locked', reason: 'invalid', rev: 3 })
      expect(l.status).toBe('locked:invalid')
      const r = reduceSection(l, { type: 'resolved', keep: 'local' })
      const [, token] = startFlight(r)
      expect(token).toMatchObject({ kind: 'delete', hash: null, baseRev: 3 })
    })
    it('keep:local on a stale index: reindex first, then the push', () => {
      const r = run(invalid(), { type: 'reconnected' }, { type: 'resolved', keep: 'local' })
      expect(decideSection(r, ON)).toMatchObject({ do: 'reindex' })
      expect(tokenOf(decideSection(indexed(r, { rev: 6, hash: H2 }), ON))).toMatchObject({ kind: 'put', hash: H0, baseRev: 6 })
    })
    it('keep:local when the SOT holds the very hash we have (newer rev only) → nothing to push: synced', () => {
      const s = run(synced(5, H0), { type: 'remote-event', rev: 6, hash: H0, own: false })
      expect(decideSection(s, ON)).toEqual({ do: 'pull' })
      const r = run(s, { type: 'locked', reason: 'invalid', rev: 6 }, { type: 'resolved', keep: 'local' })
      expect(r.status).toBe('synced')
      expect(decideSection(r, ON)).toEqual({ do: 'nothing' })
    })
  })

  describe('judged during a forcePull pull (the user had said "take the SOT", the section is dirty)', () => {
    const forced = () => run(pullable(), { type: 'local-changed', hash: H1 }, { type: 'locked', reason: 'conflict' }, { type: 'resolved', keep: 'sot' })

    it('is accepted (decide is pull) and drops forcePull — forcePull and locked never coexist', () => {
      const s = forced()
      expect(decideSection(s, ON)).toEqual({ do: 'pull' })
      const l = reduceSection(s, { type: 'locked', reason: 'invalid', rev: 6 })
      expect(l.status).toBe('locked:invalid')
      expect(l.invalid).toEqual({ rev: 6, hash: H2 })
      expect(l.forcePull).toBe(false)
      expect(l.conflict).toBeNull()
    })
    it('keep:local then pushes the live hash over it; a newer rev instead re-opens the question as a conflict', () => {
      const l = reduceSection(forced(), { type: 'locked', reason: 'invalid', rev: 6 })
      expect(tokenOf(decideSection(reduceSection(l, { type: 'resolved', keep: 'local' }), ON))).toMatchObject({ kind: 'put', hash: H1, baseRev: 6 })
      const u = reduceSection(l, { type: 'remote-event', rev: 7, hash: H3, own: false })
      expect(decideSection(u, ON)).toEqual({ do: 'lock-conflict' })
    })
  })
})

describe('restoreSectionState — a persisted conflict (spec §4.6.2: the local side is the snapshot that was SENT)', () => {
  const persisted = () => deepFreeze({ base: { rev: 5, hash: H0 }, currentHash: H3, conflict: { localHash: H1, sot: { rev: 6, hash: H2 } } })

  it('restores locked:conflict with the pair as persisted and sot = conflict.sot; everything else as without one', () => {
    const p = persisted()
    const s = restoreSectionState(p)
    expect(s).toEqual({
      base: { rev: 5, hash: H0 },
      currentHash: H3,
      sot: { rev: 6, hash: H2 },
      epoch: 0,
      indexEpoch: 0,
      status: 'locked:conflict',
      inFlight: null,
      sotMovedWhileInFlight: false,
      conflict: { localHash: H1, sot: { rev: 6, hash: H2 } },
      invalid: null,
      forcePull: false,
      restoreLocal: null,
      indexStale: true,
    })
    expect(s.conflict).not.toBe(p.conflict)
    expect([...retainedHashes(s)].sort()).toEqual([H1, H2])
  })
  it('stays locked even when the live hash happens to equal the base', () => {
    expect(restoreSectionState({ ...persisted(), currentHash: H0 }).status).toBe('locked:conflict')
  })
  it('conflict: undefined is the same as no conflict', () => {
    expect(restoreSectionState({ base: { rev: 5, hash: H0 }, currentHash: H1, conflict: undefined })).toEqual(restoreSectionState({ base: { rev: 5, hash: H0 }, currentHash: H1 }))
  })
  it('0a precedes 0d: locked and index-stale decides nothing — it does not reindex, and it is not stuck either (resolved comes first)', () => {
    const s = restoreSectionState(persisted())
    expect(decideSection(s, ON)).toEqual({ do: 'nothing' })
    expect(reduceSection(s, { type: 'pull-applied', rev: 6, hash: H2, localHash: H2 })).toBe(s)
    expect(canApplyPull(s)).toBe(false)
  })
  it('keeps learning like a lock made in the session: remote-event and the first index move sot AND conflict.sot', () => {
    const s = restoreSectionState(persisted())
    const e = reduceSection(s, { type: 'remote-event', rev: 7, hash: H8, own: false })
    expect(e.sot).toEqual({ rev: 7, hash: H8 })
    expect(e.conflict).toEqual({ localHash: H1, sot: { rev: 7, hash: H8 } })
    expect(e.status).toBe('locked:conflict')
    const i = reduceSection(s, { type: 'sot-index', epoch: s.indexEpoch, entry: { rev: 8, hash: H8 } })
    expect(i.conflict).toEqual({ localHash: H1, sot: { rev: 8, hash: H8 } })
    expect(i.indexStale).toBe(false)
    expect(i.status).toBe('locked:conflict')
  })
  it('resolved keep:local → restore-local (before the reindex) → reindex → push of the SENT snapshot', () => {
    const r = reduceSection(restoreSectionState(persisted()), { type: 'resolved', keep: 'local' })
    expect(r.base).toEqual({ rev: 6, hash: H2 })
    expect(r.restoreLocal).toEqual({ hash: H1 })
    expect(decideSection(r, ON)).toEqual({ do: 'restore-local', hash: H1 })
    const put = reduceSection(r, { type: 'local-restored', hash: H1 })
    expect(put.currentHash).toBe(H1)
    expect(decideSection(put, ON)).toMatchObject({ do: 'reindex' })
    const [f, token] = startFlight(indexed(put, { rev: 6, hash: H2 }))
    expect(token).toMatchObject({ kind: 'put', hash: H1, baseRev: 6 })
    expect(reduceSection(f, { type: 'push-applied', rev: 7 }).status).toBe('synced')
  })
  it('resolved keep:sot → forcePull → reindex → pull → synced', () => {
    const r = reduceSection(restoreSectionState(persisted()), { type: 'resolved', keep: 'sot' })
    expect(r.forcePull).toBe(true)
    expect(r.conflict).toBeNull()
    expect(decideSection(r, ON)).toMatchObject({ do: 'reindex' })
    const i = indexed(r, { rev: 6, hash: H2 })
    expect(decideSection(i, ON)).toEqual({ do: 'pull' })
    expect(canApplyPull(i)).toBe(true)
    const p = reduceSection(i, { type: 'pull-applied', rev: 6, hash: H2, localHash: H2 })
    expect(p.status).toBe('synced')
    expect(p.currentHash).toBe(H2)
  })
  it('a restored lock and the session lock it was persisted from resolve to the same thing', () => {
    const [f] = startFlight(run(synced(5, H0), { type: 'local-changed', hash: H1 }))
    const live = run(f, { type: 'local-changed', hash: H3 }, { type: 'push-conflict', rev: 6, hash: H2 })
    expect(live.status).toBe('locked:conflict')
    const back = restoreSectionState({ base: live.base, currentHash: live.currentHash, conflict: live.conflict ?? undefined })
    const events: SectionEvent[] = [{ type: 'remote-event', rev: 7, hash: H8, own: false }, { type: 'resolved', keep: 'local' }]
    const a = run(live, ...events)
    const b = run(back, ...events)
    for (const k of ['base', 'currentHash', 'sot', 'status', 'conflict', 'restoreLocal', 'forcePull', 'invalid'] as const) expect(b[k]).toEqual(a[k])
    expect(decideSection(b, ON)).toEqual(decideSection(a, ON))
  })
})

describe('retainedHashes — rule 8', () => {
  it('nothing to retain on a quiet section', () => {
    expect(retainedHashes(synced(5, H0))).toEqual([])
  })
  it('the payload in flight', () => {
    const [s] = startFlight(run(synced(5, H0), { type: 'local-changed', hash: H1 }))
    expect(retainedHashes(s)).toEqual([H1])
  })
  it('both sides of an open conflict', () => {
    const [f] = startFlight(run(synced(5, H0), { type: 'local-changed', hash: H1 }))
    const s = reduceSection(f, { type: 'push-conflict', rev: 6, hash: H8 })
    expect([...retainedHashes(s)].sort()).toEqual([H1, H8])
  })
  it('the snapshot awaiting restore', () => {
    const [f] = startFlight(run(synced(5, H0), { type: 'local-changed', hash: H1 }))
    const s = run(f, { type: 'local-changed', hash: H2 }, { type: 'push-conflict', rev: 6, hash: H8 }, { type: 'resolved', keep: 'local' })
    expect(retainedHashes(s)).toEqual([H1])
  })
  it('drops nulls and duplicates', () => {
    const s = mk({
      inFlight: { kind: 'delete', hash: null, baseRev: 5, epoch: 0 },
      conflict: { localHash: H1, sot: { rev: 6, hash: H1 } },
      restoreLocal: { hash: H1 },
      status: 'locked:conflict',
    })
    expect(retainedHashes(s)).toEqual([H1])
  })
})

describe('regression sequences — spec §9.4, verbatim', () => {
  it('#1: our own successful delete is agreement, not "deleted elsewhere, pull"', () => {
    let s = synced(5, 'H')
    s = reduceSection(s, { type: 'local-changed', hash: null })
    const d = decideSection(s, ON)
    expect(d.do).toBe('delete')
    expect(tokenOf(d).baseRev).toBe(5)
    s = reduceSection(s, { type: 'push-started', token: tokenOf(d) })
    s = reduceSection(s, { type: 'push-applied', rev: 6 })
    expect(s.base).toEqual({ rev: 6, hash: null })
    expect(isDirty(s)).toBe(false)
    expect(s.status).toBe('synced')
    s = reduceSection(s, { type: 'sot-index', epoch: s.indexEpoch, entry: null })
    expect(decideSection(s, ON)).toEqual({ do: 'nothing' })
  })

  it('#3: a late index response cannot rewind the known SOT rev', () => {
    let s = mk({ base: { rev: 5, hash: H0 }, sot: { rev: 5, hash: H0 }, currentHash: H1 })
    const requestedAt = s.indexEpoch
    s = reduceSection(s, { type: 'remote-event', rev: 8, hash: H8, own: false })
    s = reduceSection(s, { type: 'sot-index', epoch: requestedAt, entry: { rev: 5, hash: H0 } })
    expect(s.sot.rev).toBe(8)
    expect(s.indexStale).toBe(true)
    expect(decideSection(s, ON).do).not.toBe('push')
    expect(decideSection(s, ON)).toMatchObject({ do: 'reindex' })
    s = reduceSection(s, { type: 'sot-index', epoch: s.indexEpoch, entry: { rev: 8, hash: H8 } })
    expect(s.indexStale).toBe(false)
    expect(decideSection(s, ON)).toEqual({ do: 'lock-conflict' })
  })

  it('#4: a push decided before an event arrived dies at push-started', () => {
    const s = run(synced(5, H0), { type: 'local-changed', hash: H1 })
    const token = tokenOf(decideSection(s, ON))
    const s2 = reduceSection(s, { type: 'remote-event', rev: 6, hash: 'h6', own: false })
    const s3 = reduceSection(s2, { type: 'push-started', token })
    expect(s3).toBe(s2)
    expect(s3.inFlight).toBeNull()
  })

  it('#6a: keep-local targets the newest rev learnt while locked, and restores the sent snapshot', () => {
    const [opened, token] = startFlight(run(synced(5, H0), { type: 'local-changed', hash: H1 }))
    expect(token.baseRev).toBe(5)
    let s = opened
    s = reduceSection(s, { type: 'push-conflict', rev: 6, hash: 'h6' })
    expect(s.status).toBe('locked:conflict')
    s = reduceSection(s, { type: 'local-changed', hash: H2 }) // the live stores move on
    s = reduceSection(s, { type: 'remote-event', rev: 7, hash: 'h7', own: false })
    s = reduceSection(s, { type: 'resolved', keep: 'local' })
    expect(s.base.rev).toBe(7)
    expect(s.base).toEqual({ rev: 7, hash: 'h7' })
    expect(decideSection(s, ON)).toEqual({ do: 'restore-local', hash: H1 })
    s = reduceSection(s, { type: 'local-restored', hash: H1 })
    const d = decideSection(s, ON)
    expect(d.do).toBe('push')
    expect(tokenOf(d)).toMatchObject({ kind: 'put', hash: H1, baseRev: 7 })
  })

  it('#6c: keep-local while offline — the restore happens at once, the network steps wait', () => {
    const OFF = { reachable: false, autoSync: false }
    let [s] = startFlight(run(synced(5, H0), { type: 'local-changed', hash: H1 }))
    s = reduceSection(s, { type: 'push-conflict', rev: 6, hash: 'h6' })
    expect(s.status).toBe('locked:conflict')
    s = reduceSection(s, { type: 'remote-event', rev: 7, hash: 'h7', own: false })
    s = reduceSection(s, { type: 'local-changed', hash: H2 }) // the live stores move on
    s = reduceSection(s, { type: 'reconnected' }) // the connection dropped and came back: stale again
    expect(s.indexStale).toBe(true)
    s = reduceSection(s, { type: 'resolved', keep: 'local' })
    expect(s.indexStale).toBe(true)
    expect(s.restoreLocal).toEqual({ hash: H1 })
    expect(decideSection(s, OFF)).toEqual({ do: 'restore-local', hash: H1 })
    expect(decideSection(s, ON)).toEqual({ do: 'restore-local', hash: H1 })
    s = reduceSection(s, { type: 'local-restored', hash: H1 })
    expect(s.currentHash).toBe(H1)
    expect(decideSection(s, OFF)).toEqual({ do: 'nothing' })
    expect(decideSection(s, ON)).toMatchObject({ do: 'reindex' })
    s = reduceSection(s, { type: 'sot-index', epoch: s.indexEpoch, entry: { rev: 7, hash: 'h7' } })
    expect(s.indexStale).toBe(false)
    const d = decideSection(s, ON)
    expect(d.do).toBe('push')
    expect(tokenOf(d)).toMatchObject({ kind: 'put', hash: H1, baseRev: 7 })
    expect(tokenOf(d).baseRev).toBe(s.sot.rev)
  })

  it('#6b: take-SOT pulls even though the section is dirty', () => {
    let [s] = startFlight(run(synced(5, H0), { type: 'local-changed', hash: H1 }))
    s = reduceSection(s, { type: 'push-conflict', rev: 6, hash: 'h6' })
    s = reduceSection(s, { type: 'resolved', keep: 'sot' })
    expect(isDirty(s)).toBe(true)
    expect(decideSection(s, ON)).toEqual({ do: 'pull' })
    s = reduceSection(s, { type: 'pull-applied', rev: 6, hash: 'h6', localHash: 'h6' })
    expect(isDirty(s)).toBe(false)
    expect(s.forcePull).toBe(false)
    expect(s.status).toBe('synced')
    expect(s.currentHash).toBe('h6')
  })

  it('#9: push-converged sets the base to the SENT hash, not the live one', () => {
    let [s] = startFlight(run(synced(5, H0), { type: 'local-changed', hash: H1 }))
    s = reduceSection(s, { type: 'local-changed', hash: H2 })
    s = reduceSection(s, { type: 'push-converged', rev: 9 })
    expect(s.base).toEqual({ rev: 9, hash: H1 })
    expect(s.currentHash).toBe(H2)
    expect(isDirty(s)).toBe(true)
    const d = decideSection(s, ON)
    expect(d.do).toBe('push')
    expect(tokenOf(d)).toMatchObject({ hash: H2, baseRev: 9 })
  })

  it('two machines make the same edit → synced, base advances, no lock', () => {
    const s = run(synced(5, H0), { type: 'local-changed', hash: H2 }, { type: 'remote-event', rev: 6, hash: H2, own: false })
    expect(s.status).toBe('synced')
    expect(s.base).toEqual({ rev: 6, hash: H2 })
    expect(s.conflict).toBeNull()
    expect(decideSection(s, ON)).toEqual({ do: 'nothing' })
  })

  it('we delete and they delete → converge, no lock', () => {
    const s = run(synced(5, H0), { type: 'local-changed', hash: null }, { type: 'remote-event', rev: 6, hash: null, own: false })
    expect(s.status).toBe('synced')
    expect(s.base).toEqual({ rev: 6, hash: null })
    expect(decideSection(s, ON)).toEqual({ do: 'nothing' })
  })

  it('we edit and they delete → lock-conflict; keep-local recreates with baseRev 0', () => {
    let s = run(synced(5, H0), { type: 'local-changed', hash: H2 }, { type: 'remote-event', rev: 6, hash: null, own: false })
    expect(decideSection(s, ON)).toEqual({ do: 'lock-conflict' })
    s = reduceSection(s, { type: 'locked', reason: 'conflict' })
    expect(s.conflict).toEqual({ localHash: H2, sot: { rev: 6, hash: null } })
    s = reduceSection(s, { type: 'resolved', keep: 'local' })
    const d = decideSection(s, ON)
    expect(d.do).toBe('push')
    expect(tokenOf(d)).toMatchObject({ kind: 'put', hash: H2, baseRev: 0 })
  })
})

// ───────────────────────── property tests ─────────────────────────

function mulberry32(seed: number): () => number {
  let a = seed >>> 0
  return () => {
    a = (a + 0x6d2b79f5) >>> 0
    let t = a
    t = Math.imul(t ^ (t >>> 15), t | 1)
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61)
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296
  }
}

const SEQUENCES = 600
const EVENTS_PER_SEQUENCE = 50
const BASE_SEED = 0x5eed_2a

describe('property tests (seeded)', () => {
  it(`${SEQUENCES} sequences × ${EVENTS_PER_SEQUENCE} events hold every invariant`, () => {
    const HASHES: (string | null)[] = ['A', 'B', 'C', null]
    const LIVE = ['A', 'B', 'C']
    const terminalTypes = new Set(['push-applied', 'push-converged', 'push-conflict', 'push-failed'])
    const seen = new Set<string>()
    let flightsOpened = 0
    let locks = 0
    let steps = 0
    let staleDecisions = 0
    let absentRestores = 0
    let staleRestores = 0
    let cancelledRestores = 0
    let localOnlySteps = 0
    let keptLiveSot = 0
    let staleFoldsHeld = 0
    let acceptedRestores = 0
    let invalidLocks = 0
    let invalidRefused = 0
    let invalidWrongRev = 0
    let invalidAutoUnlocks = 0
    let invalidHeld = 0
    let invalidUnlockedNotByRev = 0
    let invalidKeepLocal = 0
    let invalidKeepSotRefused = 0

    for (let n = 0; n < SEQUENCES; n++) {
      const seed = BASE_SEED + n
      const rnd = mulberry32(seed)
      const int = (k: number) => Math.floor(rnd() * k)
      const pick = <T,>(xs: readonly T[]): T => xs[int(xs.length)]
      const log: SectionEvent[] = []
      let s = deepFreeze(initialSectionState(pick(HASHES)))
      let staleToken: FlightToken | null = null
      let indexSeen = false
      let editedSinceRestoreSet = false
      if (!s.indexStale || decideSection(s, ON).do !== 'reindex') throw new Error(`seed=${seed}: a fresh state must reindex first`)

      const gen = (): SectionEvent => {
        const r = int(100)
        if (r < 16) return { type: 'local-changed', hash: pick(HASHES) }
        if (r < 28) {
          const epoch = rnd() < 0.7 ? s.indexEpoch : Math.max(0, s.indexEpoch - 1 - int(3))
          const entry = rnd() < 0.25 ? null : { rev: int(9), hash: pick(LIVE) }
          return { type: 'sot-index', epoch, entry }
        }
        if (r < 42) return { type: 'remote-event', rev: int(10), hash: pick(HASHES), own: rnd() < 0.15 }
        if (r < 60) {
          const d = decideSection(s, ON)
          const fresh = d.do === 'push' || d.do === 'delete' ? d.token : null
          if (fresh !== null && rnd() < 0.75) {
            staleToken = fresh
            return { type: 'push-started', token: fresh }
          }
          if (staleToken !== null && rnd() < 0.6) return { type: 'push-started', token: staleToken }
          const hash = pick(HASHES)
          return { type: 'push-started', token: { kind: hash === null ? 'delete' : 'put', hash, baseRev: int(9), epoch: s.epoch } }
        }
        if (r < 68) return { type: 'push-applied', rev: int(10) }
        if (r < 72) return { type: 'push-converged', rev: 1 + int(9) }
        if (r < 80) return rnd() < 0.3 ? { type: 'push-conflict', rev: 0, hash: null } : { type: 'push-conflict', rev: 1 + int(9), hash: pick(LIVE) }
        if (r < 84) return { type: 'push-failed' }
        if (r < 90) {
          const hash = pick(HASHES)
          return { type: 'pull-applied', rev: int(10), hash, localHash: rnd() < 0.7 ? hash : pick(HASHES) }
        }
        if (r < 93) return { type: 'local-restored', hash: pick(HASHES) }
        if (r < 96) return { type: 'resolved', keep: rnd() < 0.5 ? 'local' : 'sot' }
        if (r < 98) return { type: 'reconnected' }
        if (rnd() < 0.3) return { type: 'locked', reason: 'invalid', rev: rnd() < 0.6 ? s.sot.rev : int(10) }
        return { type: 'locked', reason: rnd() < 0.6 ? 'conflict' : 'reset' }
      }

      const fail = (msg: string): never => {
        throw new Error(`${msg}\nseed=${seed} (sequence ${n})\nevents=${JSON.stringify(log)}\nstate=${JSON.stringify(s)}`)
      }

      for (let i = 0; i < EVENTS_PER_SEQUENCE; i++) {
        // follow the decision sometimes so that locks and restores are actually reached
        let e = gen()
        const d0 = decideSection(s, ON)
        if (rnd() < 0.5) {
          if (d0.do === 'lock-conflict') e = { type: 'locked', reason: 'conflict' }
          else if (d0.do === 'lock-reset') e = { type: 'locked', reason: 'reset' }
          // resolve → (edit) → restore: the user sometimes edits before the driver got to put the snapshot back
          else if (d0.do === 'restore-local') e = rnd() < 0.3 ? { type: 'local-changed', hash: pick(HASHES) } : { type: 'local-restored', hash: d0.hash }
          // the driver fetched the payload and sometimes refuses it — now and then for a rev that is not the one held
          else if (d0.do === 'pull' && rnd() < 0.3) e = { type: 'locked', reason: 'invalid', rev: rnd() < 0.8 ? s.sot.rev : s.sot.rev + 1 }
          else if (d0.do === 'pull') e = { type: 'pull-applied', rev: s.sot.rev, hash: s.sot.hash, localHash: s.sot.hash }
          else if (d0.do === 'reindex') e = { type: 'sot-index', epoch: s.indexEpoch, entry: s.sot.hash === null ? null : { rev: s.sot.rev, hash: s.sot.hash } }
          else if (d0.do === 'push' || d0.do === 'delete') e = { type: 'push-started', token: d0.token }
          else if (s.status !== 'synced' && s.status !== 'pending') e = { type: 'resolved', keep: rnd() < 0.5 ? 'local' : 'sot' }
        }
        steps++
        deepFreeze(e)
        log.push(e)
        const prev = s
        let next: SectionSyncState
        try {
          next = reduceSection(prev, e)
        } catch (err) {
          return fail(`reducer threw on frozen input: ${String(err)}`)
        }
        s = deepFreeze(next)
        seen.add(`${e.type}:${next === prev ? 'ignored' : 'applied'}`)
        if (prev.inFlight === null && next.inFlight !== null) flightsOpened++
        if (!prev.status.startsWith('locked') && next.status.startsWith('locked')) locks++
        if (next.restoreLocal !== null && next.restoreLocal.hash === null) absentRestores++

        // shadow state: has the live payload been edited since the pending restore was set?
        if (prev.restoreLocal === null && next.restoreLocal !== null) editedSinceRestoreSet = false
        if (e.type === 'local-changed' && next.currentHash !== prev.currentHash) {
          editedSinceRestoreSet = true
          if (prev.restoreLocal !== null) cancelledRestores++
        }
        if (next.restoreLocal !== null && editedSinceRestoreSet) fail('a restore is still pending although the user edited after resolving')
        // a restore is accepted only for the very snapshot that is pending
        if (e.type === 'local-restored' && next !== prev) {
          if (prev.restoreLocal === null || prev.restoreLocal.hash !== e.hash) fail('local-restored accepted for a hash that was not pending')
          acceptedRestores++
        }
        if (e.type === 'local-restored' && canRestoreLocal(prev, e.hash) !== (next !== prev)) fail('canRestoreLocal disagrees with the reducer')

        // unchanged ⇒ same reference; changed ⇒ epoch + 1
        if (next !== prev && next.epoch !== prev.epoch + 1) fail('a changed state must bump the epoch by exactly one')
        if (next !== prev && JSON.stringify({ ...next, epoch: 0 }) === JSON.stringify({ ...prev, epoch: 0 })) fail('epoch bumped without any change')
        // sot.rev never decreases except through a sot-index made at the current indexEpoch
        if (next.sot.rev < prev.sot.rev && !(e.type === 'sot-index' && e.epoch === prev.indexEpoch)) fail('sot.rev decreased')
        // indexEpoch is about what is known of the SOT: it moves iff base / sot / the flight moved, or the
        // connection was re-established (EVERY `reconnected`, stale already or not) — never because the user typed (a fold on local-changed moves the base)
        const sotKnowledgeMoved =
          next.base.rev !== prev.base.rev || next.base.hash !== prev.base.hash || next.sot.rev !== prev.sot.rev || next.sot.hash !== prev.sot.hash || next.inFlight !== prev.inFlight
        const wantIndexEpoch = prev.indexEpoch + (sotKnowledgeMoved || e.type === 'reconnected' ? 1 : 0)
        if (next.indexEpoch !== wantIndexEpoch) fail(`indexEpoch ${next.indexEpoch}, expected ${wantIndexEpoch}`)
        if (e.type === 'local-changed' && !sotKnowledgeMoved) {
          if (next.indexEpoch !== prev.indexEpoch) fail('a local edit moved indexEpoch')
          localOnlySteps++
        }
        // a response to the request the current state would send is never discarded
        if (e.type === 'sot-index' && e.epoch === prev.indexEpoch && next.indexStale) fail('an index made at the current indexEpoch was discarded')
        // a locked section never has a flight
        const locked = next.status === 'locked:conflict' || next.status === 'locked:reset' || next.status === 'locked:invalid'
        // locked:invalid ⇔ `invalid` is set (conflict === null, inFlight === null and decide → nothing follow from the `locked` checks)
        if ((next.status === 'locked:invalid') !== (next.invalid !== null)) fail('invalid out of step with status')
        // … and while it is shut, sot IS the refused SOT: any other sot would have opened it
        if (next.invalid !== null && (next.invalid.rev !== next.sot.rev || next.invalid.hash !== next.sot.hash)) fail('locked:invalid although sot is not the refused one')
        // locked{invalid} is accepted iff the state decides pull for exactly the rev held
        if (e.type === 'locked' && e.reason === 'invalid') {
          const called = d0.do === 'pull' && e.rev === prev.sot.rev
          if ((next !== prev) !== called) fail('locked{invalid} acceptance does not follow "decides pull, for sot.rev"')
          if (called && (next.status !== 'locked:invalid' || next.invalid?.rev !== prev.sot.rev || next.invalid?.hash !== prev.sot.hash)) fail('locked{invalid} did not record the sot')
          if (called) invalidLocks++
          else if (d0.do === 'pull') invalidWrongRev++
          else invalidRefused++
        } else if (prev.status !== 'locked:invalid' && next.status === 'locked:invalid') fail('locked:invalid entered without locked{invalid}')
        // an invalid lock opens only through keep:local, or by observing a SOT that differs from `invalid` — and then always
        if (prev.invalid !== null) {
          const sotChanged = next.sot.rev !== prev.invalid.rev || next.sot.hash !== prev.invalid.hash
          if (sotChanged && e.type !== 'remote-event' && e.type !== 'sot-index') fail('sot moved under locked:invalid without an observation')
          const observed = sotChanged
          const keptLocal = e.type === 'resolved' && e.keep === 'local'
          if ((next.status !== 'locked:invalid') !== (observed || keptLocal)) fail('locked:invalid opened (or stayed shut) against the rule')
          if (e.type === 'resolved' && e.keep === 'sot') {
            if (next !== prev) fail('keep:sot accepted on locked:invalid')
            invalidKeepSotRefused++
          }
          if (observed) {
            invalidAutoUnlocks++
            if (next.sot.rev <= prev.invalid.rev) invalidUnlockedNotByRev++
          }
          if (keptLocal) {
            if (next.base.rev !== prev.sot.rev || next.base.hash !== prev.sot.hash) fail('keep:local on locked:invalid did not rebase on the sot')
            invalidKeepLocal++
          }
          // an observation of the very same SOT (an index that lands and confirms it): still shut
          if (!observed && (e.type === 'remote-event' || e.type === 'sot-index')) invalidHeld++
        }
        if (locked && next.inFlight !== null) fail('inFlight set in a locked state')
        if ((next.status === 'locked:conflict') !== (next.conflict !== null)) fail('conflict pair out of step with status')
        if (locked && next.forcePull) fail('forcePull on a locked section')
        if (!locked && next.status !== (isDirty(next) || next.inFlight !== null ? 'pending' : 'synced')) fail('status is not the derived one')
        if (next.sotMovedWhileInFlight && next.inFlight === null) fail('sotMovedWhileInFlight without a flight')
        if (next.conflict !== null && (next.conflict.sot.rev !== next.sot.rev || next.conflict.sot.hash !== next.sot.hash)) fail('conflict.sot fell behind sot')
        // a flight is only ever opened from a state that decided it
        if (prev.inFlight === null && next.inFlight !== null) {
          if (e.type !== 'push-started') fail('a flight opened without push-started')
          if (sotMoved(prev) || next.inFlight.hash !== prev.currentHash) fail('a stale flight was opened')
          const want = prev.sot.hash === null ? 0 : prev.base.rev
          if (next.inFlight.baseRev !== want) fail('flight opened with the wrong wire baseRev')
        }
        // terminal events always close the flight
        if (terminalTypes.has(e.type) && (next.inFlight !== null || next.sotMovedWhileInFlight)) fail('terminal event left a flight open')
        // a 200 sets the base to what was SENT (a later fold may only move it to the sot)
        if ((e.type === 'push-applied' || e.type === 'push-converged') && prev.inFlight !== null) {
          if (next.base.hash !== prev.inFlight.hash && !(next.base.hash === next.sot.hash && next.base.hash === next.currentHash)) fail('base is not the sent hash')
        }
        // convergence is never left unfolded
        const foldable = !locked && next.inFlight === null && isDirty(next) && sotMoved(next) && next.sot.hash === next.currentHash
        if (foldable && !next.indexStale) fail('unfolded convergence')
        if (foldable) staleFoldsHeld++
        // … and never folded on a stale index: there the base moves only through events that SET it
        const baseMoved = next.base.rev !== prev.base.rev || next.base.hash !== prev.base.hash
        if (next.indexStale && baseMoved && !['push-applied', 'push-converged', 'pull-applied', 'resolved'].includes(e.type)) fail('folded on a stale index')
        // an accepted pull sets the base to what was FETCHED and currentHash to what the stores hold afterwards
        if (e.type === 'pull-applied' && next !== prev) {
          if (next.currentHash !== e.localHash) fail('pull-applied: currentHash is not the hash rebuilt from the stores')
          const folded = next.base.hash === next.sot.hash && next.base.hash === next.currentHash
          if ((next.base.rev !== e.rev || next.base.hash !== e.hash) && !folded) fail('pull-applied: base is not the fetched {rev, hash}')
        }
        if (next.restoreLocal !== null && next.restoreLocal.hash === next.currentHash) fail('restoreLocal lingers although already restored')
        if (e.type === 'reconnected' && !next.indexStale) fail('reconnected left the index fresh')
        // a 409 locks iff something unsynced is left: dirty, and not already what the SOT holds
        if (e.type === 'push-conflict' && prev.inFlight !== null) {
          // "absent" against a live SOT learnt while the push was out is ambiguous: sot untouched, no lock, ask the index
          const ambiguous = e.rev === 0 && prev.sotMovedWhileInFlight && prev.sot.hash !== null
          if (ambiguous) {
            if (next.sot.rev !== prev.sot.rev || next.sot.hash !== prev.sot.hash) fail('an ambiguous rev 0 conflict touched the SOT')
            if (next.status === 'locked:conflict' || next.conflict !== null) fail('an ambiguous rev 0 conflict locked')
            if (!next.indexStale) fail('an ambiguous rev 0 conflict did not ask for the index')
            if (next.base.rev !== prev.base.rev || next.base.hash !== prev.base.hash) fail('an ambiguous rev 0 conflict moved the base')
            keptLiveSot++
          }
          const shouldLock = !ambiguous && prev.currentHash !== prev.base.hash && next.sot.hash !== prev.currentHash
          if ((next.status === 'locked:conflict') !== shouldLock) fail('409 lock decision does not follow dirtiness')
          if (shouldLock && next.conflict?.localHash !== prev.inFlight.hash) fail('409 conflict does not hold the sent snapshot')
        }

        const d = decideSection(next, ON)
        if (next.inFlight !== null && d.do !== 'nothing') fail('decided something while in flight')
        if (locked && d.do !== 'nothing') fail('decided something while locked')
        // on a stale index: rows 0a/0b → nothing; else a pending restore (row 0c, local, ungated) goes
        // first; else row 0d → reindex, which shadows every row below it
        // until the first epoch-matching index has landed, the index stays stale (so: no flight, no pull, no lock)
        if (e.type === 'sot-index' && e.epoch === prev.indexEpoch) indexSeen = true
        if (!indexSeen && (!next.indexStale || next.inFlight !== null || locked)) fail('acted before the index was ever seen')
        if (next.indexStale) {
          const restorePending = next.restoreLocal !== null && next.restoreLocal.hash !== next.currentHash
          const blocked = locked || next.inFlight !== null
          const want = blocked ? 'nothing' : restorePending ? 'restore-local' : 'reindex'
          if (d.do !== want) fail(`stale index: decided "${d.do}", expected "${want}"`)
          if (d.do === 'reindex' && d.indexEpoch !== next.indexEpoch) fail('reindex does not carry the current indexEpoch')
          const offline = decideSection(next, { reachable: false, autoSync: false })
          const wantOffline = !blocked && restorePending ? 'restore-local' : 'nothing'
          if (offline.do !== wantOffline) fail(`stale index, offline: decided "${offline.do}", expected "${wantOffline}"`)
          staleDecisions++
          if (want === 'restore-local') staleRestores++
        }
        if (d.do === 'push' || d.do === 'delete') {
          if (sotMoved(next)) fail('push/delete decided although the SOT moved')
          if (d.token.epoch !== next.epoch || d.token.hash !== next.currentHash) fail('token does not describe the state')
          if ((d.do === 'delete') !== (d.token.hash === null) || (d.token.kind === 'delete') !== (d.do === 'delete')) fail('token kind mismatch')
          if (d.do === 'delete' && next.sot.hash === null) fail('delete decided against an absent SOT')
          if (d.token.baseRev !== (next.sot.hash === null ? 0 : next.base.rev)) fail('wrong wire baseRev')
        }
        const off = decideSection(next, { reachable: rnd() < 0.5, autoSync: false })
        if (off.do === 'push' || off.do === 'delete' || off.do === 'pull' || off.do === 'reindex') fail(`network action "${off.do}" with autoSync off`)
        const unreachable = decideSection(next, { reachable: false, autoSync: true })
        if (unreachable.do === 'push' || unreachable.do === 'delete' || unreachable.do === 'pull' || unreachable.do === 'reindex') fail(`network action "${unreachable.do}" while unreachable`)

        const kept = retainedHashes(next)
        if (kept.some((h) => h === null) || new Set(kept).size !== kept.length) fail('retainedHashes has a null or a duplicate')
      }
    }

    // the generator must actually reach the interesting corners, or the properties above prove nothing
    expect(steps).toBe(SEQUENCES * EVENTS_PER_SEQUENCE)
    expect(flightsOpened).toBeGreaterThan(SEQUENCES)
    expect(locks).toBeGreaterThan(SEQUENCES / 4)
    expect(staleDecisions).toBeGreaterThan(SEQUENCES)
    expect(absentRestores).toBeGreaterThan(0)
    expect(staleRestores).toBeGreaterThan(0)
    expect(cancelledRestores).toBeGreaterThan(10)
    expect(localOnlySteps).toBeGreaterThan(SEQUENCES)
    expect(keptLiveSot).toBeGreaterThan(0)
    expect(staleFoldsHeld).toBeGreaterThan(0)
    expect(acceptedRestores).toBeGreaterThan(10)
    expect(invalidLocks).toBeGreaterThan(SEQUENCES / 4)
    expect(invalidRefused).toBeGreaterThan(10)
    expect(invalidWrongRev).toBeGreaterThan(10)
    expect(invalidAutoUnlocks).toBeGreaterThan(10)
    expect(invalidHeld).toBeGreaterThan(10)
    expect(invalidUnlockedNotByRev).toBeGreaterThan(0)
    expect(invalidKeepLocal).toBeGreaterThan(10)
    expect(invalidKeepSotRefused).toBeGreaterThan(10)
    for (const t of ['local-changed', 'sot-index', 'remote-event', 'push-started', 'push-applied', 'push-converged', 'push-conflict', 'push-failed', 'pull-applied', 'local-restored', 'resolved', 'locked', 'reconnected']) {
      expect(seen.has(`${t}:applied`), `${t} was never applied`).toBe(true)
      // `reconnected` always opens a new indexEpoch, so it is never ignored
      if (t !== 'reconnected') expect(seen.has(`${t}:ignored`), `${t} was never ignored`).toBe(true)
    }
  })
})
