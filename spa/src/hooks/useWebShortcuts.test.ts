import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { renderHook } from '@testing-library/react'
import { useWebShortcuts } from './useWebShortcuts'
import { useTabStore } from '../stores/useTabStore'
import { useWorkspaceStore } from '../stores/useWorkspaceStore'
import { useHistoryStore } from '../stores/useHistoryStore'
import { createTab } from '../types/tab'

beforeEach(() => {
  delete (window as unknown as { electronAPI?: unknown }).electronAPI
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
  useWorkspaceStore.getState().reset()
  useWorkspaceStore.getState().addWorkspace('Default')
  useHistoryStore.setState({ browseHistory: [], closedTabs: [] })
})
afterEach(() => { vi.restoreAllMocks() })

function fire(target: EventTarget, init: KeyboardEventInit) {
  const e = new KeyboardEvent('keydown', { ...init, cancelable: true, bubbles: true })
  const prevented = vi.spyOn(e, 'preventDefault')
  target.dispatchEvent(e)
  return prevented
}

describe('useWebShortcuts', () => {
  it('web：Cmd+, 於非 editable → 執行 open-settings + preventDefault', () => {
    renderHook(() => useWebShortcuts())
    const prevented = fire(window, { key: ',', metaKey: true })
    expect(prevented).toHaveBeenCalled()
    expect(Object.values(useTabStore.getState().tabs).some((t) =>
      JSON.stringify(t.layout).includes('settings'))).toBe(true)
  })

  it('非 global（Cmd+Y）於 editable target（textarea）→ 放行、不執行', () => {
    renderHook(() => useWebShortcuts())
    const ta = document.createElement('textarea')
    document.body.appendChild(ta)
    const prevented = fire(ta, { key: 'y', metaKey: true })
    expect(prevented).not.toHaveBeenCalled()
    expect(Object.values(useTabStore.getState().tabs).some((t) =>
      JSON.stringify(t.layout).includes('history'))).toBe(false)
    ta.remove()
  })

  it('global（Cmd+Alt+ArrowRight）於 editable target → 仍觸發，activeTabId 實際切換', () => {
    // 放兩個分頁，驗證 next-tab 真的執行（不只是 preventDefault）
    const tab1 = createTab({ kind: 'new-tab' })
    const tab2 = createTab({ kind: 'new-tab' })
    useTabStore.getState().addTab(tab1)
    useTabStore.getState().addTab(tab2)
    useTabStore.getState().setActiveTab(tab1.id)
    const wsId = useWorkspaceStore.getState().activeWorkspaceId!
    useWorkspaceStore.getState().addTabToWorkspace(wsId, tab1.id)
    useWorkspaceStore.getState().addTabToWorkspace(wsId, tab2.id)

    renderHook(() => useWebShortcuts())
    const ta = document.createElement('textarea'); document.body.appendChild(ta)
    const prevented = fire(ta, { key: 'ArrowRight', metaKey: true, altKey: true })
    expect(prevented).toHaveBeenCalled()
    expect(useTabStore.getState().activeTabId).toBe(tab2.id)
    ta.remove()
  })

  it('未對映鍵 → 不 preventDefault', () => {
    renderHook(() => useWebShortcuts())
    const prevented = fire(window, { key: 'q', metaKey: true })
    expect(prevented).not.toHaveBeenCalled()
  })

  it('Electron → 不掛 keydown', () => {
    ;(window as unknown as { electronAPI?: unknown }).electronAPI = { onShortcut: () => () => {} }
    renderHook(() => useWebShortcuts())
    const prevented = fire(window, { key: ',', metaKey: true })
    expect(prevented).not.toHaveBeenCalled()
  })

  it('unmount 後不再觸發', () => {
    const { unmount } = renderHook(() => useWebShortcuts())
    unmount()
    const prevented = fire(window, { key: ',', metaKey: true })
    expect(prevented).not.toHaveBeenCalled()
  })
})
