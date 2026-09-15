import type { ComponentType } from 'react'
import { useHostStore } from '../stores/useHostStore'
import { HostSessionSection } from '../components/SessionSection'
import type { NewTabProviderProps, NewTabProviderSource } from './new-tab-registry'

const LEGACY_ID = 'sessions'
const PREFIX = 'sessions:'

/** New Tab block id for one host's sessions. */
export function sessionsProviderId(hostId: string): string {
  return `${PREFIX}${hostId}`
}

// One component per host, created once — a fresh component identity on every
// provider recompute would remount the block and drop its local state
// (collapse, open create-form).
const componentCache = new Map<string, ComponentType<NewTabProviderProps>>()

function componentFor(hostId: string): ComponentType<NewTabProviderProps> {
  let C = componentCache.get(hostId)
  if (!C) {
    const Block = ({ onSelect }: NewTabProviderProps) => <HostSessionSection hostId={hostId} onSelect={onSelect} />
    Block.displayName = `HostSessionSection(${hostId})`
    C = Block
    componentCache.set(hostId, C)
  }
  return C
}

/**
 * One New Tab "sessions" block per host (`sessions:<hostId>`), in host order,
 * so each host's block can be placed independently in the layout editor.
 * Owns the legacy single `sessions` id so a stale persisted entry gets pruned.
 */
export function createHostSessionProviderSource(): NewTabProviderSource {
  return {
    id: LEGACY_ID,
    getProviders: () => {
      const { hosts, hostOrder } = useHostStore.getState()
      return hostOrder
        .filter((hostId) => hosts[hostId])
        .map((hostId) => ({
          id: sessionsProviderId(hostId),
          label: 'session.provider_label_host',
          labelParams: { host: hosts[hostId].name },
          icon: 'List',
          order: 0,
          component: componentFor(hostId),
        }))
    },
    subscribe: (listener) =>
      useHostStore.subscribe((state, prev) => {
        if (state.hosts !== prev.hosts || state.hostOrder !== prev.hostOrder) listener()
      }),
    ownsId: (id) => id === LEGACY_ID || id.startsWith(PREFIX),
  }
}
