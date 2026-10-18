// spa/src/lib/conversations/model.stages.test.ts — the message stages the document holds (message-stages plan §1.1–1.2, MS2-3): per client_msg_id
// the latest stage; an id's stage never moves back; a snapshot's list replaces them.
import { describe, it, expect } from 'vitest'
import { applyStages, applySnapshot, emptyDoc } from './model'
import type { Snapshot, Stage } from './types'

const st = (client_msg_id: string, stage: string, over: Partial<Stage> = {}): Stage => ({ client_msg_id, stage, at: 1, ...over })
const snap = (stages?: Stage[]): Snapshot => ({
  conversation: {
    key: { host_id: 'h', provider: 'claude', session_id: 's' }, backend: 'terminal', provider: 'claude', title: 't', status: 'idle',
    capabilities: { source: 'transcript' }, turns: [],
  },
  header: { title: 't', status: 'idle', backend: 'terminal', live: true },
  window: { first_index: 0, last_index: 0, total_turns: 0, has_more_before: false },
  cursor: 'e:1',
  ...(stages ? { stages } : {}),
})

describe('applyStages', () => {
  it('keeps the latest stage per id', () => {
    const d = applyStages(applyStages(emptyDoc(), [st('a', 'received'), st('b', 'received')]), [st('a', 'queued', { at: 5 })])
    expect(d.stages.a).toMatchObject({ stage: 'queued', at: 5 })
    expect(d.stages.b.stage).toBe('received')
  })
  it('an id never moves back, and a terminal stage stays', () => {
    let d = applyStages(emptyDoc(), [st('a', 'queued')])
    d = applyStages(d, [st('a', 'filled')])
    expect(d.stages.a.stage).toBe('queued')
    d = applyStages(d, [st('a', 'read')])
    d = applyStages(d, [st('a', 'queued')])
    expect(d.stages.a.stage).toBe('read')
  })
  it('unknown stands in for a stage before the queue, and may still advance to queued / read', () => {
    let d = applyStages(emptyDoc(), [st('a', 'entered')])
    d = applyStages(d, [st('a', 'unknown', { reason: 'not_confirmed' })])
    expect(d.stages.a).toMatchObject({ stage: 'unknown', reason: 'not_confirmed' })
    d = applyStages(d, [st('a', 'queued')])
    expect(d.stages.a.stage).toBe('queued')
  })
  it('unknown does not replace a queued id', () => {
    const d = applyStages(applyStages(emptyDoc(), [st('a', 'queued')]), [st('a', 'unknown')])
    expect(d.stages.a.stage).toBe('queued')
  })
  it('an empty or repeated frame is no change (same document object)', () => {
    const d = applyStages(emptyDoc(), [st('a', 'queued')])
    expect(applyStages(d, [])).toBe(d)
    expect(applyStages(d, [st('a', 'queued')])).toBe(d)
  })
})

describe('applySnapshot stages', () => {
  it('a snapshot with stages replaces them (the reconnect sees where its messages are)', () => {
    const before = applyStages(emptyDoc(), [st('old', 'queued')])
    const d = applySnapshot(before, snap([st('new', 'filled')]))
    expect(Object.keys(d.stages)).toEqual(['new'])
  })
  it('a snapshot without stages (an older daemon) leaves none', () => {
    expect(applySnapshot(emptyDoc(), snap()).stages).toEqual({})
  })
})
