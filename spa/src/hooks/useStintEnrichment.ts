// spa/src/hooks/useStintEnrichment.ts — conversation entity spec §10.4: an
// earlier worker stint's enrichment, for the PreludeSegment drawing its lines.
// ExecutionView creates the pane's one cache and provides it here; without a
// provider (a section drawn outside a worker pane) nothing is ever fetched.
import { createContext, useContext, useEffect, useSyncExternalStore } from 'react'
import type { StintEnrichmentCache } from '../lib/nex/stint-enrichment-cache'
import type { StintEnrichment } from '../lib/nex/stint-enrichment'

export const StintEnrichmentContext = createContext<StintEnrichmentCache | null>(null)

const noSubscribe = () => () => {}

/**
 * Called once per PreludeSegment (one component per segment, so no dynamic
 * hook calls). On mount it asks the cache for `stintId` (idempotent: two
 * segments of one stint share one fetch); `stintId` null — a plain segment —
 * asks for nothing. undefined = loading or plain; null = failed.
 */
export function useStintEnrichment(hostId: string, stintId: string | null): StintEnrichment | null | undefined {
  const cache = useContext(StintEnrichmentContext)
  useEffect(() => {
    if (cache && stintId !== null) cache.request(hostId, stintId)
  }, [cache, hostId, stintId])
  return useSyncExternalStore(cache ? cache.subscribe : noSubscribe, () => (cache && stintId !== null ? cache.get(stintId) : undefined))
}
