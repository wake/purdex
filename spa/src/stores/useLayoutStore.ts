import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import type { SidebarRegion } from '../types/tab'
import { purdexStorage, STORAGE_KEYS, syncManager } from '../lib/storage'

const MIN_WIDTH = 120
const MAX_WIDTH = 600

interface RegionState {
  views: string[]
  activeViewId?: string
  width: number
  mode: 'pinned' | 'collapsed' | 'hidden'
  previousMode?: 'pinned' | 'collapsed'
}

interface LayoutState {
  regions: Record<SidebarRegion, RegionState>

  setRegionMode: (region: SidebarRegion, mode: RegionState['mode']) => void
  setRegionWidth: (region: SidebarRegion, width: number) => void
  setActiveView: (region: SidebarRegion, viewId: string | undefined) => void
  setRegionViews: (region: SidebarRegion, views: string[]) => void
  toggleRegion: (region: SidebarRegion) => void
  toggleVisibility: (region: SidebarRegion) => void
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
    }),
    {
      name: STORAGE_KEYS.LAYOUT,
      storage: purdexStorage,
      version: 1,
    },
  ),
)

syncManager.register(STORAGE_KEYS.LAYOUT, useLayoutStore)
