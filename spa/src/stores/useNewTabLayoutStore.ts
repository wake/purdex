import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { purdexStorage, STORAGE_KEYS, syncManager } from '../lib/storage'
import type { Profile, ProfileKey } from '../lib/resolve-profile'

export type { Profile, ProfileKey }

const COL_COUNT: Record<ProfileKey, number> = {
  '3col': 3,
  '2col': 2,
  '1col': 1,
}

/** Factory ensuring column-count invariant matches ProfileKey. */
export function makeProfile(enabled: boolean, colCount: number): Profile {
  return { enabled, columns: Array.from({ length: colCount }, () => []) }
}

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
  placeModuleInShortest: (p: ProfileKey, providerId: string) => void
  removeModule: (p: ProfileKey, providerId: string) => void
  ensureDefaults: (providers: ProviderInfo[]) => void
  reset: () => void
}

function initialState(): Pick<State, 'profiles' | 'knownIds' | 'activeEditingProfile'> {
  return {
    profiles: {
      '3col': makeProfile(false, 3),
      '2col': makeProfile(false, 2),
      '1col': makeProfile(true, 1),
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
 * Heal rehydrated state against shape corruption (missing keys, wrong column
 * count, non-array values). Mutates state in place (Zustand persist convention).
 * Called from `onRehydrateStorage` and exported for direct testing.
 */
export function healProfileState<
  T extends Partial<Pick<State, 'profiles' | 'knownIds' | 'activeEditingProfile'>>,
>(state: T): void {
  // profiles: reset entirely if not an object
  if (!state.profiles || typeof state.profiles !== 'object') {
    state.profiles = initialState().profiles as T['profiles']
  } else {
    const profiles = state.profiles as Record<string, Profile | undefined>
    for (const key of ['3col', '2col', '1col'] as const) {
      const expectedLen = COL_COUNT[key]
      const p = profiles[key]
      if (
        !p ||
        typeof p !== 'object' ||
        !Array.isArray(p.columns) ||
        p.columns.length !== expectedLen
      ) {
        profiles[key] = makeProfile(key === '1col', expectedLen)
        continue
      }
      for (let i = 0; i < p.columns.length; i++) {
        if (!Array.isArray(p.columns[i])) {
          p.columns[i] = []
        } else {
          p.columns[i] = p.columns[i].filter((s): s is string => typeof s === 'string')
        }
      }
      if (typeof p.enabled !== 'boolean') {
        p.enabled = false
      }
    }
    // 1col lock invariant
    if ((state.profiles as Record<string, Profile>)['1col'].enabled !== true) {
      ;(state.profiles as Record<string, Profile>)['1col'].enabled = true
    }
  }

  if (!Array.isArray(state.knownIds)) {
    state.knownIds = [] as T['knownIds']
  } else {
    state.knownIds = state.knownIds.filter(
      (s): s is string => typeof s === 'string',
    ) as T['knownIds']
  }

  if (!['3col', '2col', '1col'].includes(state.activeEditingProfile as string)) {
    state.activeEditingProfile = '1col' as T['activeEditingProfile']
  }
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

      placeModuleInShortest: (p, providerId) =>
        set((state) => {
          const cols = state.profiles[p].columns
          const target = shortestColIdx(cols)
          return {
            profiles: {
              ...state.profiles,
              [p]: placeIn(state.profiles[p], providerId, target, cols[target].length),
            },
          }
        }),

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
      onRehydrateStorage: () => (state) => {
        if (state) healProfileState(state)
      },
    },
  ),
)

syncManager.register(STORAGE_KEYS.NEW_TAB_LAYOUT, useNewTabLayoutStore)
