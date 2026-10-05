import { describe, it, expect } from 'vitest'
import { liveLeafIds, focusTargetOf, statusTargetOf } from './pane-focus'
import type { Pane, PaneContent, PaneLayout } from '../types/tab'

const leaf = (id: string, content: PaneContent = { kind: 'dashboard' }): PaneLayout => ({ type: 'leaf', pane: { id, content } })
const split = (id: string, ...children: PaneLayout[]): PaneLayout => ({
  type: 'split', id, direction: 'h', children, sizes: children.map(() => 100 / children.length),
})

const worker = (executionId: string): PaneContent => ({ kind: 'execution', executionId })
const terminal = (code: string, agent = false): PaneContent => ({
  kind: 'tmux-session', hostId: 'h1', sessionCode: code, mode: 'terminal',
  cachedName: agent ? 'agent' : '', tmuxInstance: '',
})
const editor: PaneContent = { kind: 'editor', source: { type: 'local' }, filePath: '/a.ts' }

/** Test predicate: a worker, or a terminal fixture marked as running an agent (cachedName 'agent'). */
const isAgent = (c: PaneContent) =>
  c.kind === 'execution' || (c.kind === 'tmux-session' && !c.terminated && c.cachedName === 'agent')

const ids = (p: Pane) => p.id

describe('liveLeafIds', () => {
  it('a single leaf → its id', () => {
    expect([...liveLeafIds(leaf('a'))]).toEqual(['a'])
  })

  it('nested splits → every leaf id in layout order, no split ids', () => {
    const layout = split('s1', leaf('a'), split('s2', leaf('b'), leaf('c')))
    const live = liveLeafIds(layout)
    expect([...live]).toEqual(['a', 'b', 'c'])
    expect(live.has('s1')).toBe(false)
    expect(live.has('s2')).toBe(false)
  })
})

describe('focusTargetOf (rule F)', () => {
  const tab = { layout: split('s', leaf('left'), leaf('right')) }

  it('the most recent live pane', () => {
    expect(focusTargetOf(tab, ['right', 'left'])).toBe('right')
  })

  it('skips dead ids to the first live one', () => {
    expect(focusTargetOf(tab, ['gone', 'also-gone', 'right'])).toBe('right')
  })

  it('every recorded pane gone → the primary pane', () => {
    expect(focusTargetOf(tab, ['gone'])).toBe('left')
  })

  it('no record (undefined or empty) → the primary pane', () => {
    expect(focusTargetOf(tab, undefined)).toBe('left')
    expect(focusTargetOf(tab, [])).toBe('left')
  })

  it('a single-leaf tab → that leaf', () => {
    expect(focusTargetOf({ layout: leaf('only') }, undefined)).toBe('only')
  })

  it('a split id in the record is never a target', () => {
    expect(focusTargetOf(tab, ['s'])).toBe('left')
  })
})

describe('statusTargetOf (rule D.4)', () => {
  it('rule 1: the most recently focused live agent pane wins over a more recent plain pane', () => {
    const tab = { layout: split('s', leaf('w1', worker('e1')), leaf('w2', worker('e2')), leaf('t', terminal('plain'))) }
    expect(ids(statusTargetOf(tab, ['t', 'w2', 'w1'], isAgent))).toBe('w2')
  })

  it('rule 1 skips dead ids and non-agent ids', () => {
    const tab = { layout: split('s', leaf('w1', worker('e1')), leaf('w2', worker('e2')), leaf('t', terminal('plain'))) }
    expect(ids(statusTargetOf(tab, ['gone-agent', 't', 'w2'], isAgent))).toBe('w2')
  })

  it('rule 2: no recorded agent pane → the first agent pane in layout order', () => {
    const tab = { layout: split('s', leaf('t', terminal('plain')), leaf('w1', worker('e1')), leaf('w2', worker('e2'))) }
    expect(ids(statusTargetOf(tab, ['t'], isAgent))).toBe('w1')
    expect(ids(statusTargetOf(tab, undefined, isAgent))).toBe('w1')
  })

  it('rule 3: no agent pane at all → the most recently focused live pane', () => {
    const tab = { layout: split('s', leaf('t1', terminal('a')), leaf('t2', terminal('b')), leaf('ed', editor)) }
    expect(ids(statusTargetOf(tab, ['gone', 'ed', 't2'], isAgent))).toBe('ed')
  })

  it('rule 4: no agent pane and no live record → the primary pane', () => {
    const tab = { layout: split('s', leaf('t1', terminal('a')), leaf('ed', editor)) }
    expect(ids(statusTargetOf(tab, ['gone'], isAgent))).toBe('t1')
    expect(ids(statusTargetOf(tab, undefined, isAgent))).toBe('t1')
  })

  it('a terminated tmux pane is not an agent pane (the predicate decides; the helper only asks it)', () => {
    const dead: PaneContent = { ...terminal('cc', true), terminated: 'session-closed' } as PaneContent
    const tab = { layout: split('s', leaf('ed', editor), leaf('t', dead)) }
    expect(ids(statusTargetOf(tab, undefined, isAgent))).toBe('ed')
  })

  it('returns the pane itself, content included', () => {
    const tab = { layout: split('s', leaf('ed', editor), leaf('w', worker('e1'))) }
    expect(statusTargetOf(tab, undefined, isAgent)).toEqual({ id: 'w', content: worker('e1') })
  })

  it('D.4-1: worker + plain terminal → the worker, even after clicking the terminal', () => {
    const tab = { layout: split('s', leaf('w', worker('e1')), leaf('t', terminal('plain'))) }
    expect(ids(statusTargetOf(tab, ['t'], isAgent))).toBe('w')
    expect(ids(statusTargetOf(tab, ['t', 'w'], isAgent))).toBe('w')
  })

  it('D.4-2: two agent panes → follows the click', () => {
    const tab = { layout: split('s', leaf('cc', terminal('cc', true)), leaf('w', worker('e1'))) }
    expect(ids(statusTargetOf(tab, ['w'], isAgent))).toBe('w')
    expect(ids(statusTargetOf(tab, ['cc', 'w'], isAgent))).toBe('cc')
    expect(ids(statusTargetOf(tab, ['w', 'cc'], isAgent))).toBe('w')
  })

  it('D.4-3: editor + CC terminal, never clicked → the terminal', () => {
    const tab = { layout: split('s', leaf('ed', editor), leaf('cc', terminal('cc', true))) }
    expect(ids(statusTargetOf(tab, undefined, isAgent))).toBe('cc')
  })
})
