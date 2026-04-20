import { describe, expect, it } from 'vitest'
import { computeSnapshotDiff, equalExceptEnvelope } from '../snapshot-diff'
import type { SyncBundle } from '../types'

function bundle(collections: SyncBundle['collections'], device = 'd1'): SyncBundle {
  return { version: 1, timestamp: 0, device, collections }
}

describe('equalExceptEnvelope', () => {
  it('ignores version / timestamp / device', () => {
    const a = bundle({ w: { version: 1, data: { x: 1 } } }, 'd1')
    const b: SyncBundle = { ...a, timestamp: 999, device: 'd2' }
    expect(equalExceptEnvelope(a, b)).toBe(true)
  })

  it('returns false when a collection differs', () => {
    const a = bundle({ w: { version: 1, data: { x: 1 } } })
    const b = bundle({ w: { version: 1, data: { x: 2 } } })
    expect(equalExceptEnvelope(a, b)).toBe(false)
  })
})

describe('computeSnapshotDiff', () => {
  it('identical collections → all identical', () => {
    const a = bundle({
      w: { version: 1, data: { x: 1 } },
      h: { version: 1, data: {} },
    })
    const result = computeSnapshotDiff(a, a)
    expect(result).toHaveLength(2)
    expect(result.every((r) => r.status === 'identical')).toBe(true)
  })

  it('detects changed / missing-in-snapshot / missing-in-current', () => {
    const snap = bundle({
      w: { version: 1, data: { x: 1 } },
      h: { version: 1, data: { y: 2 } },
      onlyInSnap: { version: 1, data: {} },
    })
    const curr = bundle({
      w: { version: 1, data: { x: 2 } },
      h: { version: 1, data: { y: 2 } },
      onlyInCurr: { version: 1, data: {} },
    })
    const result = computeSnapshotDiff(snap, curr)
    expect(result.find((r) => r.id === 'w')?.status).toBe('changed')
    expect(result.find((r) => r.id === 'h')?.status).toBe('identical')
    expect(result.find((r) => r.id === 'onlyInSnap')?.status).toBe('missing-in-current')
    expect(result.find((r) => r.id === 'onlyInCurr')?.status).toBe('missing-in-snapshot')
  })
})
