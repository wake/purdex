// Test helpers for the suites that mock PreludeSegment to show each run, and
// for the ones that watch a pane's stint listing.
import { vi, type Mock } from 'vitest'
import type { PreludeSegmentProps } from './PreludeSegment'
import type { useEntityStints as UseEntityStints } from '../../../hooks/useEntityStints'

/** Every pos a run draws, in order (a span: each message's). */
export function segmentPoses(p: PreludeSegmentProps): string[] {
  if (p.mode === 'room') return p.entries.map((e) => e.pos)
  return p.blocks.flatMap((b) => (b.kind === 'span' ? p.posOf.slice(b.start, b.end) : [b.entry.pos]))
}

let stintsSpy: Mock<typeof UseEntityStints> | null = null

/**
 * A `vi.mock('…/hooks/useEntityStints', …)` factory body: the real hook, passed
 * through a spy. Load this module with a dynamic import inside the factory
 * (the factory is hoisted above the file's imports).
 */
export async function passThroughEntityStints(importOriginal: <T>() => Promise<T>) {
  const real = await importOriginal<typeof import('../../../hooks/useEntityStints')>()
  stintsSpy = vi.fn(real.useEntityStints)
  return { ...real, useEntityStints: stintsSpy }
}

/** What the pane last learnt of its earlier stints: the spied hook's result at its last render. */
export const stintsNow = () => stintsSpy?.mock.results.at(-1)?.value

/** Forget earlier calls (a top-level beforeEach), so `stintsNow` never reads another test's render. */
export const clearStintsSpy = () => { stintsSpy?.mockClear() }
