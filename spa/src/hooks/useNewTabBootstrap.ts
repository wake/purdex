// spa/src/hooks/useNewTabBootstrap.ts
import { useEffect } from 'react'
import { useNewTabLayoutStore } from '../stores/useNewTabLayoutStore'
import { getNewTabProviders } from '../lib/new-tab-registry'

export function useNewTabBootstrap(): void {
  useEffect(() => {
    const runDefaults = () => {
      const providers = getNewTabProviders().map((p) => ({
        id: p.id,
        order: p.order,
        disabled: p.disabled,
      }))
      useNewTabLayoutStore.getState().ensureDefaults(providers)
    }

    if (useNewTabLayoutStore.persist.hasHydrated()) {
      runDefaults()
      return
    }
    return useNewTabLayoutStore.persist.onFinishHydration(runDefaults)
  }, [])
}
