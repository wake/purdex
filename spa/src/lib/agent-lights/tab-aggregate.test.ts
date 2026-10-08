import { describe, it, expect } from 'vitest'
import type { PaneLayout } from '../../types/tab'
import type { AgentStatus, NormalizedEvent } from '../../stores/useAgentStore'
import { aggregateTabAgents, tabAgentPanes, type TabAgentData, type TabAgentPane } from './tab-aggregate'

const tmux = (id: string, sessionCode: string, hostId = 'h1'): PaneLayout =>
  ({ type: 'leaf', pane: { id, content: { kind: 'tmux-session', hostId, sessionCode, mode: 'terminal', cachedName: '', tmuxInstance: '' } } })
const exec = (id: string, executionId: string, host = 'h1'): PaneLayout =>
  ({ type: 'leaf', pane: { id, content: { kind: 'execution', executionId, host } } })
const blank = (id: string): PaneLayout => ({ type: 'leaf', pane: { id, content: { kind: 'new-tab' } } })
const split = (children: PaneLayout[]): PaneLayout =>
  ({ type: 'split', id: 's', direction: 'h', children, sizes: children.map(() => 100 / children.length) })

const ev = (status: string, background?: NormalizedEvent['background']): NormalizedEvent =>
  ({ agent_type: 'cc', status, raw_event_name: 'x', broadcast_ts: 1, ...(background ? { background } : {}) })
const data = (over: Partial<TabAgentData> = {}): TabAgentData => ({ statuses: {}, unread: {}, agentTypes: {}, lastEvents: {}, ...over })
const panes = (...keys: string[]): TabAgentPane[] => keys.map((key) => ({ key, hostId: 'h1' }))

describe('tabAgentPanes', () => {
  it('lists the primary pane first, then the leaf order, once per key', () => {
    const layout = split([tmux('p1', 'a'), tmux('p2', 'b'), tmux('p3', 'a'), exec('p4', 'e1'), blank('p5')])
    expect(tabAgentPanes(layout).map((p) => p.key)).toEqual(['h1:a', 'h1:b', 'h1:exec-e1'])
  })
  it('keeps the execution id of a worker pane and skips an ended tmux pane', () => {
    const ended: PaneLayout = { type: 'leaf', pane: { id: 'p9', content: { kind: 'tmux-session', hostId: 'h1', sessionCode: 'dead', mode: 'terminal', cachedName: '', tmuxInstance: '', terminated: 'session-closed' } } }
    const got = tabAgentPanes(split([exec('p1', 'e1'), ended]))
    expect(got).toEqual([{ key: 'h1:exec-e1', hostId: 'h1', executionId: 'e1' }])
  })
  it('an ended PRIMARY tmux pane keeps showing its code\'s light (as before U1-3c)', () => {
    const ended: PaneLayout = { type: 'leaf', pane: { id: 'p9', content: { kind: 'tmux-session', hostId: 'h1', sessionCode: 'dead', mode: 'terminal', cachedName: '', tmuxInstance: '', terminated: 'session-closed' } } }
    expect(tabAgentPanes(ended).map((p) => p.key)).toEqual(['h1:dead'])
  })
})

describe('aggregateTabAgents', () => {
  it('older waiting pane beats newer running pane', () => {
    const a = aggregateTabAgents(panes('h1:a', 'h1:b'), data({ statuses: { 'h1:a': 'waiting', 'h1:b': 'running' } }))
    expect(a.status).toBe('waiting')
    expect(a.repKey).toBe('h1:a')
  })
  it('error beats waiting', () => {
    expect(aggregateTabAgents(panes('h1:a', 'h1:b'), data({ statuses: { 'h1:a': 'waiting', 'h1:b': 'error' } })).status).toBe('error')
  })
  it('tie → the primary pane (first in the list)', () => {
    const a = aggregateTabAgents(panes('h1:a', 'h1:b'), data({ statuses: { 'h1:a': 'running', 'h1:b': 'running' } }))
    expect(a.repKey).toBe('h1:a')
  })
  it('unread is OR over panes', () => {
    expect(aggregateTabAgents(panes('h1:a', 'h1:b'), data({ unread: { 'h1:b': true } })).isUnread).toBe(true)
    expect(aggregateTabAgents(panes('h1:a', 'h1:b'), data()).isUnread).toBe(false)
  })
  it('awaiting is OR and forces waiting', () => {
    const a = aggregateTabAgents(panes('h1:a', 'h1:exec-e1'), data({ statuses: { 'h1:a': 'idle', 'h1:exec-e1': 'running' } }), new Set(['h1:exec-e1']))
    expect(a.isAwaitingApproval).toBe(true)
    expect(a.status).toBe('waiting')
    expect(a.repKey).toBe('h1:exec-e1')
  })
  it('background is the highest across panes (workflow > monitor > schedule)', () => {
    const lastEvents = { 'h1:a': ev('idle', 'schedule'), 'h1:b': ev('idle', 'monitor'), 'h1:c': ev('idle') }
    expect(aggregateTabAgents(panes('h1:a', 'h1:b', 'h1:c'), data({ lastEvents })).background).toBe('monitor')
    expect(aggregateTabAgents(panes('h1:a', 'h1:b'), data({ lastEvents: { ...lastEvents, 'h1:b': ev('idle', 'workflow') } })).background).toBe('workflow')
    expect(aggregateTabAgents(panes('h1:c'), data({ lastEvents })).background).toBeUndefined()
  })
  it('the rep pane is the one with the highest status (dots and agent type come from it)', () => {
    const a = aggregateTabAgents(panes('h1:a', 'h1:b'), data({ statuses: { 'h1:a': 'idle', 'h1:b': 'running' } }))
    expect(a.repKey).toBe('h1:b')
  })
  it('a split pane with the only agent represents the tab (the primary pane is a shell)', () => {
    const a = aggregateTabAgents(panes('h1:shell', 'h1:agent'), data({ statuses: { 'h1:agent': 'running' } }))
    expect(a.status).toBe('running')
    expect(a.repKey).toBe('h1:agent')
  })
  it('with no status anywhere the first pane that has an agent type represents the tab, else the primary', () => {
    expect(aggregateTabAgents(panes('h1:a', 'h1:b'), data({ agentTypes: { 'h1:b': 'cc' } })).repKey).toBe('h1:b')
    expect(aggregateTabAgents(panes('h1:a', 'h1:b'), data()).repKey).toBe('h1:a')
  })
  it('no agent pane at all', () => {
    expect(aggregateTabAgents([], data())).toEqual({ status: undefined, isUnread: false, isAwaitingApproval: false, repKey: undefined, background: undefined })
  })
  it('a status that is only idle is idle, not absent', () => {
    expect(aggregateTabAgents(panes('h1:a'), data({ statuses: { 'h1:a': 'idle' as AgentStatus } })).status).toBe('idle')
  })
})
