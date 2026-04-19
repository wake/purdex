import { describe, expect, it } from 'vitest'
import { computeCompaction } from '../snapshot-compaction'
import type { SnapshotMetadata } from '../snapshot-types'

function meta(
  id: string,
  trigger: SnapshotMetadata['trigger'],
  offsetMs: number,
  extra: Partial<SnapshotMetadata> = {},
): SnapshotMetadata {
  return {
    id,
    timestamp: Date.now() - offsetMs,
    device: 'dev',
    trigger,
    bundleSize: 1000,
    contributorIds: [],
    isSessionPristine: false,
    ...extra,
  }
}

describe('computeCompaction', () => {
  it('returns empty result for empty input', () => {
    expect(computeCompaction([], Date.now())).toEqual({ kept: [], evicted: [] })
  })

  it('hourly bucket: keeps newest per UTC hour', () => {
    const now = Date.now()
    const HOUR = 60 * 60 * 1000
    const a = meta('a', 'manual', HOUR + 1_000)
    const b = meta('b', 'manual', HOUR + 500)
    const result = computeCompaction([a, b], now)
    expect(result.kept).toContain('b')
    expect(result.evicted).toContain('a')
  })

  it('pre-op pool: max 5 (LRU evict) but pristine never evicted', () => {
    const now = Date.now()
    const items: SnapshotMetadata[] = []
    items.push(meta('pristine', 'pre-restore', 1_000_000, { isSessionPristine: true }))
    for (let i = 0; i < 6; i++) {
      items.push(meta(`p${i}`, 'pre-import', 900_000 - i * 10_000))
    }
    const result = computeCompaction(items, now)
    expect(result.kept).toContain('pristine')
    expect(result.evicted).toEqual(['p0'])
    expect(result.kept).toHaveLength(6)
  })

  it('pre-op pool and time-tier do not interfere', () => {
    const now = Date.now()
    const items: SnapshotMetadata[] = [
      meta('manual-a', 'manual', 0),
      meta('preop-a', 'pre-import', 100),
      meta('preop-b', 'pre-restore', 200),
    ]
    const result = computeCompaction(items, now)
    expect(result.evicted).toEqual([])
  })

  it('daily tier: 1-30 days, one per UTC day', () => {
    const now = Date.now()
    const DAY = 24 * 60 * 60 * 1000
    const dayAgo2 = meta('d2a', 'manual', 2 * DAY + 1000)
    const dayAgo2Newer = meta('d2b', 'manual', 2 * DAY - 1000)
    const result = computeCompaction([dayAgo2, dayAgo2Newer], now)
    expect(result.kept).toContain('d2b')
    expect(result.evicted).toContain('d2a')
  })
})
