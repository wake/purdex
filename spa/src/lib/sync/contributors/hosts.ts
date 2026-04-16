// =============================================================================
// Sync Architecture — HostsContributor
// =============================================================================

import { useHostStore } from '../../../stores/useHostStore'
import type { SyncContributor, FullPayload, MergeStrategy } from '../types'

// ---------------------------------------------------------------------------
// Factory
// ---------------------------------------------------------------------------

export function createHostsContributor(): SyncContributor {
  return {
    id: 'hosts',
    strategy: 'full',

    getVersion(): number {
      return 1
    },

    serialize(): FullPayload {
      const state = useHostStore.getState()
      const { hosts, hostOrder, activeHostId } = state

      // Strip token from each HostConfig; do NOT include runtime (ephemeral)
      const sanitized: Record<string, unknown> = {}
      for (const [id, config] of Object.entries(hosts)) {
        const { token, ...rest } = config as Record<string, unknown>
        void token // intentionally excluded
        sanitized[id] = rest
      }

      return {
        version: 1,
        data: {
          hosts: sanitized,
          hostOrder,
          activeHostId,
        },
      }
    },

    deserialize(payload: unknown, merge: MergeStrategy): void {
      const fp = payload as FullPayload
      const incoming = fp.data as Record<string, unknown>

      if (merge.type === 'full-replace') {
        useHostStore.setState({
          hosts: incoming.hosts as ReturnType<typeof useHostStore.getState>['hosts'],
          hostOrder: incoming.hostOrder as string[],
          activeHostId: incoming.activeHostId as string | null,
        })
        return
      }

      // field-merge: only apply fields where resolved[field] === 'remote'
      const patch: Record<string, unknown> = {}
      for (const field of ['hosts', 'hostOrder', 'activeHostId'] as const) {
        if (merge.resolved[field] === 'remote' && field in incoming) {
          patch[field] = incoming[field]
        }
      }

      if (Object.keys(patch).length > 0) {
        useHostStore.setState(patch as Parameters<typeof useHostStore.setState>[0])
      }
    },
  }
}
