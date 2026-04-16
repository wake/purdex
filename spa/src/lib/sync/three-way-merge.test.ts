import { describe, it, expect } from 'vitest'
import { detectConflict, mergeCollection } from './three-way-merge'
import type { ConflictResult, MergeCollectionResult } from './three-way-merge'

// =============================================================================
// detectConflict
// =============================================================================

describe('detectConflict', () => {
  it('returns no-change when all three are equal (primitives)', () => {
    expect(detectConflict('hello', 'hello', 'hello')).toBe('no-change')
    expect(detectConflict(42, 42, 42)).toBe('no-change')
    expect(detectConflict(true, true, true)).toBe('no-change')
    expect(detectConflict(null, null, null)).toBe('no-change')
  })

  it('returns use-local when only local changed', () => {
    expect(detectConflict('old', 'new-local', 'old')).toBe('use-local')
    expect(detectConflict(1, 2, 1)).toBe('use-local')
  })

  it('returns use-remote when only remote changed', () => {
    expect(detectConflict('old', 'old', 'new-remote')).toBe('use-remote')
    expect(detectConflict(1, 1, 99)).toBe('use-remote')
  })

  it('returns both-same when both changed to the same value', () => {
    expect(detectConflict('old', 'new', 'new')).toBe('both-same')
    expect(detectConflict(0, 5, 5)).toBe('both-same')
  })

  it('returns conflict when both changed to different values', () => {
    expect(detectConflict('old', 'local-new', 'remote-new')).toBe('conflict')
    expect(detectConflict(1, 2, 3)).toBe('conflict')
  })

  it('handles nested objects with deep equality — no-change', () => {
    const obj = { a: 1, b: { c: 2 } }
    expect(detectConflict(obj, { a: 1, b: { c: 2 } }, { a: 1, b: { c: 2 } })).toBe('no-change')
  })

  it('handles nested objects with deep equality — use-local', () => {
    const last = { theme: 'dark', locale: 'en' }
    const local = { theme: 'light', locale: 'en' }
    const remote = { theme: 'dark', locale: 'en' }
    expect(detectConflict(last, local, remote)).toBe('use-local')
  })

  it('handles nested objects with deep equality — conflict', () => {
    const last = { x: 1 }
    const local = { x: 2 }
    const remote = { x: 3 }
    expect(detectConflict(last, local, remote)).toBe('conflict')
  })

  it('handles arrays with deep equality — no-change', () => {
    expect(detectConflict([1, 2, 3], [1, 2, 3], [1, 2, 3])).toBe('no-change')
  })

  it('handles arrays with deep equality — use-remote', () => {
    expect(detectConflict([1, 2], [1, 2], [1, 2, 3])).toBe('use-remote')
  })

  it('handles null values', () => {
    // last=null, local=null, remote='something' → use-remote
    expect(detectConflict(null, null, 'something')).toBe('use-remote')
    // last=null, local='something', remote=null → use-local
    expect(detectConflict(null, 'something', null)).toBe('use-local')
    // last='something', both set to null → both-same
    expect(detectConflict('something', null, null)).toBe('both-same')
  })

  it('handles undefined values', () => {
    // all undefined → no-change
    expect(detectConflict(undefined, undefined, undefined)).toBe('no-change')
    // last=undefined, only remote provided → use-remote
    expect(detectConflict(undefined, undefined, 'val')).toBe('use-remote')
    // last=undefined, both same → both-same
    expect(detectConflict(undefined, 'val', 'val')).toBe('both-same')
  })
})

// =============================================================================
// mergeCollection
// =============================================================================

describe('mergeCollection', () => {
  it('auto-merges non-conflicting fields (A changes theme, B changes locale)', () => {
    const last = { theme: 'dark', locale: 'en' }
    const local = { theme: 'light', locale: 'en' }   // A changed theme
    const remote = { theme: 'dark', locale: 'zh-TW' } // B changed locale

    const result = mergeCollection(last, local, remote, 'device-B')
    expect(result.conflicts).toHaveLength(0)
    expect(result.merged).toEqual({ theme: 'light', locale: 'zh-TW' })
  })

  it('collects conflicts for double-changed fields', () => {
    const last = { theme: 'dark', locale: 'en' }
    const local = { theme: 'light', locale: 'zh-TW' }  // changed both
    const remote = { theme: 'solarized', locale: 'ja' } // changed both differently

    const result = mergeCollection(last, local, remote, 'device-B')
    expect(result.conflicts).toHaveLength(2)

    const themeConflict = result.conflicts.find(c => c.field === 'theme')
    expect(themeConflict).toBeDefined()
    expect(themeConflict!.contributor).toBe('')
    expect(themeConflict!.lastSynced).toBe('dark')
    expect(themeConflict!.local).toBe('light')
    expect(themeConflict!.remote.value).toBe('solarized')
    expect(themeConflict!.remote.device).toBe('device-B')

    // merged keeps local as placeholder
    expect(result.merged.theme).toBe('light')
    expect(result.merged.locale).toBe('zh-TW')
  })

  it('new keys added only on local side win without conflict', () => {
    const last = { a: 1 }
    const local = { a: 1, b: 2 }  // added 'b'
    const remote = { a: 1 }

    const result = mergeCollection(last, local, remote, 'device-B')
    expect(result.conflicts).toHaveLength(0)
    expect(result.merged).toEqual({ a: 1, b: 2 })
  })

  it('new keys added only on remote side win without conflict', () => {
    const last = { a: 1 }
    const local = { a: 1 }
    const remote = { a: 1, c: 3 }  // added 'c'

    const result = mergeCollection(last, local, remote, 'device-B')
    expect(result.conflicts).toHaveLength(0)
    expect(result.merged).toEqual({ a: 1, c: 3 })
  })

  it('new keys added on both sides with same value → both-same, use local', () => {
    const last = { a: 1 }
    const local = { a: 1, newKey: 'same' }
    const remote = { a: 1, newKey: 'same' }

    const result = mergeCollection(last, local, remote, 'device-B')
    expect(result.conflicts).toHaveLength(0)
    expect(result.merged.newKey).toBe('same')
  })

  it('new keys added on both sides with different values → conflict', () => {
    const last = { a: 1 }
    const local = { a: 1, newKey: 'local-val' }
    const remote = { a: 1, newKey: 'remote-val' }

    const result = mergeCollection(last, local, remote, 'device-B')
    expect(result.conflicts).toHaveLength(1)
    expect(result.conflicts[0].field).toBe('newKey')
    // merged keeps local as placeholder
    expect(result.merged.newKey).toBe('local-val')
  })

  it('key deleted locally but unchanged remotely → deletion respected', () => {
    const last = { a: 1, b: 2 }
    const local = { a: 1 }          // deleted 'b' locally
    const remote = { a: 1, b: 2 }   // remote unchanged

    const result = mergeCollection(last, local, remote, 'device-B')
    expect(result.conflicts).toHaveLength(0)
    expect('b' in result.merged).toBe(false)
    expect(result.merged).toEqual({ a: 1 })
  })

  it('key deleted remotely but unchanged locally → deletion respected', () => {
    const last = { a: 1, b: 2 }
    const local = { a: 1, b: 2 }    // local unchanged
    const remote = { a: 1 }          // deleted 'b' remotely

    const result = mergeCollection(last, local, remote, 'device-B')
    expect(result.conflicts).toHaveLength(0)
    expect('b' in result.merged).toBe(false)
    expect(result.merged).toEqual({ a: 1 })
  })

  it('null last (first sync) → full-replace from remote, no conflicts', () => {
    const remote = { theme: 'light', locale: 'en', extra: 42 }
    const local = { theme: 'dark', locale: 'zh-TW' }

    const result = mergeCollection(null, local, remote, 'device-B')
    expect(result.conflicts).toHaveLength(0)
    expect(result.merged).toEqual(remote)
  })

  it('both-same field: uses local value, no conflict', () => {
    const last = { x: 1 }
    const local = { x: 99 }
    const remote = { x: 99 }  // both changed to same

    const result = mergeCollection(last, local, remote, 'device-B')
    expect(result.conflicts).toHaveLength(0)
    expect(result.merged.x).toBe(99)
  })

  it('unchanged field: keeps local value, no conflict', () => {
    const last = { x: 42 }
    const local = { x: 42 }
    const remote = { x: 42 }

    const result = mergeCollection(last, local, remote, 'device-B')
    expect(result.conflicts).toHaveLength(0)
    expect(result.merged.x).toBe(42)
  })
})
