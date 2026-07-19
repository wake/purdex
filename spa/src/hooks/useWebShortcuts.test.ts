import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { renderHook } from '@testing-library/react'
import { useWebShortcuts } from './useWebShortcuts'
import { useTabStore } from '../stores/useTabStore'
import { useWorkspaceStore } from '../stores/useWorkspaceStore'
import { useHistoryStore } from '../stores/useHistoryStore'

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

  it('global（Cmd+Alt+ArrowRight）於 editable target → 仍觸發', () => {
    // 先放兩個分頁以便 next-tab 有作用（只驗 preventDefault 觸發即可）
    renderHook(() => useWebShortcuts())
    const ta = document.createElement('textarea'); document.body.appendChild(ta)
    const prevented = fire(ta, { key: 'ArrowRight', metaKey: true, altKey: true })
    expect(prevented).toHaveBeenCalled()
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
