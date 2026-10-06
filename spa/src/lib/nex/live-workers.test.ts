import { describe, it, expect } from 'vitest'
import { isLiveRow, entityKeyOf, liveEntityRows } from './live-workers'
import type { ExecutionSummary } from './types'

const r = (o: Partial<ExecutionSummary> & { id: string }): ExecutionSummary => ({
  state: 'idle', provider: 'claude', principal_id: 'p', cwd: '/w', mount_kind: 'dir', brief: '', labels: {},
  created_at: 1, updated_at: 1, duration_ms: null, event_count: 0, observers: 0, archived: false, ...o,
})

describe('live workers', () => {
  it('isLiveRow follows spec §4.2', () => {
    expect(isLiveRow(r({ id: 'a', state: 'idle' }))).toBe(true)
    expect(isLiveRow(r({ id: 'a', state: 'failed' }))).toBe(true)
    expect(isLiveRow(r({ id: 'a', state: 'rejected' }))).toBe(true)
    expect(isLiveRow(r({ id: 'a', state: 'terminated' }))).toBe(false)
    expect(isLiveRow(r({ id: 'a', state: 'idle', archived: true }))).toBe(false)
  })

  it('entityKeyOf prefers session_id, then resume_session_id, then id', () => {
    expect(entityKeyOf(r({ id: 'a', session_id: 'S', resume_session_id: 'R' }))).toBe('S')
    expect(entityKeyOf(r({ id: 'a', resume_session_id: 'R' }))).toBe('R')
    expect(entityKeyOf(r({ id: 'a' }))).toBe('a')
  })

  it('keeps one live row per entity — the latest stint — in input order', () => {
    const rows = [
      r({ id: 'old', session_id: 'S', created_at: 10 }),
      r({ id: 'x', session_id: 'X', created_at: 11 }),
      r({ id: 'new', resume_session_id: 'S', created_at: 20 }), // before turn 1: only resume id
      r({ id: 'dead', session_id: 'D', state: 'terminated', created_at: 30 }),
      r({ id: 'gone', session_id: 'G', archived: true, created_at: 31 }),
    ]
    expect(liveEntityRows(rows).map((x) => x.id)).toEqual(['x', 'new'])
  })

  it('breaks a created_at tie by the larger id', () => {
    const rows = [r({ id: '01B', session_id: 'S', created_at: 5 }), r({ id: '01A', session_id: 'S', created_at: 5 })]
    expect(liveEntityRows(rows).map((x) => x.id)).toEqual(['01B'])
  })
})
