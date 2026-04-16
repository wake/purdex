// =============================================================================
// Sync Architecture — WorkspacesContributor
// =============================================================================

import { useWorkspaceStore } from '../../../features/workspace/store'
import type { SyncContributor, FullPayload, MergeStrategy } from '../types'

// ---------------------------------------------------------------------------
// Data field list (non-function fields from WorkspaceState)
// ---------------------------------------------------------------------------

const DATA_FIELDS = ['workspaces', 'activeWorkspaceId'] as const

type WorkspacesData = {
  [K in (typeof DATA_FIELDS)[number]]: ReturnType<typeof useWorkspaceStore.getState>[K]
}

// ---------------------------------------------------------------------------
// Factory
// ---------------------------------------------------------------------------

export function createWorkspacesContributor(): SyncContributor {
  return {
    id: 'workspaces',
    strategy: 'full',

    getVersion(): number {
      return 1
    },

    serialize(): FullPayload {
      const state = useWorkspaceStore.getState()
      const data: Record<string, unknown> = {}
      for (const field of DATA_FIELDS) {
        data[field] = state[field]
      }
      return { version: 1, data }
    },

    deserialize(payload: unknown, merge: MergeStrategy): void {
      const fp = payload as FullPayload
      const incoming = fp.data as Partial<WorkspacesData>

      if (merge.type === 'full-replace') {
        useWorkspaceStore.setState(incoming as WorkspacesData)
        return
      }

      // field-merge: only apply fields where resolved[field] === 'remote'
      const patch: Partial<WorkspacesData> = {}
      for (const field of DATA_FIELDS) {
        if (merge.resolved[field] === 'remote' && field in incoming) {
          ;(patch as Record<string, unknown>)[field] = incoming[field]
        }
      }

      if (Object.keys(patch).length > 0) {
        useWorkspaceStore.setState(patch as WorkspacesData)
      }
    },
  }
}
