import { describe, it, expect } from 'vitest'
import { planLayoutChange, slotsOf } from './layout-change'
import { applyLayoutPattern, collectLeaves } from './pane-tree'
import type { PaneContent, PaneLayout } from '../types/tab'

const leaf = (id: string, content: PaneContent): PaneLayout => ({ type: 'leaf', pane: { id, content } })
const split = (id: string, ...children: PaneLayout[]): PaneLayout => ({
  type: 'split', id, direction: 'h', children, sizes: children.map(() => 100 / children.length),
})

const blank: PaneContent = { kind: 'new-tab' }
const terminal = (code: string): PaneContent => ({
  kind: 'tmux-session', hostId: 'h1', sessionCode: code, mode: 'terminal', cachedName: '', tmuxInstance: '',
})
const editor: PaneContent = { kind: 'editor', source: { type: 'inapp' }, filePath: '/a.md' }
const worker = (id: string): PaneContent => ({ kind: 'execution', executionId: id })

/** Terminals whose session code starts with `cc` run Claude Code; workers are always agents (spec §9.1). */
const isAgent = (c: PaneContent) =>
  c.kind === 'execution' || (c.kind === 'tmux-session' && c.sessionCode.startsWith('cc'))

const ids = (panes: { id: string }[]) => panes.map((p) => p.id)

describe('slotsOf', () => {
  it('single has one slot, a split two', () => {
    expect(slotsOf('single')).toBe(1)
    expect(slotsOf('split-h')).toBe(2)
    expect(slotsOf('split-v')).toBe(2)
  })
})

describe('planLayoutChange — case 1: the content panes fit, nothing with content closes', () => {
  it('spec §10: one terminal + a blank pane → single applies at once, keeping the terminal', () => {
    const layout = split('s', leaf('blank', blank), leaf('term', terminal('plain')))
    expect(planLayoutChange(layout, 'single', { isAgent, recent: undefined })).toEqual({ kind: 'apply', keepIds: ['term'] })
  })

  it('a single pane → split keeps it and asks nothing', () => {
    const layout = leaf('ed', editor)
    expect(planLayoutChange(layout, 'split-h', { isAgent, recent: undefined })).toEqual({ kind: 'apply', keepIds: ['ed'] })
  })

  it('two content panes → a split keeps both, even when neither is an agent', () => {
    const layout = split('s', leaf('ed', editor), leaf('term', terminal('plain')), leaf('b', blank))
    expect(planLayoutChange(layout, 'split-v', { isAgent, recent: undefined })).toEqual({ kind: 'apply', keepIds: ['ed', 'term'] })
  })

  it('an existing blank pane fills a free slot before a fresh one would, in layout order', () => {
    const layout = split('s', leaf('b1', blank), leaf('term', terminal('plain')), leaf('b2', blank))
    expect(planLayoutChange(layout, 'split-v', { isAgent, recent: undefined })).toEqual({ kind: 'apply', keepIds: ['b1', 'term'] })
  })

  it('only blank panes → keeps the first k blanks', () => {
    const layout = split('s', leaf('b1', blank), leaf('b2', blank))
    expect(planLayoutChange(layout, 'single', { isAgent, recent: undefined })).toEqual({ kind: 'apply', keepIds: ['b1'] })
  })
})

describe('planLayoutChange — case 2: exactly k agent panes', () => {
  it('spec §10: one CC terminal + an editor + a plain terminal → single keeps the CC terminal after a confirm', () => {
    const layout = split('s', leaf('ed', editor), leaf('cc', terminal('cc1')), leaf('plain', terminal('plain')))
    const plan = planLayoutChange(layout, 'single', { isAgent, recent: ['plain', 'ed'] })
    expect(plan.kind).toBe('confirm')
    if (plan.kind !== 'confirm') return
    expect(plan.keepIds).toEqual(['cc'])
    expect(ids(plan.closing)).toEqual(['ed', 'plain'])
  })

  it('two agents among three content panes → a split keeps both agents, in layout order; blanks are not listed as closing', () => {
    const layout = split('s', leaf('w', worker('exc_1')), leaf('ed', editor), leaf('b', blank), leaf('cc', terminal('cc1')))
    const plan = planLayoutChange(layout, 'split-h', { isAgent, recent: ['cc'] })
    expect(plan).toMatchObject({ kind: 'confirm', keepIds: ['w', 'cc'] })
    if (plan.kind === 'confirm') expect(ids(plan.closing)).toEqual(['ed'])
  })
})

describe('planLayoutChange — case 3: the keep picker', () => {
  it('spec §10: two CC terminals → single opens the picker with the most recently focused one preselected', () => {
    const layout = split('s', leaf('a', terminal('cc-a')), leaf('b', terminal('cc-b')))
    const plan = planLayoutChange(layout, 'single', { isAgent, recent: ['b', 'a'] })
    expect(plan.kind).toBe('pick')
    if (plan.kind !== 'pick') return
    expect(plan.k).toBe(1)
    expect(ids(plan.candidates)).toEqual(['a', 'b'])
    expect(plan.preselected).toEqual(['b'])
  })

  it('spec §10: an editor + a plain terminal (no agent) → single opens the picker', () => {
    const layout = split('s', leaf('ed', editor), leaf('plain', terminal('plain')))
    const plan = planLayoutChange(layout, 'single', { isAgent, recent: undefined })
    expect(plan.kind).toBe('pick')
    if (plan.kind !== 'pick') return
    expect(ids(plan.candidates)).toEqual(['ed', 'plain'])
    // No focus record → topped up in layout order.
    expect(plan.preselected).toEqual(['ed'])
  })

  it('candidates are the content panes only, in layout order (blank panes are never offered)', () => {
    const layout = split('s', leaf('b', blank), leaf('ed', editor), leaf('x', terminal('plain')), leaf('y', terminal('plain2')))
    const plan = planLayoutChange(layout, 'split-h', { isAgent, recent: undefined })
    expect(plan.kind === 'pick' && ids(plan.candidates)).toEqual(['ed', 'x', 'y'])
  })

  it('preselection follows the focus record: the k most recent live content panes, most recent first', () => {
    const layout = split('s', leaf('a', terminal('cc-a')), leaf('b', terminal('cc-b')), leaf('c', terminal('cc-c')))
    const plan = planLayoutChange(layout, 'split-v', { isAgent, recent: ['c', 'a', 'b'] })
    expect(plan).toMatchObject({ kind: 'pick', k: 2, preselected: ['c', 'a'] })
  })

  it('preselection skips dead ids and blank panes in the record, then tops up in layout order', () => {
    const layout = split('s', leaf('a', terminal('cc-a')), leaf('blank', blank), leaf('b', terminal('cc-b')), leaf('c', terminal('cc-c')))
    const plan = planLayoutChange(layout, 'split-h', { isAgent, recent: ['gone', 'blank', 'c'] })
    expect(plan).toMatchObject({ kind: 'pick', k: 2, preselected: ['c', 'a'] })
  })

  it('one agent but two slots and three content panes → picker (not exactly k agents)', () => {
    const layout = split('s', leaf('ed', editor), leaf('cc', terminal('cc1')), leaf('plain', terminal('plain')))
    expect(planLayoutChange(layout, 'split-h', { isAgent, recent: undefined }).kind).toBe('pick')
  })

  it('three agents for two slots → picker', () => {
    const layout = split('s', leaf('a', worker('e1')), leaf('b', worker('e2')), leaf('c', terminal('cc-c')))
    expect(planLayoutChange(layout, 'split-h', { isAgent, recent: undefined }).kind).toBe('pick')
  })
})

describe('planLayoutChange → applyLayoutPattern: the result keeps exactly the planned panes', () => {
  it('case 2: the agent pane is the only survivor', () => {
    const layout = split('s', leaf('ed', editor), leaf('cc', terminal('cc1')), leaf('plain', terminal('plain')))
    const plan = planLayoutChange(layout, 'single', { isAgent, recent: undefined })
    if (plan.kind !== 'confirm') throw new Error(`expected confirm, got ${plan.kind}`)
    expect(ids(collectLeaves(applyLayoutPattern(layout, 'single', plan.keepIds)))).toEqual(['cc'])
  })

  it('case 1: a blank pane in front of the content keeps its position', () => {
    const layout = split('s', leaf('b1', blank), leaf('term', terminal('plain')))
    const plan = planLayoutChange(layout, 'split-v', { isAgent, recent: undefined })
    if (plan.kind !== 'apply') throw new Error(`expected apply, got ${plan.kind}`)
    expect(ids(collectLeaves(applyLayoutPattern(layout, 'split-v', plan.keepIds)))).toEqual(['b1', 'term'])
  })
})
