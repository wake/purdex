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
    expect(r.archived).toBe(true)
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
})
