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
 * `permissionTimeoutMin` (permission channel spec §5.5) is the exception: the
 * approval timeout is a per-client choice, so it is persisted (and shared with
 * this device's other windows) but deliberately NOT projected into Profile
 * Sync — precedent `useEditorSettingsStore`. It applies to new handoffs only.
 *
 * `iconStyle` / `customIcon` are consumed starting in phase C (§8.3); wired
 * here so the persisted shape does not change across phases.
 */
export type WorkerIconStyle = 'mono' | 'color' | 'custom'

/** 「等待核准逾時」 choices in minutes; 0 = never (the default, PC3). */
export const PERMISSION_TIMEOUT_MINUTES = [0, 5, 15, 30, 60] as const
export type PermissionTimeoutMin = (typeof PERMISSION_TIMEOUT_MINUTES)[number]

export interface WorkerSettingsState {
  theme: string
  iconStyle: WorkerIconStyle
  customIcon: string
  permissionTimeoutMin: PermissionTimeoutMin
  setTheme: (id: string) => void
  setIconStyle: (style: WorkerIconStyle) => void
  setCustomIcon: (icon: string) => void
  setPermissionTimeoutMin: (min: PermissionTimeoutMin) => void
}

/** The three synced fields' defaults (the Profile Sync tests compare against exactly these). */
export const DEFAULT_WORKER_SETTINGS = {
  theme: 'purdex',
  iconStyle: 'mono' as WorkerIconStyle,
  customIcon: '',
}

const DEFAULT_PERMISSION_TIMEOUT_MIN: PermissionTimeoutMin = 0

function isWorkerIconStyle(v: unknown): v is WorkerIconStyle {
  return v === 'mono' || v === 'color' || v === 'custom'
}

export function isPermissionTimeoutMin(v: unknown): v is PermissionTimeoutMin {
  return (PERMISSION_TIMEOUT_MINUTES as readonly unknown[]).includes(v)
}

type PersistedFields = Pick<WorkerSettingsState, 'theme' | 'iconStyle' | 'customIcon' | 'permissionTimeoutMin'>

/**
 * Silently replaces malformed entries with defaults — a corrupted
 * localStorage payload or a cross-version sync peer must not leave
 * `iconStyle` as some arbitrary string.
 */
function sanitize(raw: unknown): Partial<PersistedFields> {
  if (raw === null || typeof raw !== 'object') return {}
  const src = raw as Record<string, unknown>
  const out: Partial<PersistedFields> = {}
  if (typeof src.theme === 'string') out.theme = src.theme
  if (isWorkerIconStyle(src.iconStyle)) out.iconStyle = src.iconStyle
  // Anything non-string (a stale `null` from before this field was string-typed, a number, …) is left
  // unset here, so the `...DEFAULT_WORKER_SETTINGS` spread in `merge` below supplies `''`.
  if (typeof src.customIcon === 'string') out.customIcon = src.customIcon
  // Only an offered value survives; anything else (7, '15', null, …) falls back to 0 in `merge`.
  if (isPermissionTimeoutMin(src.permissionTimeoutMin)) out.permissionTimeoutMin = src.permissionTimeoutMin
  return out
}

export const useWorkerSettingsStore = create<WorkerSettingsState>()(
  persist(
    (set) => ({
      ...DEFAULT_WORKER_SETTINGS,
      permissionTimeoutMin: DEFAULT_PERMISSION_TIMEOUT_MIN,
      setTheme: (id) => set({ theme: id }),
      setIconStyle: (style) => set({ iconStyle: style }),
      setCustomIcon: (icon) => set({ customIcon: icon }),
      setPermissionTimeoutMin: (min) => set({ permissionTimeoutMin: isPermissionTimeoutMin(min) ? min : DEFAULT_PERMISSION_TIMEOUT_MIN }),
    }),
    {
      name: STORAGE_KEYS.WORKER_SETTINGS,
      storage: purdexStorage,
      version: 1,
      partialize: (state) => ({
        theme: state.theme,
        iconStyle: state.iconStyle,
        customIcon: state.customIcon,
        permissionTimeoutMin: state.permissionTimeoutMin,
      }),
      merge: (persisted, current) => {
        const clean = sanitize(persisted)
        return { ...current, ...DEFAULT_WORKER_SETTINGS, permissionTimeoutMin: DEFAULT_PERMISSION_TIMEOUT_MIN, ...clean }
      },
    },
  ),
)

syncManager.register(STORAGE_KEYS.WORKER_SETTINGS, useWorkerSettingsStore)
