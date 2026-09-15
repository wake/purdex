import { useEffect, useMemo, useState } from 'react'
import { getNewTabProviders, subscribeNewTabProviders, type NewTabProvider } from '../lib/new-tab-registry'

/**
 * Registry providers, re-read whenever a dynamic provider source changes
 * (e.g. hosts added / removed / renamed). Static providers are unaffected.
 */
export function useNewTabProviders(): NewTabProvider[] {
  const [version, setVersion] = useState(0)
  useEffect(() => subscribeNewTabProviders(() => setVersion((v) => v + 1)), [])
  // `version` is the cache key: recompute only when a source notified.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  return useMemo(() => getNewTabProviders(), [version])
}
