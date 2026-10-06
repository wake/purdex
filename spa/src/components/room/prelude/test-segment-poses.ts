// Test helper for the suites that mock PreludeSegment to show each run.
import type { PreludeSegmentProps } from './PreludeSegment'

/** Every pos a run draws, in order (a span: each message's). */
export function segmentPoses(p: PreludeSegmentProps): string[] {
  if (p.mode === 'room') return p.entries.map((e) => e.pos)
  return p.blocks.flatMap((b) => (b.kind === 'span' ? p.posOf.slice(b.start, b.end) : [b.entry.pos]))
}
