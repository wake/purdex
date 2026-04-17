// =============================================================================
// Sync Architecture — LayoutContributor
// =============================================================================

import { useLayoutStore } from '../../../stores/useLayoutStore'
import type { SyncContributor, FullPayload, MergeStrategy } from '../types'

// ---------------------------------------------------------------------------
// Data field list (non-function fields from LayoutState)
// ---------------------------------------------------------------------------

const DATA_FIELDS = [
  'regions',
  'activityBarWidth',
  'tabPosition',
  'activityBarWideSize',
  'workspaceExpanded',
] as const

type LayoutData = {
  [K in (typeof DATA_FIELDS)[number]]: ReturnType<typeof useLayoutStore.getState>[K]
}

// ---------------------------------------------------------------------------
// Factory
// ---------------------------------------------------------------------------

export function createLayoutContributor(): SyncContributor {
  return {
    id: 'layout',
    strategy: 'full',

    getVersion(): number {
      return 1
    },

    serialize(): FullPayload {
      const state = useLayoutStore.getState()
      const data: Record<string, unknown> = {}
      for (const field of DATA_FIELDS) {
        data[field] = state[field]
      }
      return { version: 1, data }
    },

    deserialize(payload: unknown, merge: MergeStrategy): void {
      const fp = payload as FullPayload
      const incoming = fp.data as Partial<LayoutData>

      if (merge.type === 'full-replace') {
        useLayoutStore.setState(incoming as LayoutData)
        return
      }

      // field-merge: only apply fields where resolved[field] === 'remote'
      const patch: Partial<LayoutData> = {}
      for (const field of DATA_FIELDS) {
        if (merge.resolved[field] === 'remote' && field in incoming) {
          ;(patch as Record<string, unknown>)[field] = incoming[field]
        }
      }

      if (Object.keys(patch).length > 0) {
        useLayoutStore.setState(patch as LayoutData)
      }
    },
  }
}
