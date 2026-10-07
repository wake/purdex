import { describe, it, expect } from 'vitest'
import { matchesExecutionQuery } from './execution-search'
import type { ExecutionSummary } from './types'

const row = (o: Partial<ExecutionSummary>): ExecutionSummary =>
  ({ id: 'exec-123', state: 'idle', provider: 'claude', principal_id: 'p', cwd: '/Users/wake/proj', mount_kind: 'dev', brief: 'Fix The Login', labels: {}, created_at: 0, updated_at: 0, duration_ms: null, event_count: 0, observers: 0, archived: false, ...o }) as ExecutionSummary

describe('matchesExecutionQuery', () => {
  it('a blank query matches everything', () => {
    expect(matchesExecutionQuery(row({}), '', '/Users/wake')).toBe(true)
    expect(matchesExecutionQuery(row({}), '   ', '/Users/wake')).toBe(true)
  })
  it('matches brief, id, provider, case-insensitively and trimmed', () => {
    expect(matchesExecutionQuery(row({}), ' login ', '')).toBe(true)
    expect(matchesExecutionQuery(row({}), 'EXEC-12', '')).toBe(true)
    expect(matchesExecutionQuery(row({ provider: 'codex' }), 'CODEX', '')).toBe(true)
  })
  it('matches the cwd as it is and as shown with ~', () => {
    expect(matchesExecutionQuery(row({}), '/users/wake/proj', '/Users/wake')).toBe(true)
    expect(matchesExecutionQuery(row({}), '~/proj', '/Users/wake')).toBe(true)
    expect(matchesExecutionQuery(row({}), '~/proj', '')).toBe(false)
  })
  it('does not match unrelated text', () => {
    expect(matchesExecutionQuery(row({}), 'zzz', '/Users/wake')).toBe(false)
  })
})
