import { beforeEach, describe, expect, it } from 'vitest'
import { useTabStore } from '../../stores/useTabStore'
import { useSessionStore } from '../../stores/useSessionStore'
import type { PaneLayout, Tab } from '../../types/tab'
import { boundSessions, visibleSessionsByHost } from './visible-sessions'

const tmux = (id: string, hostId: string, sessionCode: string): PaneLayout =>
  ({ type: 'leaf', pane: { id, content: { kind: 'tmux-session', hostId, sessionCode, mode: 'terminal', cachedName: '', tmuxInstance: '' } } })
const ended = (id: string, hostId: string, sessionCode: string): PaneLayout =>
  ({ type: 'leaf', pane: { id, content: { kind: 'tmux-session', hostId, sessionCode, mode: 'terminal', cachedName: '', tmuxInstance: '', terminated: 'tmux-restarted' } } })
const exec = (id: string, executionId: string): PaneLayout => ({ type: 'leaf', pane: { id, content: { kind: 'execution', executionId, host: 'h1' } } })
const blank = (id: string): PaneLayout => ({ type: 'leaf', pane: { id, content: { kind: 'new-tab' } } })
const split = (id: string, children: PaneLayout[]): PaneLayout => ({ type: 'split', id, direction: 'h', children, sizes: children.map(() => 100 / children.length) })
const tab = (id: string, layout: PaneLayout): Tab => ({ id, pinned: false, locked: false, createdAt: 0, layout })

const sess = (code: string, name: string) => ({ code, name }) as never

beforeEach(() => {
  useTabStore.setState({ tabs: {}, activeTabId: null, tabOrder: [] })
  useSessionStore.setState({ sessions: { h1: [sess('c1', 'dev'), sess('c2', 'ops')], h2: [sess('c9', 'nine')] } })
})

describe('visibleSessionsByHost', () => {
  it('is empty with no active tab', () => {
    useTabStore.setState({ tabs: { t1: tab('t1', tmux('p1', 'h1', 'c1')) }, activeTabId: null })
    expect(visibleSessionsByHost()).toEqual({})
  })

  it('a tab with no session pane shows nothing', () => {
    useTabStore.setState({ tabs: { t1: tab('t1', blank('p1')) }, activeTabId: 't1' })
    expect(visibleSessionsByHost()).toEqual({})
  })

  it('a single session tab: its code and the tmux session name from the sessions store', () => {
    useTabStore.setState({ tabs: { t1: tab('t1', tmux('p1', 'h1', 'c1')) }, activeTabId: 't1' })
    expect(visibleSessionsByHost()).toEqual({ h1: [{ code: 'c1', name: 'dev' }] })
  })

  it('every pane of a split, grouped by host, each session once', () => {
    useTabStore.setState({
      tabs: { t1: tab('t1', split('s', [tmux('p1', 'h1', 'c1'), split('s2', [tmux('p2', 'h2', 'c9'), tmux('p3', 'h1', 'c2'), tmux('p4', 'h1', 'c1')])])) },
      activeTabId: 't1',
    })
    expect(visibleSessionsByHost()).toEqual({
      h1: [{ code: 'c1', name: 'dev' }, { code: 'c2', name: 'ops' }],
      h2: [{ code: 'c9', name: 'nine' }],
    })
  })

  it('only the ACTIVE tab: a session in another tab is not shown', () => {
    useTabStore.setState({ tabs: { t1: tab('t1', tmux('p1', 'h1', 'c1')), t2: tab('t2', tmux('p2', 'h1', 'c2')) }, activeTabId: 't2' })
    expect(visibleSessionsByHost()).toEqual({ h1: [{ code: 'c2', name: 'ops' }] })
  })

  it('non-session panes, ended panes and workers are ignored', () => {
    useTabStore.setState({
      tabs: { t1: tab('t1', split('s', [blank('p1'), ended('p2', 'h1', 'c1'), exec('p3', 'e1'), tmux('p4', 'h1', 'c2')])) },
      activeTabId: 't1',
    })
    expect(visibleSessionsByHost()).toEqual({ h1: [{ code: 'c2', name: 'ops' }] })
  })

  it('a session the sessions store does not know yet is reported with an empty name (the code still matches)', () => {
    useTabStore.setState({ tabs: { t1: tab('t1', tmux('p1', 'h1', 'zzz')) }, activeTabId: 't1' })
    expect(visibleSessionsByHost()).toEqual({ h1: [{ code: 'zzz', name: '' }] })
  })
})

// The daemon refuses a whole report for one bad field, for good: what is sent must fit (spec §5.4).
describe('boundSessions', () => {
  it('keeps at most 200 entries, in order', () => {
    const list = Array.from({ length: 250 }, (_, i) => ({ code: `c${i}`, name: 'n' }))
    const out = boundSessions(list)
    expect(out).toHaveLength(200)
    expect(out[0].code).toBe('c0')
    expect(out[199].code).toBe('c199')
  })

  it('cuts names and codes at 64 characters (not bytes) and drops control characters', () => {
    const out = boundSessions([{ code: 'c1', name: '接'.repeat(100) }, { code: 'c2', name: 'a\u0000b\nc\u200Bd' }])
    expect(Array.from(out[0].name)).toHaveLength(64)
    expect(out[1].name).toBe('abcd')
    expect(boundSessions([{ code: 'x'.repeat(100), name: '' }])[0].code).toHaveLength(64)
  })

  it('skips an entry whose code is empty after cleaning', () => {
    expect(boundSessions([{ code: '', name: 'a' }, { code: '\u0001', name: 'b' }, { code: 'ok', name: 'c' }])).toEqual([{ code: 'ok', name: 'c' }])
  })

  it('keeps the JSON under 12 KiB even when 200 long names are shown', () => {
    const list = Array.from({ length: 200 }, (_, i) => ({ code: `c${String(i).padStart(3, '0')}`, name: '接'.repeat(64) }))
    const out = boundSessions(list)
    expect(new TextEncoder().encode(JSON.stringify(out)).length).toBeLessThanOrEqual(12 * 1024)
    expect(out.length).toBeGreaterThan(0)
    expect(out.length).toBeLessThan(200)
  })

  it('visibleSessionsByHost applies it', () => {
    useSessionStore.setState({ sessions: { h1: [sess('c1', 'x\u0007y')] } })
    useTabStore.setState({ tabs: { t1: tab('t1', tmux('p1', 'h1', 'c1')) }, activeTabId: 't1' })
    expect(visibleSessionsByHost()).toEqual({ h1: [{ code: 'c1', name: 'xy' }] })
  })
})
