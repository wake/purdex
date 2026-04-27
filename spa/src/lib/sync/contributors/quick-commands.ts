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
      const incoming = (fp.data ?? {}) as Record<string, unknown>

      // codex round-2 attack vector: a hostile sync payload could include
      // keys like `setBinding`, `getBoundCommands`, `addCommand` etc. and
      // zustand merge mode would overwrite those action methods with
      // attacker-controlled values, defeating the action-method protection.
      //
      // Defense: build patch ONLY from whitelisted DATA_FIELDS. Never spread
      // arbitrary `incoming` into setState.

      if (merge.type === 'full-replace') {
        // Full-replace: explicitly assign each whitelisted field with safe
        // defaults. bindings MUST be a record after this call (sanitizer
        // returns {} for absent / null / garbage), so getBoundCommands and
        // friends never see `undefined` leaked into the store.
        const patch: Partial<QuickCommandsData> = {
          global: Array.isArray(incoming.global) ? (incoming.global as QuickCommandsData['global']) : [],
          byHost:
            typeof incoming.byHost === 'object' &&
            incoming.byHost !== null &&
            !Array.isArray(incoming.byHost)
              ? (incoming.byHost as QuickCommandsData['byHost'])
              : {},
          bindings: sanitizeBindings(incoming.bindings),
        }
        useQuickCommandStore.setState(patch as QuickCommandsData)
        return
      }

      // field-merge: only apply remote-resolved fields actually present in
      // incoming. Absent fields → leave local untouched.
      const patch: Partial<QuickCommandsData> = {}
      for (const field of DATA_FIELDS) {
        if (merge.resolved[field] !== 'remote') continue
        if (!(field in incoming)) continue
        if (field === 'bindings') {
          patch.bindings = sanitizeBindings(incoming.bindings)
        } else if (field === 'global') {
          if (Array.isArray(incoming.global)) {
            patch.global = incoming.global as QuickCommandsData['global']
          }
        } else if (field === 'byHost') {
          if (
            typeof incoming.byHost === 'object' &&
            incoming.byHost !== null &&
            !Array.isArray(incoming.byHost)
          ) {
            patch.byHost = incoming.byHost as QuickCommandsData['byHost']
          }
        }
      }

      if (Object.keys(patch).length > 0) {
        useQuickCommandStore.setState(patch as QuickCommandsData)
      }
    },
  }
}
