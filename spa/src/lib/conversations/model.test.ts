import { describe, it, expect } from 'vitest'
import {
  applyApprovalOp, applyApprovals, applyAround, applyCapabilities, applyChanges, applyHeader, applyOlderPage, applySnapshot,
  emptyDoc, firstIndex, hasItem,
} from './model'
import type { AgentTextItem, ConversationItem, Header, Snapshot, Turn, UserItem } from './types'
import askQuestion from '../../../../testdata/conversation/v1/cc-transcript/ask-question/expected.json'
import subagent from '../../../../testdata/conversation/v1/cc-transcript/subagent/expected.json'

const header = (over: Partial<Header> = {}): Header => ({ title: 't', status: 'idle', backend: 'terminal', live: true, ...over })
const user = (id: string, index: number, text = id): UserItem => ({ type: 'user', id, at: 1, index, text, source: 'user' })
const agent = (id: string, index: number, markdown = id): AgentTextItem => ({ type: 'agent_text', id, at: 1, index, markdown })
const turn = (idx: number, items: ConversationItem[], over: Partial<Turn> = {}): Turn =>
  ({ id: `t${idx}`, index: idx, started_at: idx, outcome: 'done', items, ...over })
const snap = (turns: Turn[], over: { hasMore?: boolean; total?: number; cursor?: string; reset?: boolean } = {}): Snapshot => ({
  reset: over.reset,
  conversation: {
    key: { host_id: 'h', provider: 'claude', session_id: 's' }, backend: 'terminal', provider: 'claude', title: 't', status: 'idle',
    capabilities: { source: 'transcript' }, turns,
  },
  header: header(),
  window: {
    first_index: turns[0]?.index ?? 0, last_index: turns[turns.length - 1]?.index ?? 0,
    total_turns: over.total ?? (turns[turns.length - 1]?.index ?? -1) + 1, has_more_before: over.hasMore ?? false,
  },
  cursor: over.cursor ?? 'e:1',
})
const inc = (changes: Array<{ turn: Turn; items: ConversationItem[] }>, cursor = 'e:2') => ({
  changes: changes.map((c) => ({ turn: { ...c.turn, items: undefined as never }, items: c.items })), header: header({ status: 'running' }), cursor,
})
const ids = (d: { turns: Turn[] }) => d.turns.map((t) => t.items.map((i) => i.id))

describe('applySnapshot', () => {
  it('replaces the document and takes header, capabilities, cursor and window', () => {
    const d = applySnapshot(emptyDoc(), snap([turn(0, [user('u0', 0), agent('a0', 1)])], { cursor: 'e:7', hasMore: true, total: 9 }))
    expect(ids(d)).toEqual([['u0', 'a0']])
    expect(d.cursor).toBe('e:7')
    expect(d.hasMoreBefore).toBe(true)
    expect(d.totalTurns).toBe(9)
    expect(d.capabilities).toEqual({ source: 'transcript' })
    expect(d.detached).toBe(false)
  })

  it('a later snapshot (reset) replaces the turns but keeps the approvals', () => {
    let d = applySnapshot(emptyDoc(), snap([turn(0, [user('u0', 0)])]))
    d = applyApprovals(d, [{ id: 'ap1' }])
    d = applySnapshot(d, snap([turn(5, [user('u5', 0)])], { reset: true, cursor: 'f:1' }))
    expect(d.turns.map((t) => t.index)).toEqual([5])
    expect(d.approvals).toEqual([{ id: 'ap1' }])
    expect(d.cursor).toBe('f:1')
  })

  it('puts items in index order even if they arrive out of it', () => {
    const d = applySnapshot(emptyDoc(), snap([turn(0, [agent('a', 1), user('u', 0)])]))
    expect(ids(d)).toEqual([['u', 'a']])
  })
})

describe('applyChanges — upsert by id, place by index', () => {
  const base = () => applySnapshot(emptyDoc(), snap([turn(0, [user('u0', 0), agent('a0', 1, 'hel')])], { cursor: 'e:1' }))

  it('updates an item in place and moves the cursor and header', () => {
    const d = applyChanges(base(), inc([{ turn: turn(0, []), items: [agent('a0', 1, 'hello')] }]))
    expect((d.turns[0].items[1] as AgentTextItem).markdown).toBe('hello')
    expect(ids(d)).toEqual([['u0', 'a0']])
    expect(d.cursor).toBe('e:2')
    expect(d.header?.status).toBe('running')
  })

  it('applying the same change twice is the same document', () => {
    const c = inc([{ turn: turn(0, []), items: [agent('a0', 1, 'hello'), agent('a1', 2)] }])
    const once = applyChanges(base(), c)
    expect(applyChanges(once, c).turns).toEqual(once.turns)
  })

  it('places a new item by its index, not by arrival', () => {
    const d0 = applyChanges(base(), inc([{ turn: turn(0, []), items: [agent('a3', 3)] }]))
    const d1 = applyChanges(d0, inc([{ turn: turn(0, []), items: [agent('a2', 2)] }], 'e:3'))
    expect(ids(d1)).toEqual([['u0', 'a0', 'a2', 'a3']])
  })

  it('appends a new turn after the last', () => {
    const d = applyChanges(base(), inc([{ turn: turn(1, [], { outcome: 'running' }), items: [user('u1', 0)] }]))
    expect(d.turns.map((t) => [t.index, t.outcome])).toEqual([[0, 'done'], [1, 'running']])
    expect(d.totalTurns).toBe(2)
  })

  // the incoming header is the turn's full current state: a field it no longer has must not survive from the old one
  it('a reopened turn loses its old ended_at / duration_ms, and a cleared error goes', () => {
    const closed = applyChanges(base(), inc([{
      turn: turn(1, [], { outcome: 'failed', ended_at: 9, duration_ms: 8, error: { kind: 'api', message: 'boom' } }), items: [user('u1', 0)],
    }]))
    expect(closed.turns[1]).toMatchObject({ ended_at: 9, duration_ms: 8, error: { kind: 'api' } })
    const reopened = applyChanges(closed, inc([{ turn: turn(1, [], { outcome: 'running' }), items: [] }], 'e:3'))
    expect(reopened.turns[1].outcome).toBe('running')
    expect(reopened.turns[1].ended_at).toBeUndefined()
    expect(reopened.turns[1].duration_ms).toBeUndefined()
    expect(reopened.turns[1].error).toBeUndefined()
    expect(ids(reopened)[1]).toEqual(['u1'])
  })

  it('updates the turn header (outcome) by id', () => {
    const running = applyChanges(base(), inc([{ turn: turn(1, [], { outcome: 'running' }), items: [user('u1', 0)] }]))
    const done = applyChanges(running, inc([{ turn: turn(1, [], { outcome: 'done', ended_at: 9 }), items: [] }], 'e:3'))
    expect(done.turns[1].outcome).toBe('done')
    expect(done.turns[1].ended_at).toBe(9)
    expect(ids(done)[1]).toEqual(['u1'])
  })

  // §8.2: a change for a turn older than the loaded first_index may be ignored (it comes again when paging back)
  it('ignores a change for a turn older than the first one held', () => {
    const d0 = applySnapshot(emptyDoc(), snap([turn(5, [user('u5', 0)])], { hasMore: true }))
    const d1 = applyChanges(d0, inc([{ turn: turn(2, []), items: [agent('a2', 1)] }]))
    expect(d1.turns.map((t) => t.index)).toEqual([5])
    expect(d1.cursor).toBe('e:2') // the cursor still moves: the frame was applied, this part of it just was not wanted
  })

  describe('omitted_items', () => {
    const omitted = () => applySnapshot(emptyDoc(), snap([turn(0, [agent('a3', 3), agent('a4', 4)], { omitted_items: 3 })]))

    it('skips an item below the omitted boundary (it stays counted, not shown)', () => {
      const d = applyChanges(omitted(), inc([{ turn: turn(0, [], { omitted_items: 3 }), items: [agent('a1', 1, 'late update of an omitted item'), agent('a4', 4, 'x')] }]))
      expect(ids(d)).toEqual([['a3', 'a4']])
      expect(d.turns[0].omitted_items).toBe(3)
    })

    // the increment's turn header carries no omitted_items (a view-only field): the boundary the snapshot gave still holds
    it('keeps the boundary when a later change does not repeat omitted_items', () => {
      const d = applyChanges(omitted(), inc([{ turn: turn(0, []), items: [agent('a1', 1, 'update of an omitted item'), agent('a4', 4, 'x')] }]))
      expect(ids(d)).toEqual([['a3', 'a4']])
      expect(d.turns[0].omitted_items).toBe(3)
    })

    it('still places items at or above the boundary', () => {
      const d = applyChanges(omitted(), inc([{ turn: turn(0, [], { omitted_items: 3 }), items: [agent('a5', 5)] }]))
      expect(ids(d)).toEqual([['a3', 'a4', 'a5']])
    })
  })

  it('a detached window (a jump) does not merge live changes but keeps the live cursor', () => {
    const live = applySnapshot(emptyDoc(), snap([turn(8, [user('u8', 0)]), turn(9, [user('u9', 0)])], { cursor: 'e:5', total: 10 }))
    const jumped = applyAround(live, snap([turn(2, [user('u2', 0)]), turn(3, [user('u3', 0)])], { hasMore: true, total: 10, cursor: 'IGNORED' }), live.generation)
    expect(jumped.detached).toBe(true)
    expect(jumped.cursor).toBe('e:5')
    const d = applyChanges(jumped, inc([{ turn: turn(9, []), items: [agent('a9', 1)] }], 'e:6'))
    expect(d.turns.map((t) => t.index)).toEqual([2, 3])
    expect(d.cursor).toBe('e:6')
    // a snapshot brings the live position back
    expect(applySnapshot(d, snap([turn(9, [user('u9', 0)])], { total: 10, cursor: 'e:7' })).detached).toBe(false)
  })
})

describe('applyOlderPage', () => {
  it('goes in front, keeps the live cursor, and takes has_more_before from the page', () => {
    const live = applySnapshot(emptyDoc(), snap([turn(5, [user('u5', 0)]), turn(6, [user('u6', 0)])], { hasMore: true, cursor: 'e:9', total: 7 }))
    const d = applyOlderPage(live, snap([turn(3, [user('u3', 0)]), turn(4, [user('u4', 0)])], { hasMore: false, cursor: 'PAGE', total: 7 }), live.generation)
    expect(d.turns.map((t) => t.index)).toEqual([3, 4, 5, 6])
    expect(d.cursor).toBe('e:9')
    expect(d.hasMoreBefore).toBe(false)
    expect(firstIndex(d)).toBe(3)
  })

  it('does not duplicate a turn the document already holds', () => {
    const live = applySnapshot(emptyDoc(), snap([turn(5, [user('u5', 0)])], { hasMore: true }))
    const d = applyOlderPage(live, snap([turn(4, [user('u4', 0)]), turn(5, [user('u5', 0)])], { hasMore: true }), live.generation)
    expect(d.turns.map((t) => t.index)).toEqual([4, 5])
  })
})

// A page or a jump asked under one epoch and answered after a reset snapshot installed the next must change nothing.
describe('the generation fence', () => {
  const old = () => applySnapshot(emptyDoc(), snap([turn(5, [user('u5', 0)])], { hasMore: true, cursor: 'e:1' }))

  it('every replacing snapshot bumps the generation', () => {
    const d0 = old()
    const d1 = applySnapshot(d0, snap([turn(9, [user('u9', 0)])], { reset: true, cursor: 'f:1' }))
    expect(d1.generation).toBe(d0.generation + 1)
  })

  it('a late older page of the previous epoch is dropped', () => {
    const asked = old().generation
    const next = applySnapshot(old(), snap([turn(9, [user('u9', 0)])], { reset: true, cursor: 'f:1' }))
    const d = applyOlderPage(next, snap([turn(4, [user('stale4', 0)])]), asked)
    expect(d).toBe(next)
  })

  it('a late jump of the previous epoch is dropped', () => {
    const asked = old().generation
    const next = applySnapshot(old(), snap([turn(9, [user('u9', 0)])], { reset: true, cursor: 'f:1' }))
    expect(applyAround(next, snap([turn(2, [user('stale2', 0)])], { total: 10 }), asked)).toBe(next)
  })

  it('a page asked under the current generation still applies', () => {
    const d0 = old()
    const d = applyOlderPage(d0, snap([turn(4, [user('u4', 0)])]), d0.generation)
    expect(d.turns.map((t) => t.index)).toEqual([4, 5])
  })
})

describe('header, capabilities and approvals', () => {
  it('a header frame replaces the header and moves the cursor', () => {
    const d = applyHeader(applySnapshot(emptyDoc(), snap([turn(0, [user('u', 0)])])), header({ title: 'new' }), 'e:4')
    expect(d.header?.title).toBe('new')
    expect(d.cursor).toBe('e:4')
  })

  it('a capabilities frame replaces the table', () => {
    const d = applyCapabilities(emptyDoc(), { send: 'prompt', answer_question: 'approval' })
    expect(d.capabilities).toEqual({ send: 'prompt', answer_question: 'approval' })
  })

  it('the approvals snapshot replaces the set; ops add, replace and remove by id', () => {
    let d = applyApprovals(emptyDoc(), [{ id: 'a' }, { id: 'b' }])
    d = applyApprovals(d, [{ id: 'b' }])
    expect(d.approvals.map((a) => a.id)).toEqual(['b'])
    d = applyApprovalOp(d, 'opened', { id: 'c', v: 1 })
    d = applyApprovalOp(d, 'opened', { id: 'c', v: 2 })
    expect(d.approvals).toEqual([{ id: 'b' }, { id: 'c', v: 2 }])
    d = applyApprovalOp(d, 'closed', { id: 'b' })
    expect(d.approvals).toEqual([{ id: 'c', v: 2 }])
    expect(applyApprovalOp(d, 'closed', { id: 'zzz' }).approvals).toEqual([{ id: 'c', v: 2 }])
  })
})

describe('hasItem', () => {
  it('finds an item across turns', () => {
    const d = applySnapshot(emptyDoc(), snap([turn(0, [user('u0', 0)]), turn(1, [user('u1', 0)])]))
    expect(hasItem(d, 'u1')).toBe(true)
    expect(hasItem(d, 'nope')).toBe(false)
  })
})

// The golden fixtures are the daemon's own output: a snapshot built from them must hold every item, in order, and a
// replay of the same document as one big change must give the same document (apply is a pure function of the wire).
describe('against the golden fixtures', () => {
  const withIndex = (turns: Array<{ items: Array<Record<string, unknown>> } & Record<string, unknown>>): Turn[] =>
    turns.map((t) => ({ ...t, items: t.items.map((it, i) => ({ ...it, index: i })) }) as unknown as Turn)

  for (const [name, fx] of [['ask-question', askQuestion], ['subagent', subagent]] as const) {
    it(`${name}: snapshot holds every turn and item in order; replaying them as changes is a no-op`, () => {
      const turns = withIndex(fx.conversation.turns as never)
      const d = applySnapshot(emptyDoc(), snap(turns, { cursor: 'e:1' }))
      expect(d.turns.map((t) => t.items.length)).toEqual(turns.map((t) => t.items.length))
      const replay = applyChanges(d, {
        changes: turns.map((t) => ({ turn: { ...t, items: undefined as never }, items: t.items })), header: header(), cursor: 'e:2',
      })
      expect(replay.turns).toEqual(d.turns)
    })
  }
})
