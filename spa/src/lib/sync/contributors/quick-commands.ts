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
      // Only attach sanitized bindings when incoming has the field (preserves
      // field-merge semantics: absent field → no patch → local untouched).
      const sanitizedIncoming: Partial<QuickCommandsData> = {
        ...incoming,
        ...(incoming.bindings !== undefined
          ? { bindings: sanitizeBindings(incoming.bindings) }
          : {}),
      }

      if (merge.type === 'full-replace') {
        // Full-replace: bindings MUST be a record after this call. If incoming
        // omitted bindings (older bundle), default to {} so getBoundCommands
        // and friends never see undefined leaked into the store.
        useQuickCommandStore.setState({
          ...sanitizedIncoming,
          bindings: sanitizedIncoming.bindings ?? {},
        } as QuickCommandsData)
        return
      }

      // field-merge: only apply remote-resolved fields actually present in
      // incoming. Absent bindings → leave local untouched (don't force {}).
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
