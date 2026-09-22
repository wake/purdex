// spa/src/hooks/useNewTabBootstrap.ts
import { useEffect } from 'react'
import { useNewTabLayoutStore } from '../stores/useNewTabLayoutStore'
import {
  getReadyNewTabProviders,
  getNewTabProviderMigrations,
  getStaleNewTabProviderIds,
  subscribeNewTabProviders,
} from '../lib/new-tab-registry'

/**
 * Reconcile the persisted New Tab layout with the provider registry: prune ids
 * a dynamic source no longer produces (removed host, legacy `sessions`), then
 * place newcomers. Re-runs whenever a dynamic source changes (hosts added /
 * removed), so per-host blocks follow the host list live.
 */
export function useNewTabBootstrap(): void {
  useEffect(() => {
    const run = () => {
      // 1. Migrate retired ids first (legacy `sessions` → per-host blocks, in
      //    place) so the prune below doesn't just drop them.
      for (const { from, to } of getNewTabProviderMigrations()) {
        useNewTabLayoutStore.getState().migrateId(from, to)
      }

      // 2. Prune ids a ready source no longer produces.
      const { knownIds, presets, pruneIds } = useNewTabLayoutStore.getState()
      const referenced = new Set<string>(knownIds)
      for (const key of ['3col', '2col', '1col'] as const) {
        for (const col of presets[key].columns) col.forEach((id) => referenced.add(id))
      }
      const stale = getStaleNewTabProviderIds([...referenced])
      if (stale.length > 0) pruneIds(stale)

      // Unready sources (e.g. hosts not yet hydrated) are skipped so a transient
      // host list is never placed; they reconcile when their source notifies.
      const providers = getReadyNewTabProviders().map((p) => ({
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
