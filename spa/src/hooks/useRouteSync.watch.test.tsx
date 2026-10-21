import { describe, it, expect, beforeEach, vi } from 'vitest'

vi.mock('../features/workspace/lib/icon-path-cache', () => ({
  getIconPath: () => null,
  isWeightLoaded: () => true,
  prefetchWeight: () => Promise.resolve(),
}))

import { renderHook } from '@testing-library/react'
import { createElement, type ReactNode } from 'react'
import { Router } from 'wouter'
import { memoryLocation } from 'wouter/memory-location'
import { useRouteSync } from './useRouteSync'
import { useTabStore } from '../stores/useTabStore'
import { useHistoryStore } from '../stores/useHistoryStore'
import { useWorkspaceStore, WATCH_WORKSPACE_ID } from '../features/workspace/store'
import { createTab, type PaneContent, type Tab } from '../types/tab'

const tabOf = (id: string, content: PaneContent): Tab => ({ ...createTab(content), id })
const wrapper = (mem: ReturnType<typeof memoryLocation>) =>
  function W({ children }: { children: ReactNode }) { return createElement(Router, { hook: mem.hook, children }) }

beforeEach(() => {
  useHistoryStore.setState({ browseHistory: [], closedTabs: [] })
  useWorkspaceStore.getState().reset()
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
})

describe('useRouteSync and the watcher area', () => {
  it('records no visit of a watch tab (it could be reopened outside the area), but does of an ordinary one', () => {
    const w = tabOf('watch-host-a', { kind: 'watch-host', poolHostId: 'a' })
    const d = tabOf('dash', { kind: 'dashboard' })
    useTabStore.setState({ tabs: { [w.id]: w, [d.id]: d }, tabOrder: [w.id, d.id], activeTabId: w.id })
    renderHook(() => useRouteSync(), { wrapper: wrapper(memoryLocation({ path: '/', record: true })) })
    expect(useHistoryStore.getState().browseHistory).toEqual([])
    useTabStore.setState({ activeTabId: d.id })
    // (the control: the same hook does record an ordinary tab)
    const { unmount } = renderHook(() => useRouteSync(), { wrapper: wrapper(memoryLocation({ path: '/', record: true })) })
    unmount()
    expect(useHistoryStore.getState().browseHistory.map((r) => r.tabId)).toContain('dash')
  })

  it('ignores the workspace-settings route of the area (the route id pattern, 6 base36 chars, already rejects `watch`)', () => {
    const ws = useWorkspaceStore.getState()
    const a = ws.addWorkspace('A')
    ws.ensureWatchWorkspace()
    ws.setActiveWorkspace(a.id)
    renderHook(() => useRouteSync(), { wrapper: wrapper(memoryLocation({ path: `/w/${WATCH_WORKSPACE_ID}/settings`, record: true })) })
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBe(a.id)
    expect(useWorkspaceStore.getState().workspaces.find((x) => x.id === WATCH_WORKSPACE_ID)!.tabs).toEqual([])
    expect(Object.values(useTabStore.getState().tabs).some((t) => t.layout.type === 'leaf' && t.layout.pane.content.kind === 'settings')).toBe(false)
  })
})
