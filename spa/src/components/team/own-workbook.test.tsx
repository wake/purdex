// spa/src/components/team/own-workbook.test.tsx — which conversation a tab shows (WA-2b-1b): its first `cc` tmux-session pane.
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { ownSessionOf, useOwnWorkbook } from './own-workbook'
import { useTabStore } from '../../stores/useTabStore'
import { useWorkbookStore } from '../../stores/useWorkbookStore'
import { seedWorkbook } from '../../lib/team/__tests__/workbook-fixture'
import type { Tab, PaneContent, PaneLayout } from '../../types/tab'

const cc = (over: Partial<Extract<PaneContent, { kind: 'tmux-session' }>> = {}, agent: { type: string; sessionId?: string } | null = { type: 'cc', sessionId: 'S1' }): PaneContent => ({
  kind: 'tmux-session', hostId: 'h1', sessionCode: 'abc', mode: 'terminal', cachedName: 'n', tmuxInstance: 'i',
  rebuild: agent ? { sessionName: 'n', tmuxInstance: 'i', agent: { ...agent, updatedAt: 1 }, capturedAt: 1 } : undefined, ...over,
})
const leaf = (content: PaneContent, id = 'p'): PaneLayout => ({ type: 'leaf', pane: { id, content } }) as PaneLayout

describe('ownSessionOf', () => {
  it('a cc tmux-session pane with a recorded session id names its conversation', () => {
    expect(ownSessionOf(leaf(cc()))).toEqual({ hostId: 'h1', sessionId: 'S1' })
  })
  it('other agents, no recorded id, terminated panes and other contents name none', () => {
    expect(ownSessionOf(leaf(cc({}, { type: 'codex', sessionId: 'S1' })))).toBeNull()
    expect(ownSessionOf(leaf(cc({}, { type: 'cc' })))).toBeNull()
    expect(ownSessionOf(leaf(cc({}, null)))).toBeNull()
    expect(ownSessionOf(leaf(cc({ terminated: 'session-closed' })))).toBeNull()
    expect(ownSessionOf(leaf({ kind: 'dashboard' } as PaneContent))).toBeNull()
  })
})

describe('useOwnWorkbook', () => {
  beforeEach(() => {
    useWorkbookStore.getState().reset()
    useTabStore.setState({ tabs: {}, activeTabId: null } as never)
  })
  const open = (layout: PaneLayout) => useTabStore.setState({ tabs: { t1: { id: 't1', layout } as unknown as Tab }, activeTabId: 't1' } as never)

  it('is the target only when the conversation has a workbook on a workbook.v1 host', () => {
    open(leaf(cc()))
    const { result } = renderHook(() => useOwnWorkbook())
    expect(result.current).toBeNull()
    act(() => { seedWorkbook('h1', 'S1', { status: 'Doing x.' }) })
    expect(result.current).toEqual({ hostId: 'h1', sessionId: 'S1' })
  })
  it('a host without workbook.v1 shows none', () => {
    open(leaf(cc()))
    seedWorkbook('h1', 'S1', { status: 'x' }, { v1: false })
    const { result } = renderHook(() => useOwnWorkbook())
    expect(result.current).toBeNull()
  })
  it('asks the host once for the conversation (limit 1) when the store does not know it', () => {
    const load = vi.fn(async () => {})
    useWorkbookStore.setState({ loadSeat: load } as never)
    seedWorkbook('h1', 'other', {})
    open(leaf(cc()))
    renderHook(() => useOwnWorkbook())
    expect(load).toHaveBeenCalledWith('h1', 'S1')
  })
  it('no active tab, or a tab with no cc pane: none, nothing asked', () => {
    const load = vi.fn(async () => {})
    useWorkbookStore.setState({ loadSeat: load } as never)
    const { result } = renderHook(() => useOwnWorkbook())
    expect(result.current).toBeNull()
    open(leaf(cc({}, { type: 'codex', sessionId: 'S1' })))
    expect(result.current).toBeNull()
    expect(load).not.toHaveBeenCalled()
  })
})
