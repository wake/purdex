import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { purdexStorage, STORAGE_KEYS, syncManager } from '../lib/storage'

/**
 * Worker pane theme + icon setting (worker theme spec §4.2): one purdex-scope
 * global setting (T1 — not per pane, not two-level), under the execution
 * module's "Worker → Appearance" settings section. `theme` is a worker theme
 * registry id (`lib/worker-theme/registry.ts`); an unregistered id is not
 * rejected here — `getWorkerTheme` falls back to `purdex` at read time, so a
 * theme a sync peer no longer has registered degrades gracefully instead of
 * losing the rest of the preference.
 *
 * Sync: registered with `syncManager` (other windows of this device) and
 * projected into the Profile Sync `settings` section like the other
 * appearance stores (`lib/profile/projections.ts`, settings ordinal 9) —
 * `theme`, `iconStyle` and `customIcon` all travel. `customIcon` is a plain
 * `string`, never `null` — `''` means "no custom icon" — so it is one shape
 * class throughout and the settings applier's shape check (`applier.ts`
 * `shapeOf`) never rejects it the way a nullable field would.
 *
 * `iconStyle` / `customIcon` are consumed starting in phase C (§8.3); wired
 * here so the persisted shape does not change across phases.
 */
export type WorkerIconStyle = 'mono' | 'color' | 'custom'

export interface WorkerSettingsState {
  theme: string
  iconStyle: WorkerIconStyle
  customIcon: string
  setTheme: (id: string) => void
  setIconStyle: (style: WorkerIconStyle) => void
  setCustomIcon: (icon: string) => void
}

export const DEFAULT_WORKER_SETTINGS = {
  theme: 'purdex',
  iconStyle: 'mono' as WorkerIconStyle,
  customIcon: '',
}

function isWorkerIconStyle(v: unknown): v is WorkerIconStyle {
  return v === 'mono' || v === 'color' || v === 'custom'
}

/**
 * Silently replaces malformed entries with defaults — a corrupted
 * localStorage payload or a cross-version sync peer must not leave
 * `iconStyle` as some arbitrary string.
 */
function sanitize(raw: unknown): Partial<Pick<WorkerSettingsState, 'theme' | 'iconStyle' | 'customIcon'>> {
  if (raw === null || typeof raw !== 'object') return {}
  const src = raw as Record<string, unknown>
  const out: Partial<WorkerSettingsState> = {}
  if (typeof src.theme === 'string') out.theme = src.theme
  if (isWorkerIconStyle(src.iconStyle)) out.iconStyle = src.iconStyle
  // Anything non-string (a stale `null` from before this field was string-typed, a number, …) is left
  // unset here, so the `...DEFAULT_WORKER_SETTINGS` spread in `merge` below supplies `''`.
  if (typeof src.customIcon === 'string') out.customIcon = src.customIcon
  return out
}

export const useWorkerSettingsStore = create<WorkerSettingsState>()(
  persist(
    (set) => ({
      ...DEFAULT_WORKER_SETTINGS,
      setTheme: (id) => set({ theme: id }),
      setIconStyle: (style) => set({ iconStyle: style }),
      setCustomIcon: (icon) => set({ customIcon: icon }),
    }),
    {
      name: STORAGE_KEYS.WORKER_SETTINGS,
      storage: purdexStorage,
      version: 1,
      partialize: (state) => ({ theme: state.theme, iconStyle: state.iconStyle, customIcon: state.customIcon }),
      merge: (persisted, current) => {
        const clean = sanitize(persisted)
        return { ...current, ...DEFAULT_WORKER_SETTINGS, ...clean }
      },
    },
  ),
)

syncManager.register(STORAGE_KEYS.WORKER_SETTINGS, useWorkerSettingsStore)
