// spa/src/lib/journal/filters.test.ts — the five stream filters (J-2c, spec Q-C): which kinds each one keeps, and that clicking
// the active one again clears it.
import { describe, it, expect } from 'vitest'
import { FILTER_IDS, answered, matchesFilter, nextFilter } from './filters'
import { ENTRY_KINDS, type EntryKind, type JournalEntry } from './types'

const entry = (kind: EntryKind, over: Partial<JournalEntry> = {}): JournalEntry => ({
  id: kind, kind, by: '', at: 0, actor: 'A', text: '', refs: [], items: [], detail: '', pinned: false, ...over,
})
const kindsOf = (f: Parameters<typeof matchesFilter>[1]) => ENTRY_KINDS.filter((k) => matchesFilter(entry(k), f))

describe('matchesFilter', () => {
  it('keeps the kinds of each family', () => {
    expect(kindsOf('all')).toEqual([...ENTRY_KINDS])
    expect(kindsOf('exec')).toEqual(['exec', 'exec_done'])
    expect(kindsOf('task')).toEqual(['task_new', 'task_done'])
    expect(kindsOf('comm')).toEqual(['request', 'comm'])
    expect(kindsOf('confirm')).toEqual(['confirm'])
  })
  it('lists the five filters in the prototype order', () => {
    expect(FILTER_IDS).toEqual(['all', 'exec', 'task', 'comm', 'confirm'])
  })
})

describe('nextFilter', () => {
  it('clicking the active filter again goes back to all', () => {
    expect(nextFilter('exec', 'exec')).toBe('all')
    expect(nextFilter('confirm', 'confirm')).toBe('all')
  })
  it('clicking another filter switches to it; all stays all', () => {
    expect(nextFilter('all', 'task')).toBe('task')
    expect(nextFilter('task', 'comm')).toBe('comm')
    expect(nextFilter('all', 'all')).toBe('all')
  })
})

describe('answered', () => {
  it('counts the ticked items of a 待確認', () => {
    const e = entry('confirm', { items: [{ text: 'a', done: true }, { text: 'b', done: false }, { text: 'c', done: true }] })
    expect(answered(e)).toEqual({ done: 2, total: 3 })
    expect(answered(entry('confirm'))).toEqual({ done: 0, total: 0 })
  })
})
