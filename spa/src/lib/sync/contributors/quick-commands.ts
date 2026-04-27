// =============================================================================
// Sync Architecture — QuickCommandsContributor
// =============================================================================

import { useQuickCommandStore, sanitizeBindings } from '../../../stores/useQuickCommandStore'
import type { SyncContributor, FullPayload, MergeStrategy } from '../types'

// ---------------------------------------------------------------------------
// Data field list (non-function fields from QuickCommandState)
// ---------------------------------------------------------------------------

const DATA_FIELDS = ['global', 'byHost', 'bindings'] as const

type QuickCommandsData = {
  [K in (typeof DATA_FIELDS)[number]]: ReturnType<typeof useQuickCommandStore.getState>[K]
}

// ---------------------------------------------------------------------------
// Factory
// ---------------------------------------------------------------------------

export function createQuickCommandsContributor(): SyncContributor {
  return {
    id: 'quick-commands',
    strategy: 'full',

    getVersion(): number {
      return 1
    },

    serialize(): FullPayload {
      const state = useQuickCommandStore.getState()
      const data: Record<string, unknown> = {}
      for (const field of DATA_FIELDS) {
        data[field] = state[field]
      }
      return { version: 1, data }
    },

    deserialize(payload: unknown, merge: MergeStrategy): void {
      const fp = payload as FullPayload
      const incoming = fp.data as Partial<QuickCommandsData>

      // Sanitize untrusted incoming bindings BEFORE applying. Mirrors the
      // store's `merge` hook — sync payloads are equally untrusted.
      const sanitizedIncoming: Partial<QuickCommandsData> = {
        ...incoming,
        ...(incoming.bindings !== undefined
          ? { bindings: sanitizeBindings(incoming.bindings) }
          : {}),
      }

      if (merge.type === 'full-replace') {
        useQuickCommandStore.setState(sanitizedIncoming as QuickCommandsData)
        return
      }

      const patch: Partial<QuickCommandsData> = {}
      for (const field of DATA_FIELDS) {
        if (merge.resolved[field] === 'remote' && field in sanitizedIncoming) {
          ;(patch as Record<string, unknown>)[field] = sanitizedIncoming[field]
        }
      }

      if (Object.keys(patch).length > 0) {
        useQuickCommandStore.setState(patch as QuickCommandsData)
      }
    },
  }
}
