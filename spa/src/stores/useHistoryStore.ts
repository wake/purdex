import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import type { Tab, PaneContent } from '../types/tab'

// === Types ===

interface BrowseRecord {
  tabId: string
  paneContent: PaneContent
  visitedAt: number
}

interface ClosedTabRecord {
  tab: Tab
  closedAt: number
  fromWorkspaceId?: string
  reopenedAt?: number
}

interface HistoryState {
  browseHistory: BrowseRecord[]
  closedTabs: ClosedTabRecord[]

  recordVisit(tabId: string, content: PaneContent): void
  recordClose(tab: Tab, workspaceId?: string): void
  reopenLast(): Tab | null
  clearBrowseHistory(): void
  clearClosedTabs(): void
}

const BROWSE_HISTORY_MAX = 500
const CLOSED_TABS_MAX = 100

// === Store ===

export const useHistoryStore = create<HistoryState>()(
  persist(
    (set, get) => ({
      browseHistory: [],
      closedTabs: [],

      recordVisit(tabId, paneContent) {
        set((s) => {
          const record: BrowseRecord = { tabId, paneContent, visitedAt: Date.now() }
          const next = [...s.browseHistory, record]
          return {
            browseHistory:
              next.length > BROWSE_HISTORY_MAX
                ? next.slice(next.length - BROWSE_HISTORY_MAX)
                : next,
          }
        })
      },

      recordClose(tab, workspaceId) {
        set((s) => {
          const record: ClosedTabRecord = {
            tab,
            closedAt: Date.now(),
            fromWorkspaceId: workspaceId,
          }
          const next = [...s.closedTabs, record]
          return {
            closedTabs:
              next.length > CLOSED_TABS_MAX
                ? next.slice(next.length - CLOSED_TABS_MAX)
                : next,
          }
        })
      },

      reopenLast() {
        const { closedTabs } = get()
        // Find the most recent unreopened record (scan from the end)
        for (let i = closedTabs.length - 1; i >= 0; i--) {
          if (closedTabs[i].reopenedAt === undefined) {
            const now = Date.now()
            set((s) => ({
              closedTabs: s.closedTabs.map((r, idx) =>
                idx === i ? { ...r, reopenedAt: now } : r,
              ),
            }))
            return closedTabs[i].tab
          }
        }
        return null
      },

      clearBrowseHistory() {
        set({ browseHistory: [] })
      },

      clearClosedTabs() {
        set({ closedTabs: [] })
      },
    }),
    {
      name: 'tbox-v2-history',
      version: 1,
    },
  ),
)
