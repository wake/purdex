// =============================================================================
// Sync Architecture — HostsContributor
// =============================================================================

import { useHostStore, type HostConfig } from '../../../stores/useHostStore'
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
      const sanitized: Record<string, Omit<HostConfig, 'token'>> = {}
      for (const [id, config] of Object.entries(hosts)) {
        const { token, ...rest } = config
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
        const current = useHostStore.getState().hosts
        const incomingHosts = (incoming.hosts ?? {}) as unknown as Record<string, HostConfig>
        const mergedHosts: Record<string, HostConfig> = {}
        for (const [id, host] of Object.entries(incomingHosts)) {
          // Only preserve token when endpoint identity (ip, port) matches.
          // A bundle reusing an id but pointing at a different endpoint must
          // force re-auth, otherwise a bearer token could be sent to an
          // attacker-controlled daemon.
          const currentHost = current[id]
          const sameEndpoint =
            currentHost !== undefined &&
            currentHost.ip === host.ip &&
            currentHost.port === host.port
          mergedHosts[id] = {
            ...host,
            token: sameEndpoint ? currentHost.token : undefined,
          }
        }
        useHostStore.setState({
          hosts: mergedHosts,
          hostOrder: incoming.hostOrder as string[],
          activeHostId: incoming.activeHostId as string | null,
        })
        return
      }

      // field-merge: only apply fields where resolved[field] === 'remote'
      const patch: Partial<Pick<ReturnType<typeof useHostStore.getState>, 'hosts' | 'hostOrder' | 'activeHostId'>> = {}
      for (const field of ['hosts', 'hostOrder', 'activeHostId'] as const) {
        if (merge.resolved[field] === 'remote' && field in incoming) {
          if (field === 'hosts') patch.hosts = incoming[field] as unknown as Record<string, HostConfig>
          if (field === 'hostOrder') patch.hostOrder = incoming[field] as string[]
          if (field === 'activeHostId') patch.activeHostId = incoming[field] as string | null
        }
      }

      if (Object.keys(patch).length > 0) {
        useHostStore.setState(patch)
      }
    },
  }
}
