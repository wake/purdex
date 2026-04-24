// =============================================================================
// Sync Architecture — PreferencesContributor
// =============================================================================

import { useUISettingsStore } from '../../../stores/useUISettingsStore'
import type { SyncContributor, FullPayload, MergeStrategy } from '../types'

// ---------------------------------------------------------------------------
// Data field list (non-function fields from UISettings)
// ---------------------------------------------------------------------------

const DATA_FIELDS = [
  'terminalRevealDelay',
  'terminalRenderer',
  'keepAliveCount',
  'keepAlivePinned',
  'terminalSettingsVersion',
  'tabIndicatorStyle',
  'ccIconVariant',
  'codexIconVariant',
  'dynamicTabName',
  'showAgentTitleInStatusBar',
] as const

type PreferencesData = {
  [K in (typeof DATA_FIELDS)[number]]: ReturnType<typeof useUISettingsStore.getState>[K]
}

type IncomingPreferencesData = Partial<PreferencesData> & {
  showOscTitle?: unknown
}

function normalizeIncoming(data: IncomingPreferencesData): Partial<PreferencesData> {
  const { showOscTitle, ...rest } = data
  const normalized: Partial<PreferencesData> = { ...rest }
  if (typeof showOscTitle === 'boolean') {
    normalized.dynamicTabName = showOscTitle
    normalized.showAgentTitleInStatusBar = showOscTitle
  }
  return normalized
}

// ---------------------------------------------------------------------------
// Factory
// ---------------------------------------------------------------------------

export function createPreferencesContributor(): SyncContributor {
  return {
    id: 'preferences',
    strategy: 'full',

    getVersion(): number {
      return 1
    },

    serialize(): FullPayload {
      const state = useUISettingsStore.getState()
      const data: Record<string, unknown> = {}
      for (const field of DATA_FIELDS) {
        data[field] = state[field]
      }
      return { version: 1, data }
    },

    deserialize(payload: unknown, merge: MergeStrategy): void {
      const fp = payload as FullPayload
      const rawIncoming = (fp.data ?? {}) as IncomingPreferencesData
      const incoming = normalizeIncoming(rawIncoming)

      if (merge.type === 'full-replace') {
        useUISettingsStore.setState(incoming as PreferencesData)
        return
      }

      // field-merge: only apply fields where resolved[field] === 'remote'
      const patch: Partial<PreferencesData> = {}
      for (const field of DATA_FIELDS) {
        if (merge.resolved[field] === 'remote' && field in incoming) {
          ;(patch as Record<string, unknown>)[field] = incoming[field]
        }
      }
      if (rawIncoming.showOscTitle !== undefined && merge.resolved.showOscTitle === 'remote') {
        if (typeof rawIncoming.showOscTitle === 'boolean') {
          patch.dynamicTabName = rawIncoming.showOscTitle
          patch.showAgentTitleInStatusBar = rawIncoming.showOscTitle
        }
      }

      if (Object.keys(patch).length > 0) {
        useUISettingsStore.setState(patch as PreferencesData)
      }
    },
  }
}
