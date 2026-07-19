import { describe, it, expect, beforeEach } from 'vitest'
import { executeShortcutAction } from './shortcut-actions'
import { useTabStore } from '../stores/useTabStore'
import { useWorkspaceStore } from '../stores/useWorkspaceStore'
import { useHistoryStore } from '../stores/useHistoryStore'

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
})
