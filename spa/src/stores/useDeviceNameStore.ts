// spa/src/stores/useDeviceNameStore.ts — this computer's device name.
//
// Only `deviceName` (the user's override) is persisted; `defaultDeviceName` is
// resolved on demand by `ensureDefaultDeviceName()` and should not survive a
// reload. The storage key (`purdex-device-state`), version and persisted shape
// are those of the removed device-state store, so an existing name carries over.
import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { purdexStorage, STORAGE_KEYS, syncManager } from '../lib/storage'
import { normalizeDeviceName, resolveDefaultDeviceName } from '../lib/device-name'
import type { DeviceNameParts } from '../lib/device-name'

export interface DeviceNameState extends DeviceNameParts {
  setDeviceName: (name: string | null) => void
}

export const useDeviceNameStore = create<DeviceNameState>()(
  persist(
    (set) => ({
      deviceName: null,
      defaultDeviceName: 'Browser',
      setDeviceName: (name) => set({ deviceName: name === null ? null : normalizeDeviceName(name) }),
    }),
    {
      name: STORAGE_KEYS.DEVICE_STATE,
      storage: purdexStorage,
      version: 1,
      partialize: (state) => ({ deviceName: state.deviceName }),
    },
  ),
)

syncManager.register(STORAGE_KEYS.DEVICE_STATE, useDeviceNameStore)

let resolved = false
let inFlight: Promise<void> | null = null

async function resolveOnce(): Promise<void> {
  try {
    const name = await resolveDefaultDeviceName()
    resolved = true
    useDeviceNameStore.setState({ defaultDeviceName: normalizeDeviceName(name) ?? 'Browser' })
  } catch {
    // keep the store's fallback name; the next call retries
  }
}

/**
 * Resolve `defaultDeviceName` once. Idempotent and single-flight; never throws.
 * A failure keeps the fallback name and the next call retries. Nothing calls
 * this at module load — the caller decides when resolving is warranted.
 */
export function ensureDefaultDeviceName(): Promise<void> {
  if (resolved) return Promise.resolve()
  if (inFlight) return inFlight
  // Cleared in a chained `finally` (always a later microtask), so it cannot run
  // before the assignment even when `resolveOnce` settles without awaiting.
  inFlight = resolveOnce().finally(() => {
    inFlight = null
  })
  return inFlight
}

export function __resetDefaultDeviceNameForTest(): void {
  resolved = false
  inFlight = null
}
