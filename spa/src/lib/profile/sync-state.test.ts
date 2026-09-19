// spa/src/lib/profile/sync-state.test.ts — the per-section sync state machine
// (P2a plan Task 4). Sections: decision table (one test per row), reducer
// rules 1–8, the terminal-event invariant table, the plan-review regression
// sequences (spec §9.4), and seeded property tests.
import { describe, expect, it } from 'vitest'
import {
  canApplyPull,
  decideSection,
  initialSectionState,
  isDirty,
  reduceSection,
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
const H8 = 'h8'

/** Build a state directly (for decision-table rows that the reducer would never leave behind). */
function mk(p: Partial<SectionSyncState>): SectionSyncState {
  const s = { ...initialSectionState(null), ...p }
  if (p.status === undefined) s.status = isDirty(s) || s.inFlight !== null ? 'pending' : 'synced'
  return s
}
function synced(rev: number, hash: string | null): SectionSyncState {
  return mk({ base: { rev, hash }, sot: { rev, hash }, currentHash: hash })
}
function run(s: SectionSyncState, ...events: SectionEvent[]): SectionSyncState {
  return events.reduce(reduceSection, s)
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
      status: 'synced',
      inFlight: null,
      sotMovedWhileInFlight: false,
      conflict: null,
      forcePull: false,
      restoreLocal: null,
      indexStale: false,
    })
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
  it('0c: indexStale → reindex, ahead of every sync row', () => {
    expect(decideSection(mk({ ...dirtyPushable, indexStale: true }), ON)).toEqual({ do: 'reindex' })
  })
  it('0d: restoreLocal differs from currentHash → restore-local', () => {
    const s = mk({ ...dirtyPushable, restoreLocal: H2 })
    expect(decideSection(s, ON)).toEqual({ do: 'restore-local', hash: H2 })
  })
  it('0d is a local action: not gated by reachable/autoSync', () => {
    const s = mk({ ...dirtyPushable, restoreLocal: H2 })
    expect(decideSection(s, { reachable: false, autoSync: false })).toEqual({ do: 'restore-local', hash: H2 })
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

  describe('!reachable || !autoSync turns 0c / 0e / 3 / 4 / 5 into nothing', () => {
    const rows: [string, SectionSyncState][] = [
      ['0c', mk({ ...dirtyPushable, indexStale: true })],
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
    it('0c first-match-wins: a stale index that cannot be refreshed does not fall through to push', () => {
      const s = mk({ ...dirtyPushable, indexStale: true })
      expect(decideSection(s, { reachable: false, autoSync: true })).toEqual({ do: 'nothing' })
    })
  })
})

describe('wire baseRev', () => {
  it('create over nothing sends baseRev 0', () => {
    expect(tokenOf(decideSection(initialSectionState(H1), ON))).toEqual({ kind: 'put', hash: H1, baseRev: 0, epoch: 0 })
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
      { type: 'pull-applied', rev: 6, hash: H1 },
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
    const s = mk({ ...synced(5, H0), epoch: 4 })
    const s2 = reduceSection(s, { type: 'sot-index', epoch: 3, entry: { rev: 9, hash: H8 } })
    expect(s2.sot).toEqual({ rev: 5, hash: H0 })
    expect(s2.indexStale).toBe(true)
    // a second stale response changes nothing → same reference
    expect(reduceSection(s2, { type: 'sot-index', epoch: 3, entry: null })).toBe(s2)
  })
  it('matching epoch is authoritative, even when it lowers sot.rev (the only road to lock-reset)', () => {
    const s = synced(5, H0)
    const s2 = reduceSection(s, { type: 'sot-index', epoch: s.epoch, entry: { rev: 2, hash: H8 } })
    expect(s2.sot).toEqual({ rev: 2, hash: H8 })
    expect(decideSection(s2, ON)).toEqual({ do: 'lock-reset' })
  })
  it('matching epoch, not listed → sot = {max(sot.rev, base.rev), null}', () => {
    const s = mk({ base: { rev: 5, hash: H0 }, sot: { rev: 7, hash: H1 }, currentHash: H0 })
    expect(reduceSection(s, { type: 'sot-index', epoch: s.epoch, entry: null }).sot).toEqual({ rev: 7, hash: null })
    const t = mk({ base: { rev: 5, hash: H0 }, sot: { rev: 2, hash: H1 }, currentHash: H0 })
    expect(reduceSection(t, { type: 'sot-index', epoch: t.epoch, entry: null }).sot).toEqual({ rev: 5, hash: null })
  })
  it('matching epoch clears indexStale', () => {
    const s = mk({ ...synced(5, H0), indexStale: true })
    const s2 = reduceSection(s, { type: 'sot-index', epoch: s.epoch, entry: { rev: 5, hash: H0 } })
    expect(s2.indexStale).toBe(false)
    expect(s2.epoch).toBe(s.epoch + 1)
  })
  it('an index that confirms what we know returns the same reference', () => {
    const s = synced(5, H0)
    expect(reduceSection(s, { type: 'sot-index', epoch: s.epoch, entry: { rev: 5, hash: H0 } })).toBe(s)
  })
  it('while locked:conflict it keeps conflict.sot in step', () => {
    const [s] = startFlight(run(synced(5, H0), { type: 'local-changed', hash: H1 }))
    const locked = reduceSection(s, { type: 'push-conflict', rev: 6, hash: H2 })
    const s2 = reduceSection(locked, { type: 'sot-index', epoch: locked.epoch, entry: { rev: 9, hash: H8 } })
    expect(s2.conflict?.sot).toEqual({ rev: 9, hash: H8 })
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
  it('push-failed → back to dirty/pending, nothing else changes', () => {
    const before = inFlightPut()
    const s = reduceSection(before, { type: 'push-failed' })
    expect(s).toEqual({ ...before, inFlight: null, epoch: before.epoch + 1 })
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
    const s = run(synced(5, H0), { type: 'remote-event', rev: 6, hash: H1, own: false }, { type: 'pull-applied', rev: 6, hash: H1 })
    expect(s.base).toEqual({ rev: 6, hash: H1 })
    expect(s.sot).toEqual({ rev: 6, hash: H1 })
    expect(s.currentHash).toBe(H1)
    expect(s.status).toBe('synced')
  })
  it('a pull of an absent SOT applies a deletion', () => {
    const s = run(synced(5, H0), { type: 'remote-event', rev: 6, hash: null, own: false }, { type: 'pull-applied', rev: 6, hash: null })
    expect(s.base).toEqual({ rev: 6, hash: null })
    expect(s.currentHash).toBeNull()
    expect(decideSection(s, ON)).toEqual({ do: 'nothing' })
  })
  it('a pull older than the known sot sets the base but not the sot → pulls again', () => {
    const s = run(
      synced(5, H0),
      { type: 'remote-event', rev: 6, hash: H1, own: false },
      { type: 'remote-event', rev: 7, hash: H2, own: false },
      { type: 'pull-applied', rev: 6, hash: H1 },
    )
    expect(s.base).toEqual({ rev: 6, hash: H1 })
    expect(s.sot).toEqual({ rev: 7, hash: H2 })
    expect(decideSection(s, ON)).toEqual({ do: 'pull' })
  })
  it('is refused on a dirty section without forcePull (§4.6.2 rule 1) — canApplyPull says so first', () => {
    const s = run(synced(5, H0), { type: 'remote-event', rev: 6, hash: H1, own: false }, { type: 'local-changed', hash: H2 })
    expect(canApplyPull(s)).toBe(false)
    expect(reduceSection(s, { type: 'pull-applied', rev: 6, hash: H1 })).toBe(s)
  })
  it('is refused while a flight is open', () => {
    const [s] = startFlight(run(synced(5, H0), { type: 'local-changed', hash: H1 }))
    expect(canApplyPull(s)).toBe(false)
    expect(reduceSection(s, { type: 'pull-applied', rev: 6, hash: H2 })).toBe(s)
  })
  it('is refused while locked (a locked section refuses inbound)', () => {
    const [s] = startFlight(run(synced(5, H0), { type: 'local-changed', hash: H1 }))
    const locked = reduceSection(s, { type: 'push-conflict', rev: 6, hash: H2 })
    expect(canApplyPull(locked)).toBe(false)
    expect(reduceSection(locked, { type: 'pull-applied', rev: 6, hash: H2 })).toBe(locked)
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
    const s = reduceSection(s0, { type: 'sot-index', epoch: s0.epoch, entry: { rev: 2, hash: H8 } })
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
    expect(s.restoreLocal).toBe(H1)
    expect(decideSection(s, { reachable: false, autoSync: false })).toEqual({ do: 'restore-local', hash: H1 })
  })
  it('restoreLocal is dropped as soon as the live hash already is the snapshot (nothing to put back)', () => {
    const s = run(conflicted(), { type: 'locked', reason: 'conflict' }, { type: 'resolved', keep: 'local' })
    expect(s.restoreLocal).toBeNull()
    expect(tokenOf(decideSection(s, ON))).toMatchObject({ hash: H1, baseRev: 6 })
  })
  it('local-restored sets currentHash and clears restoreLocal; without a pending restore it is ignored', () => {
    const [f] = startFlight(run(synced(5, H0), { type: 'local-changed', hash: H1 }))
    const s = run(f, { type: 'local-changed', hash: H2 }, { type: 'push-conflict', rev: 6, hash: H8 }, { type: 'resolved', keep: 'local' })
    const r = reduceSection(s, { type: 'local-restored', hash: H1 })
    expect(r.currentHash).toBe(H1)
    expect(r.restoreLocal).toBeNull()
    expect(reduceSection(r, { type: 'local-restored', hash: H2 })).toBe(r)
  })
  it('locked:reset keeps learning; keep:local rebases on the newest sot and pushes', () => {
    const s0 = run(synced(5, H0), { type: 'local-changed', hash: H1 })
    const l = run(
      s0,
      { type: 'sot-index', epoch: s0.epoch, entry: { rev: 2, hash: H8 } },
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
    const l = run(s0, { type: 'sot-index', epoch: s0.epoch, entry: { rev: 2, hash: H8 } }, { type: 'locked', reason: 'reset' }, { type: 'resolved', keep: 'sot' })
    expect(decideSection(l, ON)).toEqual({ do: 'pull' })
    const p = reduceSection(l, { type: 'pull-applied', rev: 2, hash: H8 })
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
      restoreLocal: H1,
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
    s = reduceSection(s, { type: 'sot-index', epoch: s.epoch, entry: null })
    expect(decideSection(s, ON)).toEqual({ do: 'nothing' })
  })

  it('#3: a late index response cannot rewind the known SOT rev', () => {
    let s = mk({ base: { rev: 5, hash: H0 }, sot: { rev: 5, hash: H0 }, currentHash: H1 })
    const requestedAt = s.epoch
    s = reduceSection(s, { type: 'remote-event', rev: 8, hash: H8, own: false })
    s = reduceSection(s, { type: 'sot-index', epoch: requestedAt, entry: { rev: 5, hash: H0 } })
    expect(s.sot.rev).toBe(8)
    expect(s.indexStale).toBe(true)
    expect(decideSection(s, ON).do).not.toBe('push')
    expect(decideSection(s, ON)).toEqual({ do: 'reindex' })
    s = reduceSection(s, { type: 'sot-index', epoch: s.epoch, entry: { rev: 8, hash: H8 } })
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

  it('#6b: take-SOT pulls even though the section is dirty', () => {
    let [s] = startFlight(run(synced(5, H0), { type: 'local-changed', hash: H1 }))
    s = reduceSection(s, { type: 'push-conflict', rev: 6, hash: 'h6' })
    s = reduceSection(s, { type: 'resolved', keep: 'sot' })
    expect(isDirty(s)).toBe(true)
    expect(decideSection(s, ON)).toEqual({ do: 'pull' })
    s = reduceSection(s, { type: 'pull-applied', rev: 6, hash: 'h6' })
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

    for (let n = 0; n < SEQUENCES; n++) {
      const seed = BASE_SEED + n
      const rnd = mulberry32(seed)
      const int = (k: number) => Math.floor(rnd() * k)
      const pick = <T,>(xs: readonly T[]): T => xs[int(xs.length)]
      const log: SectionEvent[] = []
      let s = deepFreeze(initialSectionState(pick(HASHES)))
      let staleToken: FlightToken | null = null

      const gen = (): SectionEvent => {
        const r = int(100)
        if (r < 16) return { type: 'local-changed', hash: pick(HASHES) }
        if (r < 28) {
          const epoch = rnd() < 0.7 ? s.epoch : Math.max(0, s.epoch - 1 - int(3))
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
        if (r < 90) return { type: 'pull-applied', rev: int(10), hash: pick(HASHES) }
        if (r < 93) return { type: 'local-restored', hash: pick(HASHES) }
        if (r < 97) return { type: 'resolved', keep: rnd() < 0.5 ? 'local' : 'sot' }
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
          else if (d0.do === 'restore-local') e = { type: 'local-restored', hash: d0.hash }
          else if (d0.do === 'pull') e = { type: 'pull-applied', rev: s.sot.rev, hash: s.sot.hash }
          else if (d0.do === 'reindex') e = { type: 'sot-index', epoch: s.epoch, entry: s.sot.hash === null ? null : { rev: s.sot.rev, hash: s.sot.hash } }
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

        // unchanged ⇒ same reference; changed ⇒ epoch + 1
        if (next !== prev && next.epoch !== prev.epoch + 1) fail('a changed state must bump the epoch by exactly one')
        if (next !== prev && JSON.stringify({ ...next, epoch: 0 }) === JSON.stringify({ ...prev, epoch: 0 })) fail('epoch bumped without any change')
        // sot.rev never decreases except through an epoch-matching sot-index
        if (next.sot.rev < prev.sot.rev && !(e.type === 'sot-index' && e.epoch === prev.epoch)) fail('sot.rev decreased')
        // a locked section never has a flight
        const locked = next.status === 'locked:conflict' || next.status === 'locked:reset'
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
        if (!locked && next.inFlight === null && isDirty(next) && sotMoved(next) && next.sot.hash === next.currentHash) fail('unfolded convergence')
        if (next.restoreLocal !== null && next.restoreLocal === next.currentHash) fail('restoreLocal lingers although already restored')

        const d = decideSection(next, ON)
        if (next.inFlight !== null && d.do !== 'nothing') fail('decided something while in flight')
        if (locked && d.do !== 'nothing') fail('decided something while locked')
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
    for (const t of ['local-changed', 'sot-index', 'remote-event', 'push-started', 'push-applied', 'push-converged', 'push-conflict', 'push-failed', 'pull-applied', 'local-restored', 'resolved', 'locked']) {
      expect(seen.has(`${t}:applied`), `${t} was never applied`).toBe(true)
      expect(seen.has(`${t}:ignored`), `${t} was never ignored`).toBe(true)
    }
  })
})
