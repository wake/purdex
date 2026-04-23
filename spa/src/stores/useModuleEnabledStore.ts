import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { purdexStorage, STORAGE_KEYS } from '../lib/storage'
import { getModule } from '../lib/module-registry'

// AR-1 (codex adversarial review #617): persist payload sanitizer.
// Silently drops malformed entries (non-string keys, non-boolean values,
// prototype-polluting keys, non-object payloads) so a corrupted
// localStorage — user hand-edit, failed migration, serialization crash —
// cannot silently hide or un-hide module settings contributions.
const UNSAFE_KEYS = new Set(['__proto__', 'constructor', 'prototype'])

function sanitizeEnabled(raw: unknown): Record<string, boolean> {
  if (raw === null || typeof raw !== 'object') return {}
  const out: Record<string, boolean> = {}
  for (const [key, value] of Object.entries(raw as Record<string, unknown>)) {
    if (typeof key !== 'string' || key.length === 0) continue
    if (UNSAFE_KEYS.has(key)) continue
    if (typeof value !== 'boolean') continue
    out[key] = value
  }
  return out
}

/**
 * User-controlled enable/disable state for disableable modules.
 *
 * - `enabled`: persisted. Only entries the user has explicitly toggled. A
 *   module with `disableable: true` but no entry here falls back to `true`
 *   (default enabled). A module without `disableable: true` is always
 *   reported as enabled regardless of any stale entry here (persist-safe
 *   against flag flips — see spec §7 Q5).
 * - `baseline`: in-memory only (`partialize` strips it before persist).
 *   Captured once per session at the first dispatch and used by the
 *   switchboard to show "Reload required" when the user's live toggles
 *   differ from what the current session was booted with.
 *
 * Intentionally not registered with `syncManager` — toggling a module on or
 * off is a device-local preference (a host with limited resources can turn
 * off modules it doesn't want to run), not a config to sync between devices.
 */
interface ModuleEnabledState {
  enabled: Record<string, boolean>
  baseline: Record<string, boolean> | null

  setEnabled: (moduleId: string, value: boolean) => void
  resetAll: () => void
  captureBaseline: (snapshot: Record<string, boolean>) => void
  isEnabled: (moduleId: string) => boolean
  hasPendingChanges: () => boolean
}

export const useModuleEnabledStore = create<ModuleEnabledState>()(
  persist(
    (set, get) => ({
      enabled: {},
      baseline: null,

      setEnabled: (moduleId, value) =>
        set((state) => ({
          enabled: { ...state.enabled, [moduleId]: value },
        })),

      resetAll: () => set({ enabled: {} }),

      captureBaseline: (snapshot) => {
        // First-call wins — subsequent calls are no-ops so HMR re-runs of
        // `registerBuiltinModules()` don't overwrite the session baseline.
        if (get().baseline !== null) return
        set({ baseline: { ...snapshot } })
      },

      isEnabled: (moduleId) => {
        const override = get().enabled[moduleId]
        if (override !== undefined) {
          // Only honour an override when the module is actually disableable.
          // Prevents stale persist entries from disabling core modules if
          // someone flips `disableable` from true → false.
          const mod = getModule(moduleId)
          if (mod?.disableable === true) return override
          return true
        }
        // No override — default to enabled. Both disableable:true (user
        // hasn't opted out) and disableable:false/undefined (not toggleable)
        // resolve to true.
        return true
      },

      hasPendingChanges: () => {
        const { baseline } = get()
        if (baseline === null) return false
        const isEnabled = get().isEnabled
        for (const id of Object.keys(baseline)) {
          if (isEnabled(id) !== baseline[id]) return true
        }
        return false
      },
    }),
    {
      name: STORAGE_KEYS.MODULE_ENABLED,
      storage: purdexStorage,
      version: 1,
      // Baseline is an in-memory session artefact — persisting it would
      // defeat its purpose (baseline must reflect what the current session
      // booted with, not what some earlier session captured).
      partialize: (state) => ({ enabled: state.enabled }),
      merge: (persisted, current) => {
        // AR-1: sanitize every rehydrated payload. `merge` is the right hook
        // because it runs BEFORE the merged state becomes observable, so the
        // first `isEnabled()` call already sees the cleaned map.
        const p = persisted as { enabled?: unknown } | null | undefined
        const enabled = sanitizeEnabled(p?.enabled)
        return { ...current, enabled }
      },
    },
  ),
)
