// spa/src/lib/nex/execution-groups.test.ts
import { describe, it, expect } from 'vitest'
import { groupBySource, sameHostSessionCode } from './execution-groups'
import type { ExecutionSummary } from './types'

const row = (id: string, updated_at: number, source?: string): ExecutionSummary =>
  ({ id, state: 'idle', provider: 'claude', principal_id: 'p', cwd: '/w', mount_kind: 'dev', brief: id, labels: source ? { source } : {}, created_at: 0, updated_at, duration_ms: null, event_count: 0, observers: 0, archived: false }) as ExecutionSummary

describe('groupBySource', () => {
  it('returns no groups for no rows', () => {
    expect(groupBySource([])).toEqual([])
  })

  it('buckets rows without a source label under local', () => {
    const groups = groupBySource([row('a', 1), row('b', 2, 'purdex')])
    expect(groups.map((g) => g.source)).toEqual(['purdex', 'local'])
  })

  it('orders groups by their newest row and rows newest-first inside each group', () => {
    const groups = groupBySource([
      row('old-local', 10),
      row('aigora', 20, 'aigora'),
      row('purdex', 30, 'purdex'),
      row('new-local', 40),
    ])
    expect(groups.map((g) => g.source)).toEqual(['local', 'purdex', 'aigora'])
    expect(groups[0].rows.map((r) => r.id)).toEqual(['new-local', 'old-local'])
  })

  it('a non-string labels.source (or no labels object at all) is bucketed under local', () => {
    const bad = { ...row('obj', 3), labels: { source: { nested: true } } } as unknown as ExecutionSummary
    const none = { ...row('none', 2), labels: null } as unknown as ExecutionSummary
    const groups = groupBySource([bad, none, row('num', 1, 'purdex')])
    expect(groups.map((g) => g.source)).toEqual(['local', 'purdex'])
    expect(groups[0].rows.map((r) => r.id)).toEqual(['obj', 'none'])
  })

  it('does not mutate the input order', () => {
    const items = [row('a', 1), row('b', 2)]
    groupBySource(items)
    expect(items.map((r) => r.id)).toEqual(['a', 'b'])
  })
})

describe('sameHostSessionCode', () => {
  it('extracts the code from a same-host session origin', () => {
    expect(sameHostSessionCode('purdex://host/h1/session/zk16vd', 'h1')).toBe('zk16vd')
  })

  it('stops at the next path segment', () => {
    expect(sameHostSessionCode('purdex://host/h1/session/zk16vd/extra', 'h1')).toBe('zk16vd')
  })

  it('rejects another host, a non-session path, an empty code and a missing origin', () => {
    expect(sameHostSessionCode('purdex://host/h2/session/zk16vd', 'h1')).toBeNull()
    expect(sameHostSessionCode('purdex://host/h1/other/zk16vd', 'h1')).toBeNull()
    expect(sameHostSessionCode('purdex://host/h1/session/', 'h1')).toBeNull()
    expect(sameHostSessionCode(undefined, 'h1')).toBeNull()
  })

  it('does not match a host id that merely shares a prefix', () => {
    expect(sameHostSessionCode('purdex://host/h10/session/zk16vd', 'h1')).toBeNull()
  })
})
