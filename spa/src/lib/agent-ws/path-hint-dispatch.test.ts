import { describe, it, expect, beforeEach, vi } from 'vitest'
import { useTabStore } from '../../stores/useTabStore'
import { useWorkspaceStore } from '../../features/workspace/store'
import { usePathCacheStore } from '../../stores/path-cache/usePathCacheStore'
import type { Tab, PaneContent } from '../../types/tab'
import { handlePathHintEvent } from './path-hint-dispatch'

const seedTab = (id: string, content: PaneContent): Tab => ({
  id,
  pinned: false,
  locked: false,
  createdAt: 0,
  layout: { type: 'leaf', pane: { id: `p_${id}`, content } },
})

const v1 = (overrides: Record<string, unknown> = {}) =>
  JSON.stringify({
    schemaVersion: 1,
    agentId: 'cc',
    sessionCode: 'sess',
    dir: '/a/b',
    kind: 'read',
    timestamp: '2026-04-27T00:00:00Z',
    ...overrides,
  })

beforeEach(() => {
  usePathCacheStore.setState({ dirsByScope: {} } as never, false)
  const t = seedTab('t1', { kind: 'tmux-session', hostId: 'h1', sessionCode: 'sess', mode: 'terminal', cachedName: 'work', tmuxInstance: 'i1' })
  useTabStore.setState({ tabs: { t1: t }, tabOrder: ['t1'] } as never, false)
  useWorkspaceStore.setState({
    workspaces: [{ id: 'w1', name: 'A', tabs: ['t1'], activeTabId: 't1', moduleConfig: {} }],
    activeWorkspaceId: 'w1',
  } as never, false)
})

describe('handlePathHintEvent', () => {
  it('v1 payload adds dir to resolved workspace cache', () => {
    handlePathHintEvent('h1', { type: 'agent.path_hint', session: 'sess', value: v1() })
    expect(usePathCacheStore.getState().dirsByScope['h1:w1']).toEqual(['/a/b'])
  })

  it('schemaVersion !== 1 → drop', () => {
    handlePathHintEvent('h1', { type: 'agent.path_hint', session: 'sess', value: v1({ schemaVersion: 2 }) })
    expect(usePathCacheStore.getState().dirsByScope['h1:w1']).toBeUndefined()
  })

  it('non-absolute dir → drop', () => {
    handlePathHintEvent('h1', { type: 'agent.path_hint', session: 'sess', value: v1({ dir: 'rel/dir' }) })
    expect(usePathCacheStore.getState().dirsByScope['h1:w1']).toBeUndefined()
  })

  it('invalid kind → drop', () => {
    handlePathHintEvent('h1', { type: 'agent.path_hint', session: 'sess', value: v1({ kind: 'delete' }) })
    expect(usePathCacheStore.getState().dirsByScope['h1:w1']).toBeUndefined()
  })

  it('missing sessionCode → drop', () => {
    handlePathHintEvent('h1', { type: 'agent.path_hint', session: 'sess', value: v1({ sessionCode: '' }) })
    expect(usePathCacheStore.getState().dirsByScope['h1:w1']).toBeUndefined()
  })

  it('malformed JSON → drop without throwing', () => {
    expect(() =>
      handlePathHintEvent('h1', { type: 'agent.path_hint', session: 'sess', value: 'not-json' }),
    ).not.toThrow()
    expect(usePathCacheStore.getState().dirsByScope['h1:w1']).toBeUndefined()
  })

  it('non-object JSON (array / null / primitive) → drop', () => {
    handlePathHintEvent('h1', { type: 'agent.path_hint', session: 'sess', value: '[1,2,3]' })
    handlePathHintEvent('h1', { type: 'agent.path_hint', session: 'sess', value: 'null' })
    handlePathHintEvent('h1', { type: 'agent.path_hint', session: 'sess', value: '"bare"' })
    expect(usePathCacheStore.getState().dirsByScope['h1:w1']).toBeUndefined()
  })

  it('unresolvable workspace (no matching tab) → drop', () => {
    useTabStore.setState({ tabs: {}, tabOrder: [] } as never, false)
    handlePathHintEvent('h1', { type: 'agent.path_hint', session: 'sess', value: v1() })
    expect(Object.keys(usePathCacheStore.getState().dirsByScope)).toEqual([])
  })

  it('resolver throwing → does not crash dispatcher', async () => {
    const mod = await import('./resolve-workspace-id-for-agent-session')
    const spy = vi.spyOn(mod, 'resolveWorkspaceIdForAgentSession').mockImplementation(() => {
      throw new Error('boom')
    })
    expect(() =>
      handlePathHintEvent('h1', { type: 'agent.path_hint', session: 'sess', value: v1() }),
    ).not.toThrow()
    expect(usePathCacheStore.getState().dirsByScope['h1:w1']).toBeUndefined()
    spy.mockRestore()
  })

  it('multiple distinct dirs from different events accumulate (head first)', () => {
    handlePathHintEvent('h1', { type: 'agent.path_hint', session: 'sess', value: v1({ dir: '/a/b' }) })
    handlePathHintEvent('h1', { type: 'agent.path_hint', session: 'sess', value: v1({ dir: '/c/d' }) })
    expect(usePathCacheStore.getState().dirsByScope['h1:w1']).toEqual(['/c/d', '/a/b'])
  })
})
