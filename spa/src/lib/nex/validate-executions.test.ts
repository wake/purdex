// spa/src/lib/nex/validate-executions.test.ts
import { describe, it, expect } from 'vitest'
import { sanitizeExecutionsPage } from './validate-executions'

const good = { id: 'exc_1', state: 'idle', provider: 'claude', principal_id: 'p', cwd: '/w', mount_kind: 'dev', brief: 'b', labels: { source: 'purdex' }, created_at: 1, updated_at: 2, duration_ms: null, event_count: 0, observers: 0, archived: false }

describe('sanitizeExecutionsPage', () => {
  it('passes a well-formed page through untouched', () => {
    const out = sanitizeExecutionsPage({ items: [good], next_cursor: '' })
    expect(out.dropped).toBe(0)
    expect(out.items).toEqual([good])
  })

  it('a page whose items is not an array yields an empty list and one dropped page', () => {
    expect(sanitizeExecutionsPage({ items: {} })).toEqual({ items: [], dropped: 1 })
    expect(sanitizeExecutionsPage(null)).toEqual({ items: [], dropped: 1 })
    expect(sanitizeExecutionsPage('nope')).toEqual({ items: [], dropped: 1 })
    expect(sanitizeExecutionsPage({})).toEqual({ items: [], dropped: 1 })
  })

  it('drops rows without a string id or state, and non-object rows', () => {
    const out = sanitizeExecutionsPage({ items: [{ ...good, id: undefined }, { ...good, state: 7 }, null, 'x', good] })
    expect(out.items.map((r) => r.id)).toEqual(['exc_1'])
    expect(out.dropped).toBe(4)
  })

  it('coerces brief, origin, timestamps and archived', () => {
    const [r] = sanitizeExecutionsPage({ items: [{ ...good, brief: 42, origin: 9, created_at: 'x', updated_at: Infinity, archived: 'yes' }] }).items
    expect(r.brief).toBe('')
    expect(r.origin).toBeUndefined()
    expect(r.created_at).toBe(0)
    expect(r.updated_at).toBe(0)
    expect(r.archived).toBe(false)
    const [s] = sanitizeExecutionsPage({ items: [{ ...good, origin: 'purdex://x', archived: 0 }] }).items
    expect(s.origin).toBe('purdex://x')
    expect(s.archived).toBe(false)
  })

  it('keeps a row with labels: null as labels {} and drops non-string label values', () => {
    const out = sanitizeExecutionsPage({ items: [{ ...good, labels: null }, { ...good, id: 'exc_2', labels: { source: { nested: true }, keep: 'k', n: 1 } }, { ...good, id: 'exc_3', labels: 'str' }] })
    expect(out.dropped).toBe(0)
    expect(out.items[0].labels).toEqual({})
    expect(out.items[1].labels).toEqual({ keep: 'k' })
    expect(out.items[2].labels).toEqual({})
  })

  it('coerces the other rendered strings and drops a malformed lease', () => {
    const [r] = sanitizeExecutionsPage({ items: [{ ...good, cwd: null, provider: 3, observers: 'many', lease: { expires_at: 1 } }] }).items
    expect(r.cwd).toBe('')
    expect(r.provider).toBe('')
    expect(r.observers).toBe(0)
    expect(r.lease).toBeUndefined()
    const [s] = sanitizeExecutionsPage({ items: [{ ...good, lease: { principal_id: 'pdx:a/b', expires_at: 1 } }] }).items
    expect(s.lease).toEqual({ principal_id: 'pdx:a/b', expires_at: 1 })
  })

  it('archived accepts only a real boolean; any other value is false', () => {
    const out = sanitizeExecutionsPage({ items: [
      { ...good, archived: 'false' }, { ...good, archived: 'no' }, { ...good, archived: 1 }, { ...good, archived: true },
    ] })
    expect(out.items.map((r) => r.archived)).toEqual([false, false, false, true])
  })

  it('rendered optional strings are dropped when not strings', () => {
    const out = sanitizeExecutionsPage({ items: [{ ...good, last_turn_reason: { x: 1 }, terminal_reason: 3, reject_reason: ['a'], effective_profile: {}, requested_profile: null, session_id: 9, account_id: 1 }] })
    const row = out.items[0]
    for (const k of ['last_turn_reason', 'terminal_reason', 'reject_reason', 'effective_profile', 'requested_profile', 'session_id', 'account_id'] as const) {
      expect(row[k]).toBeUndefined()
    }
    expect(out.dropped).toBe(0)
  })

  describe('worker rollup fields (nexen v0.13 worker_rollup)', () => {
    const rollup = {
      cost_usd: 0.1126,
      last_tool: { name: 'Bash', tool_use_id: 'toolu_1', at: 1790500501000 },
      running_tasks: 1,
      turn_count: 3,
      activity: { phase: 'tool', tool: { name: 'Bash', tool_use_id: 'toolu_1', since: 1790500501000 }, open_tools: 1, since: 1790500501000 },
    }

    it('passes well-formed rollup fields through', () => {
      const [r] = sanitizeExecutionsPage({ items: [{ ...good, ...rollup }] }).items
      expect(r).toMatchObject(rollup)
    })

    it('an old daemon row has none of them (not invented)', () => {
      const [r] = sanitizeExecutionsPage({ items: [good] }).items
      for (const k of ['cost_usd', 'last_tool', 'running_tasks', 'activity', 'turn_count'] as const) expect(k in r).toBe(false)
    })

    it('cost_usd: null stays null; non-finite, negative or non-number → null', () => {
      const rows = sanitizeExecutionsPage({ items: [
        { ...good, cost_usd: null }, { ...good, cost_usd: -1 }, { ...good, cost_usd: 'x' }, { ...good, cost_usd: Infinity }, { ...good, cost_usd: 0 },
      ] }).items
      expect(rows.map((r) => r.cost_usd)).toEqual([null, null, null, null, 0])
    })

    it('running_tasks / turn_count must be non-negative integers, else dropped', () => {
      const rows = sanitizeExecutionsPage({ items: [
        { ...good, running_tasks: -1, turn_count: 1.5 }, { ...good, running_tasks: '2', turn_count: null }, { ...good, running_tasks: 0, turn_count: 0 },
      ] }).items
      expect('running_tasks' in rows[0]).toBe(false)
      expect('turn_count' in rows[0]).toBe(false)
      expect('running_tasks' in rows[1]).toBe(false)
      expect('turn_count' in rows[1]).toBe(false)
      expect(rows[2].running_tasks).toBe(0)
      expect(rows[2].turn_count).toBe(0)
    })

    it('last_tool is dropped unless name / tool_use_id / at have the right types', () => {
      const rows = sanitizeExecutionsPage({ items: [
        { ...good, last_tool: { name: 'Bash', tool_use_id: 'toolu_1' } },
        { ...good, last_tool: { name: 3, tool_use_id: 'toolu_1', at: 1 } },
        { ...good, last_tool: 'Bash' },
        { ...good, last_tool: { name: 'Bash', tool_use_id: 'toolu_1', at: 1, extra: true } },
      ] }).items
      expect('last_tool' in rows[0]).toBe(false)
      expect('last_tool' in rows[1]).toBe(false)
      expect('last_tool' in rows[2]).toBe(false)
      expect(rows[3].last_tool).toEqual({ name: 'Bash', tool_use_id: 'toolu_1', at: 1 })
    })

    it('activity is dropped unless phase is a string and open_tools a non-negative integer; a bad tool / since is dropped alone', () => {
      const rows = sanitizeExecutionsPage({ items: [
        { ...good, activity: { open_tools: 0 } },
        { ...good, activity: { phase: 'idle', open_tools: 'x' } },
        { ...good, activity: { phase: 'awaiting_input', open_tools: 0, tool: { name: 'Bash' }, since: 'now' } },
      ] }).items
      expect('activity' in rows[0]).toBe(false)
      expect('activity' in rows[1]).toBe(false)
      // Unknown phase is kept verbatim here; normalizePhase maps it at render time.
      expect(rows[2].activity).toEqual({ phase: 'awaiting_input', open_tools: 0 })
    })
  })
})
