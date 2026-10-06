// spa/src/lib/nex/stint-enrichment-cache.ts — conversation entity spec §10.4:
// one per worker pane (ExecutionView). Each earlier stint's enrichment is
// fetched once, the first time a segment of that stint is drawn, and kept for
// the pane's life. A failure settles null and stays: the retry is the next
// pane mount, and it is silent — no toast, no error row, no log (§10.6).
// Imperative, no React: useStintEnrichment reads it through useSyncExternalStore.
import { fetchExecutionEvents } from './nex-api'
import { ENRICHMENT_EVENT_BUDGET, enrichFromEvents, type StintEnrichment } from './stint-enrichment'
import type { NexEvent } from './types'

export interface StintEnrichmentCache {
  /** undefined = not requested, or still loading; null = failed. The same object on every read. */
  get(stintId: string): StintEnrichment | null | undefined
  /** Idempotent: at most one fetch per stint, ever, in this cache. */
  request(hostId: string, stintId: string): void
  subscribe(fn: () => void): () => void
  /** Bumps on every settle, success or failure (the prelude anchor's version, search's redraw). */
  revision(): number
}

const PAGE_LIMIT = 500

/**
 * `fetchEvents` defaults to the daemon's; it is read only when a fetch starts.
 * The fetch pages forward from 0 until `next_cursor === 0`, or until it holds
 * more than the budget — one page past it, so the reducer can tell exactly
 * the budget from more. A cursor that does not move fails the walk.
 */
export function createStintEnrichmentCache(fetchEvents?: typeof fetchExecutionEvents): StintEnrichmentCache {
  const settled = new Map<string, StintEnrichment | null>()
  const started = new Set<string>()
  const listeners = new Set<() => void>()
  let rev = 0

  const load = async (hostId: string, stintId: string): Promise<StintEnrichment | null> => {
    const fetchPage = fetchEvents ?? fetchExecutionEvents
    const events: NexEvent[] = []
    let after = 0
    for (;;) {
      const page = await fetchPage(hostId, stintId, { after, limit: PAGE_LIMIT })
      events.push(...page.items)
      if (page.next_cursor === 0 || events.length > ENRICHMENT_EVENT_BUDGET) return enrichFromEvents(events)
      if (page.next_cursor <= after) return null
      after = page.next_cursor
    }
  }

  return {
    get: (stintId) => settled.get(stintId),
    request(hostId, stintId) {
      if (started.has(stintId)) return
      started.add(stintId)
      void load(hostId, stintId).catch(() => null).then((value) => {
        settled.set(stintId, value)
        rev++
        for (const fn of [...listeners]) fn()
      })
    },
    subscribe(fn) {
      listeners.add(fn)
      return () => { listeners.delete(fn) }
    },
    revision: () => rev,
  }
}
