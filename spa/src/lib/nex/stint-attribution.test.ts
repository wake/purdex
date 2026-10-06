// Conversation entity spec §10.3 / D20: which earlier worker stint drew each
// prelude line. The fixture is the real capture (terminal -> worker A ->
// terminal) plus a synthetic worker B after it; the viewer is a later stint C.
import { describe, it, expect } from 'vitest'
import real from './__fixtures__/prelude-06GGS8J1YKZCPF4BRXZTX764F4.json'
import { sanitizePreludePage, type PreludeItem } from './prelude-wire'
import { derivePrelude, preludeBlocks, type PreludeBlock } from './prelude'
import type { Stint } from './entity-stints'
import { attributeItems, attributionRuns, type Attribution } from './stint-attribution'

type RawItem = { pos: string; kind: string; at?: number; payload: Record<string, unknown> }
// Test data only: the capture predates `offset`, and each of its poses starts
// with its line's byte offset, so that is the offset filled in here. The SPA
// itself never parses a pos (D20).
const lineStart = (pos: string) => Number(pos.split('.')[0])
const B_AT = 454607 // the capture's total_bytes: worker B's transcript starts where it ends
const synthetic: RawItem[] = [
  { pos: `${B_AT}.0`, kind: 'prelude.segment', payload: { entrypoint: 'sdk-cli' } },
  { pos: `${B_AT}.1`, kind: 'user', payload: { type: 'user', message: { role: 'user', content: [{ type: 'text', text: 'continue in B' }] } } },
  { pos: '455100.1', kind: 'assistant', payload: { type: 'assistant', message: { role: 'assistant', content: [{ type: 'tool_use', id: 'tb', name: 'Bash', input: { command: 'ls' } }] } } },
  { pos: '455100.2', kind: 'tool_use', payload: { tool_use_id: 'tb', name: 'Bash', input: { command: 'ls' } } },
  { pos: '455900.1', kind: 'user', payload: { type: 'user', message: { role: 'user', content: [{ type: 'tool_result', tool_use_id: 'tb', content: 'a' }] } } },
  { pos: '455900.2', kind: 'tool_result', payload: { tool_use_id: 'tb', content: 'a' } },
  { pos: '456300.1', kind: 'assistant', payload: { type: 'assistant', message: { role: 'assistant', content: [{ type: 'text', text: 'B done' }] } } },
]
const rawItems = [...(real as unknown as { items: RawItem[] }).items, ...synthetic]
  .map((r) => ({ ...r, at: r.at ?? 1, offset: lineStart(r.pos) }))
const items: PreludeItem[] = sanitizePreludePage({ ...real, items: rawItems })!.items
const posesOf = (from: string, to: string) => {
  const a = items.findIndex((i) => i.pos === from)
  const b = items.findIndex((i) => i.pos === to)
  return items.slice(a, b + 1).map((i) => i.pos)
}

const A_AT = 394248 // the capture's own worker segment
const A: Stint = { id: 'exc_A', boundary: A_AT, createdAt: 1 }
const B: Stint = { id: 'exc_B', boundary: B_AT, createdAt: 2 }

describe('attributeItems', () => {
  const at = attributeItems(items, [A, B])

  it('sanity: the wire kept every offset', () => {
    expect(items).toHaveLength(55 + synthetic.length)
    expect(items.every((i) => i.offset === lineStart(i.pos))).toBe(true)
  })

  it('maps worker A\'s lines to A and worker B\'s to B: marker, messages and tool items alike', () => {
    for (const pos of posesOf('394248.0', '411382.1')) expect(at.get(pos)).toBe(A.id)
    for (const pos of synthetic.map((s) => s.pos)) expect(at.get(pos)).toBe(B.id)
    expect([...at.keys()]).toEqual([...posesOf('394248.0', '411382.1'), ...synthetic.map((s) => s.pos)])
  })

  it('maps every terminal line to none', () => {
    for (const pos of [...posesOf('22485.0', '393190.1'), ...posesOf('415815.0', '454104.1')]) expect(at.has(pos)).toBe(false)
  })

  it('mutation guard: a line at exactly b(B) is B\'s (largest boundary <= offset, not < and not the smallest)', () => {
    expect(items.find((i) => i.pos === `${B_AT}.1`)!.offset).toBe(B.boundary)
    expect(at.get(`${B_AT}.1`)).toBe(B.id)
    expect(at.get(`${B_AT}.0`)).toBe(B.id)
  })

  it('lines before the first loaded marker are unknown, so none, until the older page brings it', () => {
    // A page that starts right after worker A's marker: its lines have no known entrypoint yet.
    const tail = items.slice(items.findIndex((i) => i.pos === '394248.1'))
    const t = attributeItems(tail, [A, B])
    expect(t.has('394248.1')).toBe(false)
    expect(t.has('411382.1')).toBe(false)
    expect(t.get(`${B_AT}.1`)).toBe(B.id)
  })

  it('worker -> worker with no marker between (a rebuild): split exactly at b(next)', () => {
    const A2: Stint = { id: 'exc_A2', boundary: 411382, createdAt: 2 }
    const t = attributeItems(items, [A, A2])
    expect(t.get('394248.0')).toBe(A.id)
    expect(t.get('394248.1')).toBe(A.id)
    expect(t.get('411382.1')).toBe(A2.id)
    // B's lines are past A2's boundary and nothing newer is listed: A2's.
    expect(t.get(`${B_AT}.1`)).toBe(A2.id)
  })

  it('equal boundaries: the later of the two in the list (the newer) takes the lines', () => {
    const early: Stint = { id: 'exc_early', boundary: A_AT, createdAt: 1 }
    const late: Stint = { id: 'exc_late', boundary: A_AT, createdAt: 2 }
    expect(attributeItems(items, [early, late]).get('394248.1')).toBe(late.id)
  })

  it('a worker line below the first stint\'s boundary, or with no offset, is not attributed', () => {
    // Only B is listed: worker A's lines sit below every boundary.
    const onlyB = attributeItems(items, [B])
    expect(onlyB.has('394248.1')).toBe(false)
    expect(onlyB.get(`${B_AT}.1`)).toBe(B.id)
    const noOffset = items.map((i) => (i.pos === '411382.1' ? { ...i, offset: null } : i))
    const t = attributeItems(noOffset, [A, B])
    expect(t.has('411382.1')).toBe(false)
    expect(t.get('394248.1')).toBe(A.id)
  })

  it('D21: a stint whose boundary is past every offset gets nothing; the others are unaffected', () => {
    const Z: Stint = { id: 'exc_Z', boundary: 999999, createdAt: 3 }
    const t = attributeItems(items, [A, B, Z])
    expect([...t.values()].includes(Z.id)).toBe(false)
    expect([...t.entries()]).toEqual([...at.entries()])
  })

  it('no stints listed: nothing is attributed', () => {
    expect(attributeItems(items, []).size).toBe(0)
  })
})

describe('attributionRuns', () => {
  const view = derivePrelude(items)
  const at = attributeItems(items, [A, B])
  const entryRuns = (a: Attribution) => attributionRuns(view.entries, (e) => [e.pos, e.pos], a)
  const posOf: string[] = []
  for (const e of view.entries) if (e.kind === 'message') posOf[e.m] = e.pos
  const blockPoses = (b: PreludeBlock): [string, string] => (b.kind === 'span' ? [posOf[b.start], posOf[b.end - 1]] : [b.entry.pos, b.entry.pos])

  it('room: runs split exactly where the attribution changes, keyed by stint and last pos', () => {
    const runs = entryRuns(at)
    expect(runs.map((r) => [r.stintId, r.key])).toEqual([
      [null, 'plain:393190.1'], [A.id, `${A.id}:411382.1`], [null, 'plain:454104.1'], [B.id, `${B.id}:456300.1`],
    ])
    // Contiguous, covering every entry once, each run of one attribution, neighbours different.
    expect(runs[0].start).toBe(0)
    expect(runs.at(-1)!.end).toBe(view.entries.length)
    for (let k = 1; k < runs.length; k++) expect(runs[k].start).toBe(runs[k - 1].end)
    for (const r of runs) {
      for (const e of view.entries.slice(r.start, r.end)) expect(at.get(e.pos) ?? null).toBe(r.stintId)
    }
    expect(view.entries[runs[1].start].pos).toBe('394248.0')
    expect(view.entries[runs[3].start].pos).toBe(`${B_AT}.0`)
  })

  it('an unattributed prelude is one run, keyed by its last pos', () => {
    expect(entryRuns(new Map())).toEqual([{ stintId: null, key: 'plain:456300.1', start: 0, end: view.entries.length }])
  })

  it('the key of a run does not move when an older page joins the front', () => {
    const tail = derivePrelude(items.slice(items.findIndex((i) => i.pos === '415815.0')))
    const tailKeys = attributionRuns(tail.entries, (e) => [e.pos, e.pos], attributeItems(items.slice(items.findIndex((i) => i.pos === '415815.0')), [A, B])).map((r) => r.key)
    expect(tailKeys).toEqual(['plain:454104.1', `${B.id}:456300.1`])
    expect(entryRuns(at).map((r) => r.key).slice(-2)).toEqual(tailKeys)
  })

  it('no units: no runs', () => {
    expect(attributionRuns([], (e: string) => [e, e], at)).toEqual([])
  })

  it('chat: a span takes its first message\'s attribution and is keyed by its last message', () => {
    // Worker A rebuilt into A2 at 411382 with no marker: the span [394248.1, 411382.1] straddles the split.
    // Known limit (#1614): the straddling tail runs under A; enrichment joins by unique id, so it loses enrichment, never mismatches.
    const A2: Stint = { id: 'exc_A2', boundary: 411382, createdAt: 2 }
    const split = attributeItems(items, [A, A2])
    const blocks = preludeBlocks(view)
    const runs = attributionRuns(blocks, blockPoses, split)
    const a = runs.find((r) => r.stintId === A.id)!
    expect(a.key).toBe(`${A.id}:411382.1`)
    expect(blocks.slice(a.start, a.end).map(blockPoses)).toEqual([['394248.0', '394248.0'], ['394248.1', '411382.1']])
    // A2 owns 411382.1 and every later worker line, but no block starts with one before B's marker.
    expect(runs.map((r) => r.stintId)).toEqual([null, A.id, null, A2.id])
    expect(blockPoses(blocks[runs[3].start])).toEqual([`${B_AT}.0`, `${B_AT}.0`])
  })

  it('chat: runs over the A/B attribution match the room\'s stints in order', () => {
    const runs = attributionRuns(preludeBlocks(view), blockPoses, at)
    expect(runs.map((r) => [r.stintId, r.key])).toEqual([
      [null, 'plain:393190.1'], [A.id, `${A.id}:411382.1`], [null, 'plain:454104.1'], [B.id, `${B.id}:456300.1`],
    ])
  })
})
