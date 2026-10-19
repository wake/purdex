// spa/src/stores/useTabMenuExtrasStore.ts — extra rows a surface puts at the top of the tab context menu for the tab it was opened on (name pool v2 spec §7, NA-6c: the task-group view adds 「加入任務編組」 and the like).
// The surface sets them as the right-click bubbles up from its row — after the tab menu's own handler (`handleContextMenu`) has cleared whatever an earlier menu left — and `TabContextMenu` reads them for ITS tab only
// and clears them when it closes, so a menu opened from elsewhere never shows a stale row. Not persisted.
import { create } from 'zustand'

export interface TabMenuExtra {
  key: string
  label: string
  onSelect: () => void
  disabled?: boolean
  /** Why it is disabled (the tooltip). */
  title?: string
}

interface State {
  tabId: string | null
  items: TabMenuExtra[]
  set: (tabId: string, items: TabMenuExtra[]) => void
  clear: () => void
}

export const useTabMenuExtrasStore = create<State>()((set) => ({
  tabId: null,
  items: [],
  set: (tabId, items) => set({ tabId, items }),
  clear: () => set((s) => (s.tabId === null && s.items.length === 0 ? s : { tabId: null, items: [] })),
}))
