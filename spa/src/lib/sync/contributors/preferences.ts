// =============================================================================
// Sync Architecture — PreferencesContributor
// =============================================================================

import {
  useUISettingsStore,
  isHostColorMarkStyle,
  clampHostColorLineWidth,
} from '../../../stores/useUISettingsStore'
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
  'hostColorSidebarStyle',
  'hostColorSidebarWidth',
  'hostColorTabBarStyle',
  'hostColorTabBarWidth',
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
  return sanitizeHostColorPrefs(normalized)
}

/**
 * Sync bypasses store setters, so remote host color mark fields are validated
 * here: invalid styles and non-finite / non-number widths are dropped (local
 * value kept); finite widths are rounded + clamped.
 */
function sanitizeHostColorPrefs(data: Partial<PreferencesData>): Partial<PreferencesData> {
  const out: Record<string, unknown> = { ...data }
  for (const field of ['hostColorSidebarStyle', 'hostColorTabBarStyle'] as const) {
    if (field in out && !isHostColorMarkStyle(out[field])) delete out[field]
  }
  for (const field of ['hostColorSidebarWidth', 'hostColorTabBarWidth'] as const) {
    if (!(field in out)) continue
    const v = out[field]
    if (typeof v !== 'number' || !Number.isFinite(v)) delete out[field]
    else out[field] = clampHostColorLineWidth(v)
  }
  return out as Partial<PreferencesData>
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
