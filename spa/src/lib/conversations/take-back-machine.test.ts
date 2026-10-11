// #2682: the take-back's pure state machine — the generation of the take-back in flight, the optimistic hide of header rows by (text, at) instance, and
// which App entries stand for the rows an answer brought back. take-back.test.ts pins the coordinator (timers, draft, queue); these are the races as tables.
import { describe, it, expect } from 'vitest'
import {
  answerMatches, begin, dismissNotice, entriesToDismiss, expireHide, fail, headerCaughtUp, hideAfter, HIDE_MS, IDLE_CORE, isCurrent, isIdle, isRowHidden, land,
  settle, snapshotOf, visibleRows, type TakeBackCore,
} from './take-back-machine'
import type { HeaderRow } from './send-queue'

const row = (text: string, at: number): HeaderRow => ({ text, at })
const notice = { key: 'deck.recall.none', tone: 'warn' } as never

/** A core that is on its way under `gen`. */
const running = (gen: number): TakeBackCore => begin(IDLE_CORE, gen)!

describe('the generation', () => {
  it('a take-back starts when none is on its way; it is busy, clears the last notice and is current', () => {
    const c = begin({ state: { busy: false, notice, hide: null }, op: null }, 1)!
    expect(c.state).toEqual({ busy: true, notice: null, hide: null })
    expect(isCurrent(c, 1)).toBe(true)
  })

  it('further Up / clicks are ignored while one is on its way', () => {
    expect(begin(running(1), 2)).toBeNull()
  })

  it('a failed answer ends it with the notice; a stale one (the key was let go, or another started) applies nothing', () => {
    const failed = fail(running(1), 1, notice)!
    expect(failed.state).toEqual({ busy: false, notice, hide: null })
    expect(failed.op).toBeNull()
    expect(fail(IDLE_CORE, 1, notice)).toBeNull() // let go while the daemon answered
    expect(fail(running(2), 1, notice)).toBeNull() // another has started since
  })

  it('a landed answer is no longer in flight but the take-back is busy until the rows are settled; a stale one applies nothing', () => {
    const landed = land(running(1), 1)!
    expect(landed.op).toBeNull()
    expect(landed.state.busy).toBe(true)
    expect(land(landed, 1)).toBeNull() // the same answer twice
    expect(land(IDLE_CORE, 1)).toBeNull()
    expect(land(running(2), 1)).toBeNull()
  })

  it('a landed answer ends the hide of an earlier take-back (its rows were just taken back again); with none, the state object is the same', () => {
    const hide = { rows: [row('a', 1)], at: 5 }
    const c = begin({ state: { busy: false, notice: null, hide }, op: null }, 1)!
    expect(land(c, 1)!.state.hide).toBeNull()
    const plain = running(1)
    expect(land(plain, 1)!.state).toBe(plain.state)
  })

  it('settling ends the busy state and leaves the hide (or none)', () => {
    const hide = { rows: [row('a', 1)], at: 5 }
    const done = settle(land(running(1), 1)!, hide)
    expect(done.state).toEqual({ busy: false, notice: null, hide })
    expect(settle(land(running(1), 1)!, null).state).toEqual({ busy: false, notice: null, hide: null })
  })

  it('a generation is never current again once it ended: a later take-back under a new one does not revive it', () => {
    const first = running(1)
    const failed = fail(first, 1, notice)!
    const second = begin(failed, 2)!
    expect(isCurrent(second, 1)).toBe(false)
    expect(isCurrent(second, 2)).toBe(true)
    expect(land(second, 1)).toBeNull()
  })

  it('an idle core is not kept; one with a notice, a hide or a take-back in flight is', () => {
    expect(isIdle(IDLE_CORE)).toBe(true)
    expect(isIdle(running(1))).toBe(false)
    expect(isIdle({ state: { busy: false, notice, hide: null }, op: null })).toBe(false)
    expect(isIdle({ state: { busy: false, notice: null, hide: { rows: [], at: 1 } }, op: null })).toBe(false)
    expect(isIdle(dismissNotice({ state: { busy: false, notice, hide: null }, op: null }))).toBe(true)
  })

  it('dismissing a notice that is not there changes nothing (same object)', () => {
    expect(dismissNotice(IDLE_CORE)).toBe(IDLE_CORE)
  })
})

describe('the hide, by (text, at) instance', () => {
  const state = (rows: HeaderRow[], at = 1000) => ({ busy: false, notice: null, hide: { rows, at } })

  it('hides exactly the instances that came back: the same text at another time is another line', () => {
    const s = state([row('fix it', 10)])
    expect(isRowHidden(s, row('fix it', 10), 1001)).toBe(true)
    expect(isRowHidden(s, row('fix it', 11), 1001)).toBe(false)
    expect(isRowHidden(s, row('other', 10), 1001)).toBe(false)
  })

  it('compares the text with its trailing whitespace trimmed', () => {
    expect(isRowHidden(state([row('fix it', 10)]), row('fix it  \n', 10), 1001)).toBe(true)
  })

  it('stops hiding after HIDE_MS, whatever the header says', () => {
    const s = state([row('a', 1)], 1000)
    expect(isRowHidden(s, row('a', 1), 1000 + HIDE_MS - 1)).toBe(true)
    expect(isRowHidden(s, row('a', 1), 1000 + HIDE_MS)).toBe(false)
  })

  it('nothing hidden without a hide', () => {
    expect(isRowHidden({ busy: false, notice: null, hide: null }, row('a', 1), 0)).toBe(false)
  })

  it('counts the rows still shown', () => {
    const s = state([row('a', 1)])
    expect(visibleRows(s, [row('a', 1), row('a', 2), row('b', 1)], 1001)).toBe(2)
  })

  it('the header caught up when it lists none of the instances that came back — one that is still listed keeps the hide', () => {
    const s = state([row('a', 1), row('b', 2)])
    expect(headerCaughtUp(s, [row('a', 1)])).toBe(false)
    expect(headerCaughtUp(s, [row('b', 2)])).toBe(false)
    expect(headerCaughtUp(s, [row('a', 9), row('c', 2)])).toBe(true) // same texts at other times are other lines
    expect(headerCaughtUp(s, [])).toBe(true)
    expect(headerCaughtUp({ busy: false, notice: null, hide: null }, [])).toBe(false) // nothing to end
  })

  it('the timer ends the hide it was set for only: a newer hide (set since) has its own timer', () => {
    const c: TakeBackCore = { state: state([row('a', 1)], 2000), op: null }
    expect(expireHide(c, 1000)).toBe(c)
    expect(expireHide(c, 2000).state.hide).toBeNull()
    expect(expireHide(IDLE_CORE, 1)).toBe(IDLE_CORE)
  })

  it('the hide an answer leaves is the snapshot\'s rows, or none when there were none', () => {
    const q = snapshotOf([row('a', 1)], [])
    expect(hideAfter(q, 7)).toEqual({ rows: [{ at: 1, text: 'a' }], at: 7 })
    expect(hideAfter(snapshotOf([], []), 7)).toBeNull()
  })
})

describe('which lines an answer stands for', () => {
  const entry = (id: string, text: string, over: Record<string, unknown> = {}) => ({ id, text, state: 'sent' as const, mode: 'queued' as never, claimAt: undefined as number | undefined, ...over })

  it('the snapshot is the header rows (trimmed) then the handed entries no row stands for; the daemon\'s text is those lines joined', () => {
    const q = snapshotOf([row('one  ', 1), row('two', 2)], [entry('e1', 'three \n'), entry('e2', 'claimed', { claimAt: 2 }), entry('e3', 'mine', { state: 'maybe' }), entry('e4', 'bare', { mode: undefined })])
    expect(q.snapshot).toEqual([{ at: 1, text: 'one' }, { at: 2, text: 'two' }])
    expect(q.loose).toEqual([{ id: 'e1', text: 'three' }])
    expect(q.expected).toBe('one\ntwo\nthree')
  })

  it('an answer matches only when it is exactly the queue as it was snapshotted; an empty queue matches nothing', () => {
    const q = snapshotOf([row('a', 1), row('b', 2)], [])
    expect(answerMatches(q, 'a\nb')).toBe(true)
    expect(answerMatches(q, 'a')).toBe(false)
    expect(answerMatches(q, 'a\nb\nc')).toBe(false)
    expect(answerMatches(snapshotOf([], []), '')).toBe(false)
  })

  it('dismisses the entries that held a claim on a row of the snapshot, one to one, and the ones that stood alone', () => {
    const entries = [entry('c1', 'a', { claimAt: 1 }), entry('c2', 'b', { claimAt: 2 }), entry('l1', 'loose')]
    const q = snapshotOf([row('a', 1), row('b', 2)], entries)
    expect(entriesToDismiss(q, entries)).toEqual(['c1', 'c2', 'l1'])
  })

  it('two entries claiming the same row: only one stands for it (one to one); the other is no part of this answer', () => {
    const entries = [entry('c1', 'a', { claimAt: 1 }), entry('c2', 'a', { claimAt: 1 })]
    const q = snapshotOf([row('a', 1)], entries)
    expect(entriesToDismiss(q, entries)).toEqual(['c1'])
  })

  it('an entry claiming a row that is not in the snapshot (it arrived since), or one that is not on Claude Code\'s queue, is left alone', () => {
    const snap = snapshotOf([row('a', 1)], [])
    const entries = [entry('late', 'z', { claimAt: 9 }), entry('echoed', 'a', { state: 'maybe', claimAt: 1 }), entry('plain', 'a', { mode: undefined })]
    expect(entriesToDismiss(snap, entries)).toEqual([])
  })

  it('a claim whose text no longer matches the row (edited text) does not stand for it', () => {
    const entries = [entry('c1', 'other', { claimAt: 1 })]
    expect(entriesToDismiss(snapshotOf([row('a', 1)], []), entries)).toEqual([])
  })
})
