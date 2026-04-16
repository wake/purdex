import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { purdexStorage, STORAGE_KEYS } from '../lib/storage'
import type { Profile, ProfileKey } from '../lib/resolve-profile'

export type { Profile, ProfileKey }

interface ProviderInfo {
  id: string
  order: number
  disabled?: boolean
}

interface State {
  profiles: Record<ProfileKey, Profile>
  knownIds: string[]
  activeEditingProfile: ProfileKey

  setEnabled: (p: ProfileKey, enabled: boolean) => void
  setEditing: (p: ProfileKey) => void
  placeModule: (p: ProfileKey, providerId: string, colIdx: number, rowIdx: number) => void
  removeModule: (p: ProfileKey, providerId: string) => void
  ensureDefaults: (providers: ProviderInfo[]) => void
  reset: () => void
}

function initialState(): Pick<State, 'profiles' | 'knownIds' | 'activeEditingProfile'> {
  return {
    profiles: {
      '3col': { enabled: false, columns: [[], [], []] },
      '2col': { enabled: false, columns: [[], []] },
      '1col': { enabled: true, columns: [[]] },
    },
    knownIds: [],
    activeEditingProfile: '1col',
  }
}

function clamp(n: number, lo: number, hi: number): number {
  return Math.max(lo, Math.min(hi, n))
}

function shortestColIdx(cols: string[][]): number {
  let best = 0
  for (let i = 1; i < cols.length; i++) {
    if (cols[i].length < cols[best].length) best = i
  }
  return best
}

function cloneProfile(p: Profile): Profile {
  return { enabled: p.enabled, columns: p.columns.map((c) => [...c]) }
}

/**
 * Insert `id` into `profile` at (colIdx, rowIdx). If `id` already exists
 * anywhere in the profile, remove it first. Handles same-column downward
 * moves via index compensation (caller passes pre-removal index).
 *
 * Compensation logic for same-column downward moves:
 * - After splicing out the item, indices >= fromRow shift down by 1
 * - Compensate only when toRow is strictly before end (toRow < target.length)
 * - "Move to end" case: toRow equals target.length after clamp → no compensation
 */
function placeIn(profile: Profile, id: string, colIdx: number, rowIdx: number): Profile {
  const next = cloneProfile(profile)
  if (!next.columns[colIdx]) return profile // defensive

  let fromCol = -1
  let fromRow = -1
  for (let c = 0; c < next.columns.length; c++) {
    const i = next.columns[c].indexOf(id)
    if (i >= 0) {
      fromCol = c
      fromRow = i
      next.columns[c].splice(i, 1)
      break
    }
  }

  const target = next.columns[colIdx]
  let toRow = clamp(rowIdx, 0, target.length)

  // Same-column downward move: caller passed pre-removal index. After splice,
  // target indices >= fromRow shifted down by 1 — compensate.
  // Only when toRow is strictly before the end (appending to end needs no shift).
  if (fromCol === colIdx && fromRow !== -1 && fromRow < toRow && toRow < target.length) {
    toRow -= 1
  }
  target.splice(toRow, 0, id)
  return next
}

export const useNewTabLayoutStore = create<State>()(
  persist(
    (set) => ({
      ...initialState(),

      setEnabled: (p, enabled) =>
        set((state) => {
          if (p === '1col' && !enabled) return state
          return {
            profiles: {
              ...state.profiles,
              [p]: { ...state.profiles[p], enabled },
            },
          }
        }),

      setEditing: (p) => set({ activeEditingProfile: p }),

      placeModule: (p, providerId, colIdx, rowIdx) =>
        set((state) => ({
          profiles: {
            ...state.profiles,
            [p]: placeIn(state.profiles[p], providerId, colIdx, rowIdx),
          },
        })),

      removeModule: (p, providerId) =>
        set((state) => {
          const next = cloneProfile(state.profiles[p])
          let changed = false
          for (const col of next.columns) {
            const i = col.indexOf(providerId)
            if (i >= 0) {
              col.splice(i, 1)
              changed = true
            }
          }
          if (!changed) return state
          return { profiles: { ...state.profiles, [p]: next } }
        }),

      ensureDefaults: (providers) =>
        set((state) => {
          const known = new Set(state.knownIds)
          const newcomers = providers
            .filter((p) => !known.has(p.id) && !p.disabled)
            .sort((a, b) => a.order - b.order)
          if (newcomers.length === 0) return state

          const profiles = { ...state.profiles }
          for (const key of ['3col', '2col', '1col'] as const) {
            profiles[key] = cloneProfile(profiles[key])
          }
          const knownIds = [...state.knownIds]

          for (const p of newcomers) {
            for (const key of ['3col', '2col', '1col'] as const) {
              const cols = profiles[key].columns
              cols[shortestColIdx(cols)].push(p.id)
            }
            knownIds.push(p.id)
          }

          return { profiles, knownIds }
        }),

      reset: () => set({ ...initialState() }),
    }),
    {
      name: STORAGE_KEYS.NEW_TAB_LAYOUT,
      storage: purdexStorage,
      version: 1,
      partialize: (state) => ({
        profiles: state.profiles,
        knownIds: state.knownIds,
        activeEditingProfile: state.activeEditingProfile,
      }),
    },
  ),
)

