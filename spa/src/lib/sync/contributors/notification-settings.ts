// =============================================================================
// Sync Architecture — NotificationSettingsContributor
// =============================================================================

import { useNotificationSettingsStore } from '../../../stores/useNotificationSettingsStore'
import type { SyncContributor, FullPayload, MergeStrategy } from '../types'

// ---------------------------------------------------------------------------
// Data field list (non-function fields from NotificationSettingsState)
// ---------------------------------------------------------------------------

const DATA_FIELDS = ['agents'] as const

type NotificationSettingsData = {
  [K in (typeof DATA_FIELDS)[number]]: ReturnType<typeof useNotificationSettingsStore.getState>[K]
}

// ---------------------------------------------------------------------------
// Factory
// ---------------------------------------------------------------------------

export function createNotificationSettingsContributor(): SyncContributor {
  return {
    id: 'notification-settings',
    strategy: 'full',

    getVersion(): number {
      return 1
    },

    serialize(): FullPayload {
      const state = useNotificationSettingsStore.getState()
      const data: Record<string, unknown> = {}
      for (const field of DATA_FIELDS) {
        data[field] = state[field]
      }
      return { version: 1, data }
    },

    deserialize(payload: unknown, merge: MergeStrategy): void {
      const fp = payload as FullPayload
      const incoming = fp.data as Partial<NotificationSettingsData>

      if (merge.type === 'full-replace') {
        useNotificationSettingsStore.setState(incoming as NotificationSettingsData)
        return
      }

      // field-merge: only apply fields where resolved[field] === 'remote'
      const patch: Partial<NotificationSettingsData> = {}
      for (const field of DATA_FIELDS) {
        if (merge.resolved[field] === 'remote' && field in incoming) {
          ;(patch as Record<string, unknown>)[field] = incoming[field]
        }
      }

      if (Object.keys(patch).length > 0) {
        useNotificationSettingsStore.setState(patch as NotificationSettingsData)
      }
    },
  }
}
