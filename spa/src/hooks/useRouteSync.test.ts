import { describe, it, expect, beforeEach } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import type { ReactNode } from 'react'
import { createElement } from 'react'
import { Router } from 'wouter'
import { memoryLocation } from 'wouter/memory-location'
import { useRouteSync } from './useRouteSync'
import { useTabStore } from '../stores/useTabStore'
import { getPrimaryPane } from '../lib/pane-tree'
import type { Tab } from '../types/tab'

function makeTab(id: string, contentKind: 'session' | 'dashboard' | 'history', mode?: 'terminal' | 'stream'): Tab {
  const content = contentKind === 'session'
    ? { kind: 'session' as const, sessionCode: 'test', mode: mode ?? 'terminal' as const }
    : { kind: contentKind as 'dashboard' | 'history' }
  return {
    id,
    pinned: false,
    locked: false,
    createdAt: Date.now(),
    layout: { type: 'leaf', pane: { id: `pane-${id}`, content } },
  }
}

function resetStore(data?: { tabs?: Record<string, Tab>; tabOrder?: string[]; activeTabId?: string | null }) {
  // Use merge mode (no second arg) so zustand action methods are preserved
  useTabStore.setState({
    tabs: data?.tabs ?? {},
    tabOrder: data?.tabOrder ?? [],
    activeTabId: data?.activeTabId ?? null,
  })
}

function createWrapper(mem: ReturnType<typeof memoryLocation>) {
  return function Wrapper({ children }: { children: ReactNode }) {
    return createElement(Router, { hook: mem.hook, children })
  }
}

describe('useRouteSync', () => {
  beforeEach(() => {
    resetStore()
  })

  it('singleton route /history opens a history tab', () => {
    const mem = memoryLocation({ path: '/history', record: true })

    renderHook(() => useRouteSync(), { wrapper: createWrapper(mem) })

    // openSingletonTab should have created a tab with history content
    const state = useTabStore.getState()
    expect(state.tabOrder.length).toBeGreaterThanOrEqual(1)
    const tabId = state.activeTabId!
    expect(tabId).toBeTruthy()
    const tab = state.tabs[tabId]
    const primary = getPrimaryPane(tab.layout)
    expect(primary.content.kind).toBe('history')
  })

  it('session route /t/abc123/terminal with existing tab activates it', () => {
    const tab = makeTab('abc123', 'session', 'terminal')
    resetStore({
      tabs: { abc123: tab },
      tabOrder: ['abc123'],
      activeTabId: null,
    })

    const mem = memoryLocation({ path: '/t/abc123/terminal', record: true })
    renderHook(() => useRouteSync(), { wrapper: createWrapper(mem) })

    expect(useTabStore.getState().activeTabId).toBe('abc123')
  })

  it('session route with missing tab sets activeTabId to null', () => {
    // No active tab — navigating to a nonexistent session tab should leave activeTabId null
    resetStore({
      tabs: {},
      tabOrder: [],
      activeTabId: null,
    })

    const mem = memoryLocation({ path: '/t/abc123/terminal', record: true })
    renderHook(() => useRouteSync(), { wrapper: createWrapper(mem) })

    expect(useTabStore.getState().activeTabId).toBeNull()
  })

  it('tab activation updates URL', () => {
    const tab = makeTab('abc123', 'session', 'terminal')
    resetStore({
      tabs: { abc123: tab },
      tabOrder: ['abc123'],
      activeTabId: null,
    })

    const mem = memoryLocation({ path: '/', record: true })
    renderHook(() => useRouteSync(), { wrapper: createWrapper(mem) })

    // Activate tab
    act(() => {
      useTabStore.getState().setActiveTab('abc123')
    })

    expect(mem.history).toContain('/t/abc123/terminal')
  })

  it('viewMode change updates URL', () => {
    const tab = makeTab('abc123', 'session', 'terminal')
    resetStore({
      tabs: { abc123: tab },
      tabOrder: ['abc123'],
      activeTabId: 'abc123',
    })

    const mem = memoryLocation({ path: '/t/abc123/terminal', record: true })
    renderHook(() => useRouteSync(), { wrapper: createWrapper(mem) })

    // Change view mode
    act(() => {
      useTabStore.getState().setViewMode('abc123', 'pane-abc123', 'stream')
    })

    expect(mem.history).toContain('/t/abc123/stream')
  })

  it('dashboard route opens dashboard singleton', () => {
    const mem = memoryLocation({ path: '/', record: true })

    renderHook(() => useRouteSync(), { wrapper: createWrapper(mem) })

    const state = useTabStore.getState()
    expect(state.tabOrder.length).toBeGreaterThanOrEqual(1)
    const tabId = state.activeTabId!
    expect(tabId).toBeTruthy()
    const tab = state.tabs[tabId]
    const primary = getPrimaryPane(tab.layout)
    expect(primary.content.kind).toBe('dashboard')
  })

  it('invalid ID format in URL does not activate any tab', () => {
    const mem = memoryLocation({ path: '/t/INVALID/terminal', record: true })

    renderHook(() => useRouteSync(), { wrapper: createWrapper(mem) })

    // parseRoute returns null for invalid IDs — no tab should be activated
    expect(useTabStore.getState().activeTabId).toBeNull()
  })
})
