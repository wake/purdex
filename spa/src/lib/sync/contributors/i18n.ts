// =============================================================================
// Sync Architecture — I18nContributor
// =============================================================================

import { useI18nStore } from '../../../stores/useI18nStore'
import type { SyncContributor, FullPayload, MergeStrategy } from '../types'

// ---------------------------------------------------------------------------
// Data field list (non-function fields from I18nState; excludes `t` getter)
// ---------------------------------------------------------------------------

const DATA_FIELDS = ['activeLocaleId', 'customLocales'] as const

type I18nData = {
  [K in (typeof DATA_FIELDS)[number]]: ReturnType<typeof useI18nStore.getState>[K]
}

// ---------------------------------------------------------------------------
// Factory
// ---------------------------------------------------------------------------

export function createI18nContributor(): SyncContributor {
  return {
    id: 'i18n',
    strategy: 'full',

    getVersion(): number {
      return 1
    },

    serialize(): FullPayload {
      const state = useI18nStore.getState()
      const data: Record<string, unknown> = {}
      for (const field of DATA_FIELDS) {
        data[field] = state[field]
      }
      return { version: 1, data }
    },

    deserialize(payload: unknown, merge: MergeStrategy): void {
      const fp = payload as FullPayload
      const incoming = fp.data as Partial<I18nData>

      if (merge.type === 'full-replace') {
        useI18nStore.setState(incoming as I18nData)
        return
      }

      // field-merge: only apply fields where resolved[field] === 'remote'
      const patch: Partial<I18nData> = {}
      for (const field of DATA_FIELDS) {
        if (merge.resolved[field] === 'remote' && field in incoming) {
          ;(patch as Record<string, unknown>)[field] = incoming[field]
        }
      }

      if (Object.keys(patch).length > 0) {
        useI18nStore.setState(patch as I18nData)
      }
    },
  }
}
