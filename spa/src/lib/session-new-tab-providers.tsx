import type { ComponentType } from 'react'
import { useHostStore } from '../stores/useHostStore'
import { useHostLookStore } from '../stores/useHostLookStore'
import { hostLabel, hostLookOf } from './host-look'
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
 * Owns the legacy single `sessions` id so a stale persisted entry gets pruned;
 * every `sessions:<id>` is retained, whether or not that host is here.
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
          labelParams: { host: hostLabel(hostId, hostLookOf(hostId, hosts)) },
          icon: 'List',
          order: 0,
          component: componentFor(hostId),
        }))
    },
    subscribe: (listener) => {
      const unsubState = useHostStore.subscribe((state, prev) => {
        if (state.hosts !== prev.hosts || state.hostOrder !== prev.hostOrder) listener()
      })
      // The label is the workbench look's name (H2c-3): a rename — local or applied from a synced `settings`
      // payload — writes the look store only, so it must re-notify too.
      const unsubLooks = useHostLookStore.subscribe((state, prev) => {
        if (state.looks !== prev.looks) listener()
      })
      // persist sets state BEFORE flipping hasHydrated, so the state change
      // above is seen while still unready — re-notify once hydration is done.
      const unsubHydration = useHostStore.persist.onFinishHydration(() => listener())
      return () => { unsubState(); unsubLooks(); unsubHydration() }
    },
    ownsId: (id) => id === LEGACY_ID || id.startsWith(PREFIX),
    // A block of a host this device lacks is kept, never pruned (host ownership
    // §3.2); only the legacy single id still goes, through its migration.
    retainsId: (id) => id.startsWith(PREFIX),
    // Until the persisted host list is loaded, hostOrder is a transient
    // default — never prune or place per-host blocks from it.
    isReady: () => useHostStore.persist.hasHydrated(),
    // The retired single `sessions` block becomes one block per current host.
    migrations: () => {
      const { hosts, hostOrder } = useHostStore.getState()
      return [{ from: LEGACY_ID, to: hostOrder.filter((h) => hosts[h]).map(sessionsProviderId) }]
    },
  }
}
