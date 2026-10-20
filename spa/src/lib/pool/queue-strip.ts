// spa/src/lib/pool/queue-strip.ts — what the sidebar's queue strip and the narrow sidebar's two counts draw (monitoring tabs spec M1–M5, M15, M25).
// One segment per job still waiting in the pool (not claimed, not held), the four hosts' queues together in the jobs page's display order; heavy and light by `job.slot` (a response without it: heavy).
// The strip never shows more than `nseg` segments: the rest is `overflow`. The counts and the list are over ALL waiting jobs. A pool nobody can read draws nothing, whatever the jobs store still holds.
import type { QueueEntry } from './jobs-lanes'

/** The list a hover shows is the first this many; the line "… n more" says the rest. */
export const QUEUE_STRIP_TOP = 10

export interface QueueStrip {
  readable: boolean
  segments: Array<'heavy' | 'light'>
  overflow: number
  counts: { heavy: number; light: number }
  top: QueueEntry[]
}

export function queueStripOf(entries: readonly QueueEntry[], nseg: number, readable: boolean): QueueStrip {
  if (!readable) return { readable: false, segments: [], overflow: 0, counts: { heavy: 0, light: 0 }, top: [] }
  const waiting = entries.filter((e) => !e.held)
  const slotOf = (e: QueueEntry): 'heavy' | 'light' => (e.job.slot === 'light' ? 'light' : 'heavy')
  const counts = { heavy: 0, light: 0 }
  for (const e of waiting) counts[slotOf(e)] += 1
  return {
    readable: true,
    segments: waiting.slice(0, nseg).map(slotOf),
    overflow: Math.max(0, waiting.length - nseg),
    counts,
    top: waiting.slice(0, QUEUE_STRIP_TOP),
  }
}
