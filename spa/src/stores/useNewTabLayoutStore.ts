import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { purdexStorage, STORAGE_KEYS, syncManager } from '../lib/storage'
import type { LayoutPreset, PresetKey } from '../lib/resolve-preset'
import { mapColumnsKeepingOne } from '../lib/profile/host-identity'

export type { LayoutPreset, PresetKey }

const COL_COUNT: Record<PresetKey, number> = {
  '3col': 3,
  '2col': 2,
  '1col': 1,
}

/** Factory ensuring column-count invariant matches PresetKey. */
export function makePreset(enabled: boolean, colCount: number): LayoutPreset {
  return { enabled, columns: Array.from({ length: colCount }, () => []) }
}

interface ProviderInfo {
  id: string
  order: number
  disabled?: boolean
}

interface State {
  presets: Record<PresetKey, LayoutPreset>
  knownIds: string[]
  activeEditingPreset: PresetKey

  setEnabled: (preset: PresetKey, enabled: boolean) => void
  setEditing: (preset: PresetKey) => void
  placeModule: (preset: PresetKey, providerId: string, colIdx: number, rowIdx: number) => void
  placeModuleInShortest: (preset: PresetKey, providerId: string) => void
  removeModule: (preset: PresetKey, providerId: string) => void
  /**
   * Place every provider not yet known, in each preset's shortest column.
   * `placedAs(id)` names another id the same block may already be under (a
   * host's block kept under its wire id — host ownership §3.3): a provider
   * whose `placedAs` id is known or placed is not placed. Judged here, on the
   * state this action writes — never on a snapshot the caller took before.
   */
  ensureDefaults: (providers: ProviderInfo[], placedAs?: (id: string) => string) => void
  /** Remove ids from every preset and from knownIds (e.g. a removed host's block). */
  pruneIds: (ids: string[]) => void
  /**
   * Replace a retired id with its successors. Where `from` is placed, the
   * successors take its exact slot (same column/row) in that preset; if
   * `from` was only known (user removed it), successors become known but
   * unplaced, preserving the removal. Targets already present are not duplicated.
   */
  migrateId: (from: string, to: string[]) => void
  /**
   * Rename ids through `map` in every preset and in knownIds, in place (the
   * host re-resolve pass: `sessions:<wire id>` → `sessions:<local id>`). Where
   * a renamed id lands on one already present, ONE is kept in that preset —
   * or in knownIds — at its own place: the wire-form one (`sessions:d1_…`),
   * else the first (host ownership plan §0.11). Nothing renamed → no `set`.
   */
  renameIds: (map: (id: string) => string) => void
  reset: () => void
}

function initialState(): Pick<State, 'presets' | 'knownIds' | 'activeEditingPreset'> {
  return {
    presets: {
      '3col': makePreset(false, 3),
      '2col': makePreset(false, 2),
      '1col': makePreset(true, 1),
    },
    knownIds: [],
    activeEditingPreset: '1col',
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

function clonePreset(p: LayoutPreset): LayoutPreset {
  return { enabled: p.enabled, columns: p.columns.map((c) => [...c]) }
}

/**
 * Heal rehydrated state against shape corruption (missing keys, wrong column
 * count, non-array values). Mutates state in place (Zustand persist convention).
 * Called from `onRehydrateStorage` and exported for direct testing.
 */
export function healPresetState<
  T extends Partial<Pick<State, 'presets' | 'knownIds' | 'activeEditingPreset'>>,
>(state: T): void {
  // presets: reset entirely if not an object
  if (!state.presets || typeof state.presets !== 'object') {
    state.presets = initialState().presets as T['presets']
  } else {
    const presets = state.presets as Record<string, LayoutPreset | undefined>
    for (const key of ['3col', '2col', '1col'] as const) {
      const expectedLen = COL_COUNT[key]
      const p = presets[key]
      if (
        !p ||
        typeof p !== 'object' ||
        !Array.isArray(p.columns) ||
        p.columns.length !== expectedLen
      ) {
        presets[key] = makePreset(key === '1col', expectedLen)
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
    if ((state.presets as Record<string, LayoutPreset>)['1col'].enabled !== true) {
      ;(state.presets as Record<string, LayoutPreset>)['1col'].enabled = true
    }
  }

  if (!Array.isArray(state.knownIds)) {
    state.knownIds = [] as T['knownIds']
  } else {
    state.knownIds = state.knownIds.filter(
      (s): s is string => typeof s === 'string',
    ) as T['knownIds']
  }

  if (!['3col', '2col', '1col'].includes(state.activeEditingPreset as string)) {
    state.activeEditingPreset = '1col' as T['activeEditingPreset']
  }
}

/**
 * persist migrate. v1 → v2 renames the two fields (`profiles` → `presets`,
 * `activeEditingProfile` → `activeEditingPreset`) and drops the old keys. Only
 * keys the blob has are carried: a missing one stays missing (zustand's merge
 * keeps the in-memory value, `healPresetState` defaults it — as for a v1 blob
 * before). A `presets` already in a v1 blob wins. No healing here: it runs
 * after the merge (`onRehydrateStorage`), on the new names.
 */
function migratePersisted(persisted: unknown, from: number): unknown {
  if (from >= 2 || !persisted || typeof persisted !== 'object') return persisted
  const { profiles, activeEditingProfile, ...rest } = persisted as Record<string, unknown>
  const out: Record<string, unknown> = { ...rest }
  if (!('presets' in out) && profiles !== undefined) out.presets = profiles
  if (!('activeEditingPreset' in out) && activeEditingProfile !== undefined) out.activeEditingPreset = activeEditingProfile
  return out
}

/**
 * Insert `id` into `preset` at (colIdx, rowIdx). If `id` already exists
 * anywhere in the preset, remove it first. Handles same-column downward
 * moves via index compensation (caller passes pre-removal index).
 *
 * Compensation logic for same-column downward moves:
 * - After splicing out the item, indices >= fromRow shift down by 1
 * - Compensate only when toRow is strictly before end (toRow < target.length)
 * - "Move to end" case: toRow equals target.length after clamp → no compensation
 */
function placeIn(preset: LayoutPreset, id: string, colIdx: number, rowIdx: number): LayoutPreset {
  const next = clonePreset(preset)
  if (!next.columns[colIdx]) return preset // defensive

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

/**
 * `renameIds` as a pure step: the presets and knownIds with every id renamed
 * through `map`, where two land on one target only one kept (the wire-form
 * one, else the first — `mapColumnsKeepingOne`); `null` when nothing is renamed. An untouched preset
 * keeps its object.
 */
export function renameLayoutIds(
  state: Pick<State, 'presets' | 'knownIds'>,
  map: (id: string) => string,
): Pick<State, 'presets' | 'knownIds'> | null {
  const keys = ['3col', '2col', '1col'] as const
  // Anything to rename at all? Nothing → no new state.
  const targets = new Set<string>()
  for (const id of [...state.knownIds, ...keys.flatMap((k) => state.presets[k].columns.flat())]) {
    const to = map(id)
    if (to !== id) targets.add(to)
  }
  if (targets.size === 0) return null
  // Renamed per list; two different ids renamed onto one target keep one — the wire-form source, else the first.
  const presets = { ...state.presets }
  for (const key of keys) {
    const src = state.presets[key]
    const columns = mapColumnsKeepingOne(src.columns, map)
    const same = columns.every((col, i) => col.length === src.columns[i].length && col.every((id, j) => id === src.columns[i][j]))
    if (!same) presets[key] = { enabled: src.enabled, columns }
  }
  return { presets, knownIds: mapColumnsKeepingOne([state.knownIds], map)[0] }
}

export const useNewTabLayoutStore = create<State>()(
  persist(
    (set) => ({
      ...initialState(),

      setEnabled: (preset, enabled) =>
        set((state) => {
          if (preset === '1col' && !enabled) return state
          return {
            presets: {
              ...state.presets,
              [preset]: { ...state.presets[preset], enabled },
            },
          }
        }),

      setEditing: (preset) => set({ activeEditingPreset: preset }),

      placeModule: (preset, providerId, colIdx, rowIdx) =>
        set((state) => ({
          presets: {
            ...state.presets,
            [preset]: placeIn(state.presets[preset], providerId, colIdx, rowIdx),
          },
        })),

      placeModuleInShortest: (preset, providerId) =>
        set((state) => {
          const cols = state.presets[preset].columns
          const target = shortestColIdx(cols)
          return {
            presets: {
              ...state.presets,
              [preset]: placeIn(state.presets[preset], providerId, target, cols[target].length),
            },
          }
        }),

      removeModule: (preset, providerId) =>
        set((state) => {
          const next = clonePreset(state.presets[preset])
          let changed = false
          for (const col of next.columns) {
            const i = col.indexOf(providerId)
            if (i >= 0) {
              col.splice(i, 1)
              changed = true
            }
          }
          if (!changed) return state
          return { presets: { ...state.presets, [preset]: next } }
        }),

      ensureDefaults: (providers, placedAs) =>
        set((state) => {
          const known = new Set(state.knownIds)
          const placed = new Set<string>()
          for (const key of ['3col', '2col', '1col'] as const) {
            for (const col of state.presets[key].columns) col.forEach((id) => placed.add(id))
          }
          const present = new Set([...known, ...placed])
          const elsewhere = (id: string): boolean => {
            if (placedAs === undefined) return false
            const alt = placedAs(id)
            return alt !== id && present.has(alt)
          }
          const unknown = providers.filter((p) => !known.has(p.id) && !p.disabled)
          // knownIds is device-local and never synced: a block that arrived in a preset (or that the re-resolve pass
          // renamed there from its wire id) is placed without being known. Placed is placed — it only becomes known.
          const alreadyPlaced = unknown.filter((p) => placed.has(p.id))
          const newcomers = unknown
            .filter((p) => !placed.has(p.id) && !elsewhere(p.id))
            .sort((a, b) => a.order - b.order)
          if (newcomers.length === 0 && alreadyPlaced.length === 0) return state

          const presets = { ...state.presets }
          for (const key of ['3col', '2col', '1col'] as const) {
            presets[key] = clonePreset(presets[key])
          }
          const knownIds = [...state.knownIds, ...alreadyPlaced.map((p) => p.id)]

          for (const p of newcomers) {
            for (const key of ['3col', '2col', '1col'] as const) {
              const cols = presets[key].columns
              cols[shortestColIdx(cols)].push(p.id)
            }
            knownIds.push(p.id)
          }

          return { presets, knownIds }
        }),

      pruneIds: (ids) =>
        set((state) => {
          const drop = new Set(ids)
          const present =
            state.knownIds.some((id) => drop.has(id)) ||
            (['3col', '2col', '1col'] as const).some((k) =>
              state.presets[k].columns.some((col) => col.some((id) => drop.has(id))),
            )
          if (!present) return state
          const presets = { ...state.presets }
          for (const key of ['3col', '2col', '1col'] as const) {
            presets[key] = {
              enabled: state.presets[key].enabled,
              columns: state.presets[key].columns.map((col) => col.filter((id) => !drop.has(id))),
            }
          }
          return { presets, knownIds: state.knownIds.filter((id) => !drop.has(id)) }
        }),

      migrateId: (from, to) =>
        set((state) => {
          const keys = ['3col', '2col', '1col'] as const
          const isPlaced = keys.some((k) => state.presets[k].columns.some((col) => col.includes(from)))
          if (!isPlaced && !state.knownIds.includes(from)) return state

          const presets = { ...state.presets }
          for (const key of keys) {
            const src = state.presets[key]
            if (!src.columns.some((col) => col.includes(from))) continue
            const already = new Set(src.columns.flat())
            const insert = to.filter((id) => !already.has(id))
            presets[key] = {
              enabled: src.enabled,
              columns: src.columns.map((col) => col.flatMap((id) => (id === from ? insert : [id]))),
            }
          }

          const knownIds = state.knownIds.filter((id) => id !== from)
          for (const id of to) if (!knownIds.includes(id)) knownIds.push(id)
          return { presets, knownIds }
        }),

      renameIds: (map) =>
        set((state) => renameLayoutIds(state, map) ?? state),

      reset: () => set({ ...initialState() }),
    }),
    {
      name: STORAGE_KEYS.NEW_TAB_LAYOUT,
      storage: purdexStorage,
      // v2 (Profile Sync P3e): the fields were `profiles` / `activeEditingProfile`.
      version: 2,
      migrate: (persisted, from) => migratePersisted(persisted, from),
      partialize: (state) => ({
        presets: state.presets,
        knownIds: state.knownIds,
        activeEditingPreset: state.activeEditingPreset,
      }),
      onRehydrateStorage: () => (state) => {
        if (state) healPresetState(state)
      },
    },
  ),
)

syncManager.register(STORAGE_KEYS.NEW_TAB_LAYOUT, useNewTabLayoutStore)
