// spa/src/stores/useDeviceStateStore.ts — this computer's identity and the
// device-state uploader's status (spec §3.5).
//
// Only `deviceName` (the user's override) is persisted. `defaultDeviceName` is
// resolved at startup and `status` is live uploader state; neither should
// survive a reload. The last-uploaded hash lives inside the uploader, not here.
import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { purdexStorage, STORAGE_KEYS, syncManager } from '../lib/storage'

export const DEVICE_NAME_MAX_CODE_POINTS = 64

export type DeviceStateStatusKind = 'idle' | 'no-target' | 'offline' | 'uploading' | 'ok' | 'error'

export interface DeviceStateStatus {
  kind: DeviceStateStatusKind
  at?: number
  hostId?: string
  message?: string
}

export interface DeviceStateState {
  /** User override; null = use `defaultDeviceName`. Persisted. */
  deviceName: string | null
  /** Resolved once at startup (Electron hostname or UA). Not persisted. */
  defaultDeviceName: string
  /** Uploader status. Not persisted. */
  status: DeviceStateStatus
  setDeviceName: (name: string | null) => void
  setStatus: (status: DeviceStateStatus) => void
}

/**
 * Trim; blank → null; longer than 64 code points → first 64 code points
 * (split by code point, so surrogate pairs such as emoji are never cut).
 * Mirrors the daemon's validation (trim, then 1–64 runes).
 */
export function normalizeDeviceName(name: string): string | null {
  const trimmed = name.trim()
  if (trimmed === '') return null
  const points = Array.from(trimmed)
  return points.length > DEVICE_NAME_MAX_CODE_POINTS
    ? points.slice(0, DEVICE_NAME_MAX_CODE_POINTS).join('')
    : trimmed
}

export function effectiveDeviceName(state: Pick<DeviceStateState, 'deviceName' | 'defaultDeviceName'>): string {
  return (
    normalizeDeviceName(state.deviceName ?? '') ?? normalizeDeviceName(state.defaultDeviceName) ?? 'Browser'
  )
}

export const useDeviceStateStore = create<DeviceStateState>()(
  persist(
    (set) => ({
      deviceName: null,
      defaultDeviceName: 'Browser',
      status: { kind: 'idle' },
      setDeviceName: (name) => set({ deviceName: name === null ? null : normalizeDeviceName(name) }),
      setStatus: (status) => set({ status }),
    }),
    {
      name: STORAGE_KEYS.DEVICE_STATE,
      storage: purdexStorage,
      version: 1,
      partialize: (state) => ({ deviceName: state.deviceName }),
    },
  ),
)

syncManager.register(STORAGE_KEYS.DEVICE_STATE, useDeviceStateStore)
