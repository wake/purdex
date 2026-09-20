// spa/src/stores/useDeviceStateStore.ts — the device-state uploader's status
// (spec §3.5). Live state only: nothing here is persisted.
//
// The device name moved to `useDeviceNameStore` / `lib/device-name.ts`, which
// survive this module's removal (Profile Sync P4b); they are re-exported below
// for the device-state callers that remain until then.
import { create } from 'zustand'

export { DEVICE_NAME_MAX_CODE_POINTS, effectiveDeviceName, normalizeDeviceName } from '../lib/device-name'
export { ensureDefaultDeviceName, useDeviceNameStore } from './useDeviceNameStore'

export type DeviceStateStatusKind = 'idle' | 'no-target' | 'offline' | 'uploading' | 'ok' | 'error'

export interface DeviceStateStatus {
  kind: DeviceStateStatusKind
  at?: number
  hostId?: string
  message?: string
}

export interface DeviceStateState {
  /** Uploader status. Not persisted. */
  status: DeviceStateStatus
  setStatus: (status: DeviceStateStatus) => void
}

export const useDeviceStateStore = create<DeviceStateState>()((set) => ({
  status: { kind: 'idle' },
  setStatus: (status) => set({ status }),
}))
