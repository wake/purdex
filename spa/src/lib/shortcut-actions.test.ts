import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { executeShortcutAction } from './shortcut-actions'
import { useTabStore } from '../stores/useTabStore'
import { useWorkspaceStore } from '../stores/useWorkspaceStore'
import { useHistoryStore } from '../stores/useHistoryStore'
import { createTab } from '../types/tab'
import { registerTabShortcuts, clearTabShortcutRegistry } from './tab-shortcut-registry'

beforeEach(() => {
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
  useWorkspaceStore.getState().reset()
  useWorkspaceStore.getState().addWorkspace('Default')
  useHistoryStore.setState({ browseHistory: [], closedTabs: [] })
})

describe('executeShortcutAction', () => {
  it('new-tab → 新增 new-tab 分頁並 active', () => {
    executeShortcutAction('new-tab')
    const s = useTabStore.getState()
    expect(s.tabOrder.length).toBe(1)
    expect(s.activeTabId).toBe(s.tabOrder[0])
  })

  it('open-settings → 開 settings singleton 分頁', () => {
    executeShortcutAction('open-settings')
    const hasSettings = Object.values(useTabStore.getState().tabs).some((t) =>
      JSON.stringify(t.layout).includes('settings'))
    expect(hasSettings).toBe(true)
  })

  it('open-history / open-hosts → 各開對應 singleton', () => {
    executeShortcutAction('open-history')
    executeShortcutAction('open-hosts')
    const kinds = Object.values(useTabStore.getState().tabs).map((t) => JSON.stringify(t.layout))
    expect(kinds.some((k) => k.includes('history'))).toBe(true)
    expect(kinds.some((k) => k.includes('hosts'))).toBe(true)
  })

  it('switch-workspace-home → activeWorkspaceId 設為 null', () => {
    // 先建一個 workspace 並切入，再切回 home
    useWorkspaceStore.getState().addWorkspace('W2')
    const ws = useWorkspaceStore.getState().workspaces
    useWorkspaceStore.getState().setActiveWorkspace(ws[ws.length - 1].id)
    executeShortcutAction('switch-workspace-home')
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBeNull()
  })

  it('reopen-closed-tab 無歷史 → 不 throw、不新增分頁', () => {
    executeShortcutAction('reopen-closed-tab')
    expect(useTabStore.getState().tabOrder.length).toBe(0)
  })

  it('未知 action → 不 throw', () => {
    expect(() => executeShortcutAction('nonexistent-xyz')).not.toThrow()
  })

  it('switch-workspace-1 → 切到該 workspace 且同步其 active tab', () => {
    // Default (index 0) 有一顆 tab 並設為該 workspace 的 activeTab
    const defaultWsId = useWorkspaceStore.getState().activeWorkspaceId!
    const tab = createTab({ kind: 'new-tab' })
    useTabStore.getState().addTab(tab)
    useWorkspaceStore.getState().addTabToWorkspace(defaultWsId, tab.id)
    useWorkspaceStore.getState().setWorkspaceActiveTab(defaultWsId, tab.id)

    // 切到另一個 workspace，讓 activeWorkspaceId 先偏離 index 0
    const ws2 = useWorkspaceStore.getState().addWorkspace('W2')
    useWorkspaceStore.getState().setActiveWorkspace(ws2.id)

    executeShortcutAction('switch-workspace-1')

    expect(useWorkspaceStore.getState().activeWorkspaceId).toBe(defaultWsId)
    expect(useTabStore.getState().activeTabId).toBe(tab.id)
    const defaultWs = useWorkspaceStore.getState().workspaces.find((w) => w.id === defaultWsId)
    expect(defaultWs?.activeTabId).toBe(tab.id)
  })

  it('next-workspace → activeWorkspaceId 移到下一個 workspace', () => {
    const ws2 = useWorkspaceStore.getState().addWorkspace('W2')

    executeShortcutAction('next-workspace')

    expect(useWorkspaceStore.getState().activeWorkspaceId).toBe(ws2.id)
  })

  it('prev-workspace → activeWorkspaceId 移到前一個 workspace（環繞）', () => {
    const ws1Id = useWorkspaceStore.getState().activeWorkspaceId!
    useWorkspaceStore.getState().addWorkspace('W2')

    executeShortcutAction('prev-workspace')

    // 只有 2 個 workspace，prev 從 index 0 環繞到最後一個（index 1）
    const workspaces = useWorkspaceStore.getState().workspaces
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBe(workspaces[1].id)
    expect(useWorkspaceStore.getState().activeWorkspaceId).not.toBe(ws1Id)
  })

  describe('unknown action DEV warn', () => {
    afterEach(() => vi.restoreAllMocks())

    it('未知 action 在 DEV 模式下 console.warn，且不 mutate tab/workspace 狀態', () => {
      // vitest 預設以 DEV 模式跑（import.meta.env.DEV === true），故可直接驗證 warn。
      const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {})
      const tabOrderBefore = useTabStore.getState().tabOrder
      const activeWsBefore = useWorkspaceStore.getState().activeWorkspaceId

      executeShortcutAction('totally-bogus-action')

      expect(warnSpy).toHaveBeenCalledWith(expect.stringContaining('totally-bogus-action'))
      expect(useTabStore.getState().tabOrder).toEqual(tabOrderBefore)
      expect(useWorkspaceStore.getState().activeWorkspaceId).toBe(activeWsBefore)
    })
  })

  describe('tab-level shortcut registry dispatch', () => {
    afterEach(() => clearTabShortcutRegistry())

    it('active tab 的 kind 有註冊 handler → 呼叫該 handler', () => {
      const handler = vi.fn()
      registerTabShortcuts('browser', { reload: handler })

      const tab = createTab({ kind: 'browser', url: 'https://example.com' })
      useTabStore.getState().addTab(tab)
      useTabStore.getState().setActiveTab(tab.id)
      const wsId = useWorkspaceStore.getState().activeWorkspaceId!
      useWorkspaceStore.getState().addTabToWorkspace(wsId, tab.id)

      executeShortcutAction('reload')

      expect(handler).toHaveBeenCalledOnce()
      expect(handler).toHaveBeenCalledWith(
        tab,
        expect.objectContaining({ content: { kind: 'browser', url: 'https://example.com' } }),
      )
    })
  })
})
