// spa/src/hooks/useNewTabBootstrap.ts
import { useEffect } from 'react'
import { useNewTabLayoutStore } from '../stores/useNewTabLayoutStore'
import { useHostStore } from '../stores/useHostStore'
import {
  getReadyNewTabProviders,
  getNewTabProviderMigrations,
  getStaleNewTabProviderIds,
  subscribeNewTabProviders,
} from '../lib/new-tab-registry'
import { identityOfSync, presetColumnIdToWire } from '../lib/profile/host-identity'

/**
 * Reconcile the persisted New Tab layout with the provider registry: migrate
 * the legacy single `sessions` id, prune ids a dynamic source no longer
 * produces and does not retain, then place newcomers. A host-bearing column
 * (`sessions:<id>` / `headless:<id>`) is retained even when its host is not on
 * this device — it is kept verbatim and comes back when the host is added
 * (host ownership spec §3.2) — so a removed or absent host's block stays.
 * Re-runs whenever a dynamic source changes (hosts added / removed), so a new
 * host's blocks are placed live.
 */
export function useNewTabBootstrap(): void {
  useEffect(() => {
    const run = () => {
      // 1. Migrate retired ids first (legacy `sessions` → per-host blocks, in
      //    place) so the prune below doesn't just drop them.
      for (const { from, to } of getNewTabProviderMigrations()) {
        useNewTabLayoutStore.getState().migrateId(from, to)
      }

      // 2. Prune ids a ready source no longer produces and does not retain.
      const { knownIds, presets, pruneIds } = useNewTabLayoutStore.getState()
      const referenced = new Set<string>(knownIds)
      for (const key of ['3col', '2col', '1col'] as const) {
        for (const col of presets[key].columns) col.forEach((id) => referenced.add(id))
      }
      const stale = getStaleNewTabProviderIds([...referenced])
      if (stale.length > 0) pruneIds(stale)

      // Unready sources (e.g. hosts not yet hydrated) are skipped so a transient
      // host list is never placed; they reconcile when their source notifies.
      //
      // 3. A new host's block may already be here under the host's WIRE id —
      //    kept verbatim while this device lacked the host, and not yet
      //    rewritten by the (asynchronous) re-resolve pass (host ownership
      //    §3.3). That block is the one the pass will turn into this
      //    provider's: placing the provider too would leave two.
      const after = useNewTabLayoutStore.getState()
      const present = new Set<string>(after.knownIds)
      for (const key of ['3col', '2col', '1col'] as const) {
        for (const col of after.presets[key].columns) col.forEach((id) => present.add(id))
      }
      const identity = identityOfSync(useHostStore.getState().hosts)
      const providers = getReadyNewTabProviders()
        .filter((p) => {
          const wire = presetColumnIdToWire(p.id, identity)
          return wire === p.id || !present.has(wire)
        })
        .map((p) => ({
          id: p.id,
          order: p.order,
          disabled: p.disabled,
        }))
      useNewTabLayoutStore.getState().ensureDefaults(providers)
    }

    let unsubProviders: (() => void) | undefined
    const start = () => {
      run()
      unsubProviders = subscribeNewTabProviders(run)
    }

    let unsubHydration: (() => void) | undefined
    if (useNewTabLayoutStore.persist.hasHydrated()) start()
    else unsubHydration = useNewTabLayoutStore.persist.onFinishHydration(start)

    return () => {
      unsubHydration?.()
      unsubProviders?.()
    }
  }, [])
}
