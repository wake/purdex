import { describe, it, expect } from 'vitest'
import { matchesExecutionQuery } from './execution-search'
import type { ExecutionSummary } from './types'

const row = (o: Partial<ExecutionSummary>): ExecutionSummary =>
  ({ id: 'exec-123', state: 'idle', provider: 'claude', principal_id: 'p', cwd: '/Users/wake/proj', mount_kind: 'dev', brief: 'Fix The Login', labels: {}, created_at: 0, updated_at: 0, duration_ms: null, event_count: 0, observers: 0, archived: false, ...o }) as ExecutionSummary

describe('matchesExecutionQuery', () => {
  it('a blank query matches everything', () => {
    expect(matchesExecutionQuery(row({}), '', '/Users/wake', false)).toBe(true)
    expect(matchesExecutionQuery(row({}), '   ', '/Users/wake', false)).toBe(true)
  })
  it('matches brief, id, provider, case-insensitively and trimmed', () => {
    expect(matchesExecutionQuery(row({}), ' login ', '', false)).toBe(true)
    expect(matchesExecutionQuery(row({}), 'EXEC-12', '', false)).toBe(true)
    expect(matchesExecutionQuery(row({ provider: 'codex' }), 'CODEX', '', false)).toBe(true)
  })
  it('matches the cwd as it is and as shown with ~', () => {
    expect(matchesExecutionQuery(row({}), '/users/wake/proj', '/Users/wake', false)).toBe(true)
    expect(matchesExecutionQuery(row({}), '~/proj', '/Users/wake', false)).toBe(true)
    expect(matchesExecutionQuery(row({}), '~/proj', '', false)).toBe(false)
  })
  it('does not match unrelated text', () => {
    expect(matchesExecutionQuery(row({}), 'zzz', '/Users/wake', false)).toBe(false)
  })
  describe('the name a row shows (#1771)', () => {
    const handoff = () => row({ brief: '', cwd: '/w/repo', session_title: { text: 'Zebrafinch', source: 'ai' } })
    it('with the capability: the session title is searchable, case-insensitively', () => {
      expect(matchesExecutionQuery(handoff(), 'zebrafinch', '', true)).toBe(true)
    })
    it('without the capability: the cached title is NOT searchable, the cwd still is', () => {
      expect(matchesExecutionQuery(handoff(), 'Zebrafinch', '', false)).toBe(false)
      expect(matchesExecutionQuery(handoff(), 'repo', '', false)).toBe(true)
    })
    it('a brief row is still found by brief, cwd, id and provider', () => {
      expect(matchesExecutionQuery(row({}), 'login', '', true)).toBe(true)
      expect(matchesExecutionQuery(row({}), 'proj', '', false)).toBe(true)
      expect(matchesExecutionQuery(row({}), 'exec-1', '', true)).toBe(true)
      expect(matchesExecutionQuery(row({}), 'claude', '', false)).toBe(true)
    })
  })
})
