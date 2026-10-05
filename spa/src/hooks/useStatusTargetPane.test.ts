import { describe, it, expect, beforeEach } from 'vitest'
import { act, renderHook } from '@testing-library/react'
import { isAgentPane, useStatusTargetPane } from './useStatusTargetPane'
import { useAgentStore } from '../stores/useAgentStore'
import { usePaneFocusStore } from '../stores/usePaneFocusStore'
import { compositeKey } from '../lib/composite-key'
import type { PaneContent, PaneLayout, Tab } from '../types/tab'

const HOST = 'h1'

const leaf = (id: string, content: PaneContent): PaneLayout => ({ type: 'leaf', pane: { id, content } })
const split = (id: string, ...children: PaneLayout[]): PaneLayout => ({
  type: 'split', id, direction: 'h', children, sizes: children.map(() => 100 / children.length),
})
const tabOf = (id: string, layout: PaneLayout): Tab => ({ id, pinned: false, locked: false, createdAt: 0, layout })

const worker = (executionId: string): PaneContent => ({ kind: 'execution', executionId })
const terminal = (code: string, extra: Partial<Extract<PaneContent, { kind: 'tmux-session' }>> = {}): PaneContent => ({
  kind: 'tmux-session', hostId: HOST, sessionCode: code, mode: 'terminal', cachedName: '', tmuxInstance: '', ...extra,
})
const editor: PaneContent = { kind: 'editor', source: { type: 'inapp' }, filePath: '/a.md' }

/** Mark a tmux session as running a detected agent, the way the agent event handler does. */
function setAgent(code: string, type = 'cc') {
  useAgentStore.setState((s) => ({ agentTypes: { ...s.agentTypes, [compositeKey(HOST, code)]: type } }))
}

/** What a pointerdown / focus inside a pane leaf records (PaneLayoutRenderer's writer, spec §8.1). */
function click(tabId: string, paneId: string) {
  act(() => usePaneFocusStore.getState().touch(tabId, paneId))
}

beforeEach(() => {
  useAgentStore.setState({ agentTypes: {} })
  usePaneFocusStore.setState({ recent: {} })
})

describe('isAgentPane (spec §9.1)', () => {
  it('an execution (worker) pane → true', () => {
    expect(isAgentPane(worker('exc_1'), {})).toBe(true)
  })

  it('a running tmux session with a detected agent → true', () => {
    expect(isAgentPane(terminal('cc'), { [compositeKey(HOST, 'cc')]: 'cc' })).toBe(true)
  })

  it('a tmux session without a detected agent → false', () => {
    expect(isAgentPane(terminal('plain'), { [compositeKey(HOST, 'other')]: 'cc' })).toBe(false)
  })

  it('a terminated tmux session → false, even with an agent type left over', () => {
    expect(isAgentPane(terminal('cc', { terminated: 'session-closed' }), { [compositeKey(HOST, 'cc')]: 'cc' })).toBe(false)
  })

  it('the agent type is keyed by host as well as session code', () => {
    expect(isAgentPane(terminal('cc'), { [compositeKey('other-host', 'cc')]: 'cc' })).toBe(false)
  })

  it('any other kind → false', () => {
    expect(isAgentPane(editor, {})).toBe(false)
    expect(isAgentPane({ kind: 'dashboard' }, {})).toBe(false)
    expect(isAgentPane({ kind: 'new-tab' }, {})).toBe(false)
  })
})

describe('useStatusTargetPane (rule D.4)', () => {
  it('no tab → null', () => {
    const { result } = renderHook(() => useStatusTargetPane(null))
    expect(result.current).toBeNull()
  })

  it('a single-pane tab → that pane', () => {
    const tab = tabOf('t', leaf('only', editor))
    const { result } = renderHook(() => useStatusTargetPane(tab))
    expect(result.current?.id).toBe('only')
  })

  it('D.4-1: worker + plain terminal → after clicking the plain terminal the bar still shows the worker', () => {
    const tab = tabOf('t', split('s', leaf('w', worker('exc_1')), leaf('term', terminal('plain'))))
    const { result } = renderHook(() => useStatusTargetPane(tab))
    expect(result.current?.id).toBe('w')
    click('t', 'term')
    expect(result.current?.id).toBe('w')
  })

  it('D.4-2: two agent panes → follows the click', () => {
    setAgent('cc')
    const tab = tabOf('t', split('s', leaf('cc', terminal('cc')), leaf('w', worker('exc_1'))))
    const { result } = renderHook(() => useStatusTargetPane(tab))
    expect(result.current?.id).toBe('cc')
    click('t', 'w')
    expect(result.current?.id).toBe('w')
    click('t', 'cc')
    expect(result.current?.id).toBe('cc')
  })

  it('D.4-3: editor + CC terminal, never clicked → the terminal', () => {
    setAgent('cc')
    const tab = tabOf('t', split('s', leaf('ed', editor), leaf('cc', terminal('cc'))))
    const { result } = renderHook(() => useStatusTargetPane(tab))
    expect(result.current?.id).toBe('cc')
  })

  it('rule 3: no agent pane → the most recently focused live pane', () => {
    const tab = tabOf('t', split('s', leaf('t1', terminal('a')), leaf('ed', editor)))
    const { result } = renderHook(() => useStatusTargetPane(tab))
    click('t', 'ed')
    expect(result.current?.id).toBe('ed')
    click('t', 't1')
    expect(result.current?.id).toBe('t1')
  })

  it('rule 4: no agent pane and no live record → the primary pane', () => {
    const tab = tabOf('t', split('s', leaf('t1', terminal('a')), leaf('ed', editor)))
    const { result } = renderHook(() => useStatusTargetPane(tab))
    expect(result.current?.id).toBe('t1')
    click('t', 'gone')
    expect(result.current?.id).toBe('t1')
  })

  it("another tab's record does not move this tab's target", () => {
    const tab = tabOf('t', split('s', leaf('t1', terminal('a')), leaf('ed', editor)))
    const { result } = renderHook(() => useStatusTargetPane(tab))
    click('other', 'ed')
    expect(result.current?.id).toBe('t1')
  })

  it('follows agent detection live: a terminal that starts an agent takes over from the clicked editor', () => {
    const tab = tabOf('t', split('s', leaf('ed', editor), leaf('cc', terminal('cc'))))
    const { result } = renderHook(() => useStatusTargetPane(tab))
    click('t', 'ed')
    expect(result.current?.id).toBe('ed')
    act(() => setAgent('cc'))
    expect(result.current?.id).toBe('cc')
    act(() => useAgentStore.setState({ agentTypes: {} }))
    expect(result.current?.id).toBe('ed')
  })

  it('a terminated tmux pane with a leftover agent type does not take the bar', () => {
    setAgent('cc')
    const tab = tabOf('t', split('s', leaf('ed', editor), leaf('cc', terminal('cc', { terminated: 'session-closed' }))))
    const { result } = renderHook(() => useStatusTargetPane(tab))
    expect(result.current?.id).toBe('ed')
  })

  it('an agent-store write that changes no pane of this tab does not re-render', () => {
    setAgent('cc')
    const tab = tabOf('t', split('s', leaf('ed', editor), leaf('cc', terminal('cc'))))
    let renders = 0
    const { result } = renderHook(() => { renders++; return useStatusTargetPane(tab) })
    const before = renders
    act(() => setAgent('unrelated-session'))
    act(() => useAgentStore.setState((s) => ({ models: { ...s.models, x: 'y' } })))
    expect(renders).toBe(before)
    expect(result.current?.id).toBe('cc')
  })
})
