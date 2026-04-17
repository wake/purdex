import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import type { SidebarRegion } from '../types/layout'
import { purdexStorage, STORAGE_KEYS, syncManager } from '../lib/storage'
import { getAllViews } from '../lib/module-registry'

export const MIN_WIDTH = 120
export const MAX_WIDTH = 600

export type ActivityBarWidth = 'narrow' | 'wide'
export type TabPosition = 'top' | 'left'

/**
 * Reserved key for Home row in `workspaceExpanded` (not a real workspace id).
 */
export const HOME_WS_KEY = 'home'

/**
 * Self-heal invariant: `tabPosition='left'` requires `activityBarWidth='wide'`.
 * Called by persist's onRehydrateStorage; also exported for direct testing.
 */
export function healLayoutInvariant<T extends { activityBarWidth?: ActivityBarWidth; tabPosition?: TabPosition }>(state: T): T {
  if (state.tabPosition === 'left' && state.activityBarWidth === 'narrow') {
    state.activityBarWidth = 'wide'
  }
  return state
}

interface RegionState {
  views: string[]
  activeViewId?: string
  width: number
  mode: 'pinned' | 'collapsed' | 'hidden'
  previousMode?: 'pinned' | 'collapsed'
}

interface LayoutState {
  regions: Record<SidebarRegion, RegionState>
  activityBarWidth: ActivityBarWidth
  tabPosition: TabPosition
  activityBarWideSize: number
  workspaceExpanded: Record<string, boolean>

  setRegionMode: (region: SidebarRegion, mode: RegionState['mode']) => void
  setRegionWidth: (region: SidebarRegion, width: number) => void
  setActiveView: (region: SidebarRegion, viewId: string | undefined) => void
  setRegionViews: (region: SidebarRegion, views: string[]) => void
  toggleRegion: (region: SidebarRegion) => void
  toggleVisibility: (region: SidebarRegion) => void
  addView: (region: SidebarRegion, viewId: string) => void
  removeView: (region: SidebarRegion, viewId: string) => void
  reorderViews: (region: SidebarRegion, views: string[]) => void
  reconcileViews: () => void
  setActivityBarWidth: (width: ActivityBarWidth) => void
  toggleActivityBarWidth: () => void
  setTabPosition: (position: TabPosition) => void
  setActivityBarWideSize: (size: number) => void
  toggleWorkspaceExpanded: (wsId: string) => void
  reconcileWorkspaceExpanded: (liveWsIds: string[]) => void
}

function createDefaultRegions(): Record<SidebarRegion, RegionState> {
  return {
    'primary-sidebar': { views: [], width: 240, mode: 'collapsed' },
    'primary-panel': { views: [], width: 200, mode: 'collapsed' },
    'secondary-panel': { views: [], width: 200, mode: 'collapsed' },
    'secondary-sidebar': { views: [], width: 240, mode: 'collapsed' },
  }
}

function clampWidth(w: number): number {
  return Math.max(MIN_WIDTH, Math.min(MAX_WIDTH, w))
}

function updateRegion(
  state: LayoutState,
  region: SidebarRegion,
  patch: Partial<RegionState>,
): Partial<LayoutState> {
  return {
    regions: {
      ...state.regions,
      [region]: { ...state.regions[region], ...patch },
    },
  }
}

export const useLayoutStore = create<LayoutState>()(
  persist(
    (set) => ({
      regions: createDefaultRegions(),
      activityBarWidth: 'narrow',
      tabPosition: 'top',
      activityBarWideSize: 240,
      workspaceExpanded: {},

      setRegionMode: (region, mode) =>
        set((state) => updateRegion(state, region, { mode })),

      setRegionWidth: (region, width) =>
        set((state) => updateRegion(state, region, { width: clampWidth(width) })),

      setActiveView: (region, viewId) =>
        set((state) => updateRegion(state, region, { activeViewId: viewId })),

      setRegionViews: (region, views) =>
        set((state) => updateRegion(state, region, { views })),

      toggleRegion: (region) =>
        set((state) => {
          const current = state.regions[region].mode
          const next = current === 'collapsed' ? 'pinned' : 'collapsed'
          return updateRegion(state, region, { mode: next })
        }),

      toggleVisibility: (region) =>
        set((state) => {
          const { mode, previousMode } = state.regions[region]
          if (mode === 'hidden') {
            return updateRegion(state, region, {
              mode: previousMode ?? 'pinned',
              previousMode: undefined,
            })
          }
          return updateRegion(state, region, {
            mode: 'hidden',
            previousMode: mode,
          })
        }),

      addView: (region, viewId) =>
        set((state) => {
          const current = state.regions[region].views
          if (current.includes(viewId)) return state
          return updateRegion(state, region, { views: [...current, viewId] })
        }),

      removeView: (region, viewId) =>
        set((state) => {
          const { views, activeViewId } = state.regions[region]
          const next = views.filter((id) => id !== viewId)
          const patch: Partial<RegionState> = { views: next }
          if (activeViewId === viewId) {
            patch.activeViewId = next[0]
          }
          return updateRegion(state, region, patch)
        }),

      reorderViews: (region, newOrder) =>
        set((state) => {
          const current = state.regions[region].views
          const currentSet = new Set(current)
          const reordered = newOrder.filter((id) => currentSet.has(id))
          const reorderedSet = new Set(reordered)
          for (const id of current) {
            if (!reorderedSet.has(id)) reordered.push(id)
          }
          return updateRegion(state, region, { views: reordered })
        }),

      reconcileViews: () =>
        set((state) => {
          const validIds = new Set(getAllViews().map((v) => v.id))
          if (validIds.size === 0) return state
          const reconciled = { ...state.regions }
          for (const key of Object.keys(reconciled) as SidebarRegion[]) {
            const region = reconciled[key]
            const filtered = region.views.filter((id) => validIds.has(id))
            const activeViewId =
              region.activeViewId && filtered.includes(region.activeViewId)
                ? region.activeViewId
                : region.activeViewId !== undefined
                  ? filtered[0]
                  : undefined
            reconciled[key] = { ...region, views: filtered, activeViewId }
          }
          const allEmpty = Object.values(reconciled).every((r) => r.views.length === 0)
          if (allEmpty && validIds.has('file-tree-workspace')) {
            reconciled['primary-sidebar'] = {
              ...reconciled['primary-sidebar'],
              views: ['file-tree-workspace'],
              activeViewId: 'file-tree-workspace',
            }
          }
          return { regions: reconciled }
        }),

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
        set(() => {
          if (position === 'left') {
            return { tabPosition: 'left', activityBarWidth: 'wide' }
          }
          return { tabPosition: 'top' }
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
    }),
    {
      name: STORAGE_KEYS.LAYOUT,
      storage: purdexStorage,
      version: 1,
      partialize: (state) => ({
        regions: state.regions,
        activityBarWidth: state.activityBarWidth,
        tabPosition: state.tabPosition,
        activityBarWideSize: state.activityBarWideSize,
        workspaceExpanded: state.workspaceExpanded,
      }),
      onRehydrateStorage: () => (state) => {
        if (state) healLayoutInvariant(state)
      },
    },
  ),
)

syncManager.register(STORAGE_KEYS.LAYOUT, useLayoutStore)
