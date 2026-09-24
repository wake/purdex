// spa/src/lib/headless-new-tab-providers.tsx — one New Tab "Headless" block
// per host (P-C spec §4.2), mirroring the per-host sessions source. Owned by
// the `execution` module so `unregisterNewTabProvidersByModule('execution')`
// tears it down.
import type { ComponentType } from 'react'
import { useHostStore } from '../stores/useHostStore'
import { hostLabel, hostLookOf } from './host-look'
import { HeadlessLauncher } from '../components/headless/HeadlessLauncher'
import type { NewTabProviderProps, NewTabProviderSource } from './new-tab-registry'

const SOURCE_ID = 'headless'
const PREFIX = 'headless:'
/** After the sessions block (order 0); ties with the editor block keep source order. */
const ORDER = 5

/** New Tab block id for one host's Headless launcher. */
export function headlessProviderId(hostId: string): string {
  return `${PREFIX}${hostId}`
}

// One component per host, created once — a fresh component identity on every
// provider recompute would remount the block and drop its form state.
const componentCache = new Map<string, ComponentType<NewTabProviderProps>>()

function componentFor(hostId: string): ComponentType<NewTabProviderProps> {
  let C = componentCache.get(hostId)
  if (!C) {
    const Block = ({ onSelect }: NewTabProviderProps) => <HeadlessLauncher hostId={hostId} onSelect={onSelect} />
    Block.displayName = `HeadlessLauncher(${hostId})`
    C = Block
    componentCache.set(hostId, C)
  }
  return C
}

/**
 * One New Tab "Headless" block per host (`headless:<hostId>`), in host order,
 * so each host's block can be placed independently in the layout editor.
 * There is no legacy single id to migrate from.
 */
export function createHeadlessProviderSource(): NewTabProviderSource {
  return {
    id: SOURCE_ID,
    moduleId: 'execution',
    getProviders: () => {
      const { hosts, hostOrder } = useHostStore.getState()
      return hostOrder
        .filter((hostId) => hosts[hostId])
        .map((hostId) => ({
          id: headlessProviderId(hostId),
          label: 'newtab.headless.title',
          labelParams: { host: hostLabel(hostId, hostLookOf(hostId, hosts)) },
          icon: 'Lightning',
          order: ORDER,
          component: componentFor(hostId),
          moduleId: 'execution',
        }))
    },
    subscribe: (listener) => {
      const unsubState = useHostStore.subscribe((state, prev) => {
        if (state.hosts !== prev.hosts || state.hostOrder !== prev.hostOrder) listener()
      })
      // persist sets state BEFORE flipping hasHydrated, so the state change
      // above is seen while still unready — re-notify once hydration is done.
      const unsubHydration = useHostStore.persist.onFinishHydration(() => listener())
      return () => { unsubState(); unsubHydration() }
    },
    ownsId: (id) => id.startsWith(PREFIX),
    // A block of a host this device lacks is kept, never pruned (host ownership §3.2).
    retainsId: (id) => id.startsWith(PREFIX),
    // Until the persisted host list is loaded, hostOrder is a transient
    // default — never prune or place per-host blocks from it.
    isReady: () => useHostStore.persist.hasHydrated(),
  }
}
