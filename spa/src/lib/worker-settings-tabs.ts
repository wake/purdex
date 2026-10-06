// spa/src/lib/worker-settings-tabs.ts — the tabs of Settings → Worker (conversation entity spec §9).
// Appearance and Workers are built in; Exited, Dormant and Aigora plug in through `registerWorkerSettingsTab`.
import type { ComponentType } from 'react'

export interface WorkerSettingsTab {
  id: string
  labelKey: string
  order: number
  /** Host-scoped tabs get a host picker and receive hostId. Later tabs (Dormant, Aigora) plug in here (spec §9). */
  hostScoped: boolean
  component: ComponentType<{ hostId?: string }>
}

let tabs: WorkerSettingsTab[] = []

/** Replaces by id; the list stays sorted by `order`. */
export function registerWorkerSettingsTab(def: WorkerSettingsTab): void {
  tabs = [...tabs.filter((t) => t.id !== def.id), def].sort((a, b) => a.order - b.order)
}

export function getWorkerSettingsTabs(): WorkerSettingsTab[] {
  return tabs
}

export function clearWorkerSettingsTabs(): void {
  tabs = []
}
