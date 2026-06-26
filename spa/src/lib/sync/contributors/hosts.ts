// =============================================================================
// Sync Architecture — HostsContributor
// =============================================================================

import { useHostStore, type HostConfig } from '../../../stores/useHostStore'
import type { SyncContributor, FullPayload, MergeStrategy } from '../types'

// Apply an incoming (token-stripped) host map onto the current state, preserving
// each host's local token ONLY when endpoint identity (ip + port) still matches.
// A reused id pointing at a different endpoint — or a host not present locally —
// gets the explicit `null` sentinel ("cleared, re-auth required"), never a stale
// token (which could leak a bearer to an attacker-controlled daemon) and never a
// silently-dropped `undefined`. Shared by full-replace and field-merge so both
// merge modes enforce the same token-clearing contract.
function mergeHostsPreservingTokens(
  current: Record<string, HostConfig>,
  incomingHosts: Record<string, HostConfig>,
): Record<string, HostConfig> {
  const merged: Record<string, HostConfig> = {}
  for (const [id, host] of Object.entries(incomingHosts)) {
    const currentHost = current[id]
    const sameEndpoint =
      currentHost !== undefined &&
      currentHost.ip === host.ip &&
      currentHost.port === host.port
    merged[id] = { ...host, token: sameEndpoint ? currentHost.token : null }
  }
  return merged
}

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
        const incomingHosts = (incoming.hosts ?? {}) as unknown as Record<string, HostConfig>
        useHostStore.setState({
          hosts: mergeHostsPreservingTokens(useHostStore.getState().hosts, incomingHosts),
          hostOrder: incoming.hostOrder as string[],
          activeHostId: incoming.activeHostId as string | null,
        })
        return
      }

      // field-merge: only apply fields where resolved[field] === 'remote'.
      // Applying remote `hosts` must enforce the same endpoint-aware token
      // contract as full-replace — incoming hosts are token-stripped, so a raw
      // assignment would silently drop every local token (and skip the `null`
      // re-auth sentinel for endpoint-changed hosts).
      const patch: Partial<Pick<ReturnType<typeof useHostStore.getState>, 'hosts' | 'hostOrder' | 'activeHostId'>> = {}
      for (const field of ['hosts', 'hostOrder', 'activeHostId'] as const) {
        if (merge.resolved[field] === 'remote' && field in incoming) {
          if (field === 'hosts') patch.hosts = mergeHostsPreservingTokens(useHostStore.getState().hosts, incoming[field] as unknown as Record<string, HostConfig>)
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
