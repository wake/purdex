import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { purdexStorage, STORAGE_KEYS, syncManager } from '../lib/storage'

export const MIN_WIDTH = 120
export const MAX_WIDTH = 600

/** Worker list section height bounds in the wide activity bar (shell cleanup spec §4.1). */
export const WORKER_LIST_MIN = 96
export const WORKER_LIST_MAX = 800
export const WORKER_LIST_DEFAULT = 240

export type ActivityBarWidth = 'narrow' | 'wide'
export type TabPosition = 'top' | 'left' | 'both'

/**
 * Reserved key for Home row in `workspaceExpanded` (not a real workspace id).
 */
export const HOME_WS_KEY = 'home'

/**
 * Self-heal invariant: only `tabPosition='left'` requires `activityBarWidth='wide'`.
 * `both` keeps tabs reachable via the top tab bar, so narrow is valid there.
 * Called by persist's onRehydrateStorage; also exported for direct testing.
 */
export function healLayoutInvariant<T extends { activityBarWidth?: ActivityBarWidth; tabPosition?: TabPosition }>(state: T): T {
  if (state.tabPosition === 'left' && state.activityBarWidth === 'narrow') {
    state.activityBarWidth = 'wide'
  }
  return state
}

interface LayoutState {
  activityBarWidth: ActivityBarWidth
  tabPosition: TabPosition
  activityBarWideSize: number
  workspaceExpanded: Record<string, boolean>
  workerListOpen: boolean
  workerListHeight: number
  bottomNavCompact: boolean

  setActivityBarWidth: (width: ActivityBarWidth) => void
  toggleActivityBarWidth: () => void
  setTabPosition: (position: TabPosition) => void
  setActivityBarWideSize: (size: number) => void
  toggleWorkspaceExpanded: (wsId: string) => void
  reconcileWorkspaceExpanded: (liveWsIds: string[]) => void
  setWorkerListOpen: (open: boolean) => void
  toggleWorkerListOpen: () => void
  setWorkerListHeight: (height: number) => void
  setBottomNavCompact: (compact: boolean) => void
  toggleBottomNavCompact: () => void
}

function clampWidth(w: number): number {
  return Math.max(MIN_WIDTH, Math.min(MAX_WIDTH, w))
}

function clampWorkerListHeight(h: number): number {
  return Math.max(WORKER_LIST_MIN, Math.min(WORKER_LIST_MAX, h))
}

export const useLayoutStore = create<LayoutState>()(
  persist(
    (set) => ({
      activityBarWidth: 'narrow',
      tabPosition: 'top',
      activityBarWideSize: 240,
      workspaceExpanded: {},
      workerListOpen: false,
      workerListHeight: WORKER_LIST_DEFAULT,
      bottomNavCompact: false,

      setActivityBarWidth: (width) =>
        set((state) => {
          if (width === 'narrow' && state.tabPosition === 'left') return state
          return { activityBarWidth: width }
        }),

      toggleActivityBarWidth: () =>
        set((state) => {
          const next: ActivityBarWidth = state.activityBarWidth === 'narrow' ? 'wide' : 'narrow'
          if (next === 'narrow' && state.tabPosition === 'left') return state
          return { activityBarWidth: next }
        }),

      setTabPosition: (position) =>
        set((state) => {
          if (position === 'left') {
            return { tabPosition: 'left', activityBarWidth: 'wide' }
          }
          return { tabPosition: position, activityBarWidth: state.activityBarWidth }
        }),

      setActivityBarWideSize: (size) =>
        set(() => ({ activityBarWideSize: clampWidth(size) })),

      toggleWorkspaceExpanded: (wsId) =>
        set((state) => ({
          workspaceExpanded: {
            ...state.workspaceExpanded,
            [wsId]: !state.workspaceExpanded[wsId],
          },
        })),

      reconcileWorkspaceExpanded: (liveWsIds) =>
        set((state) => {
          const alive = new Set(liveWsIds)
          alive.add(HOME_WS_KEY)
          const next: Record<string, boolean> = {}
          let changed = false
          for (const [key, value] of Object.entries(state.workspaceExpanded)) {
            if (alive.has(key)) next[key] = value
            else changed = true
          }
          if (!changed) return state
          return { workspaceExpanded: next }
        }),

      setWorkerListOpen: (open) => set(() => ({ workerListOpen: open })),

      toggleWorkerListOpen: () =>
        set((state) => ({ workerListOpen: !state.workerListOpen })),

      setWorkerListHeight: (height) =>
        set(() => ({ workerListHeight: clampWorkerListHeight(height) })),

      setBottomNavCompact: (compact) => set(() => ({ bottomNavCompact: compact })),

      toggleBottomNavCompact: () =>
        set((state) => ({ bottomNavCompact: !state.bottomNavCompact })),
    }),
    {
      name: STORAGE_KEYS.LAYOUT,
      storage: purdexStorage,
      version: 1,
      partialize: (state) => ({
        activityBarWidth: state.activityBarWidth,
        tabPosition: state.tabPosition,
        activityBarWideSize: state.activityBarWideSize,
        workspaceExpanded: state.workspaceExpanded,
        // Device-local: synced across windows, never projected to Profile Sync (spec §4.1).
        workerListOpen: state.workerListOpen,
        workerListHeight: state.workerListHeight,
        bottomNavCompact: state.bottomNavCompact,
      }),
      onRehydrateStorage: () => (state) => {
        if (state) healLayoutInvariant(state)
      },
    },
  ),
)

syncManager.register(STORAGE_KEYS.LAYOUT, useLayoutStore)
