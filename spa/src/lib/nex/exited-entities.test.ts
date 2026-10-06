import { describe, it, expect } from 'vitest'
import { exitedEntities, matchesExitedQuery } from './exited-entities'
import type { ExecutionSummary } from './types'

const r = (o: Partial<ExecutionSummary>): ExecutionSummary => ({
  id: 'x', state: 'idle', provider: 'cc', principal_id: 'p', cwd: '/w', mount_kind: 'none', brief: '', labels: {},
  created_at: 0, updated_at: 0, duration_ms: null, event_count: 0, observers: 0, archived: false, ...o,
})

describe('exitedEntities', () => {
  it('lists entities with no live stint, latest stint each, newest first', () => {
    const rows = [
      r({ id: 'a1', session_id: 'A', state: 'terminated', archived: true, created_at: 1, updated_at: 10 }),
      r({ id: 'a2', session_id: 'A', state: 'terminated', archived: true, created_at: 2, updated_at: 20 }),
      r({ id: 'b1', session_id: 'B', state: 'terminated', archived: true, created_at: 3, updated_at: 30 }),
      r({ id: 'b2', session_id: 'B', state: 'idle', created_at: 4, updated_at: 40 }),
      r({ id: 'c1', session_id: 'C', state: 'terminated', created_at: 5, updated_at: 50 }),
    ]
    expect(exitedEntities(rows).map((x) => x.id)).toEqual(['c1', 'a2'])
  })
  it('an entity with an older live stint and a newer exited one is live, not exited (§4.2)', () => {
    const rows = [
      r({ id: 'o', session_id: 'S', state: 'idle', created_at: 1, updated_at: 1 }),
      r({ id: 'n', session_id: 'S', state: 'terminated', archived: true, created_at: 2, updated_at: 2 }),
    ]
    expect(exitedEntities(rows)).toEqual([])
  })
})

describe('matchesExitedQuery', () => {
  it('matches title, brief, cwd and session id', () => {
    const row = r({ id: 'x', session_id: 'abc-123', cwd: '/Users/w/proj', brief: '修 bug\n細節' })
    expect(matchesExitedQuery(row, 'PROJ')).toBe(true)
    expect(matchesExitedQuery(row, 'abc')).toBe(true)
    expect(matchesExitedQuery(row, '修')).toBe(true)
    expect(matchesExitedQuery(row, '細節')).toBe(false)
    expect(matchesExitedQuery(row, '')).toBe(true)
  })
})
