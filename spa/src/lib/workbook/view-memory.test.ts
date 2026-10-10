import { describe, it, expect, beforeEach } from 'vitest'
import { MAX_VIEW_MEMOS, clearViewMemos, forgetViewMemosOfHost, patchViewMemo, readViewMemo, viewMemoCount } from './view-memory'
import { useWorkbookStore } from '../../stores/useWorkbookStore'

beforeEach(() => { clearViewMemos(); useWorkbookStore.getState().reset() })

describe('view memo retention', () => {
  it('is bounded: past the cap the least recently used conversation is dropped', () => {
    for (let i = 0; i < MAX_VIEW_MEMOS + 5; i++) patchViewMemo('h', `c${i}`, { tab: 'todos' })
    expect(viewMemoCount()).toBe(MAX_VIEW_MEMOS)
    expect(readViewMemo('h', 'c0').tab).toBe('log') // gone: back to the initial memo
    expect(readViewMemo('h', `c${MAX_VIEW_MEMOS + 4}`).tab).toBe('todos')
  })
  it('a read or a write counts as use: the recently used one outlives older ones', () => {
    for (let i = 0; i < MAX_VIEW_MEMOS; i++) patchViewMemo('h', `c${i}`, { tab: 'todos' })
    readViewMemo('h', 'c0') // c0 is now the newest
    patchViewMemo('h', 'extra', { tab: 'todos' }) // evicts c1, not c0
    expect(readViewMemo('h', 'c0').tab).toBe('todos')
    expect(readViewMemo('h', 'c1').tab).toBe('log')
  })
  it('reading something never remembered stores nothing', () => {
    readViewMemo('h', 'nope')
    expect(viewMemoCount()).toBe(0)
  })
  it('forgetting a host drops its memos only', () => {
    patchViewMemo('h1', 'c', { tab: 'todos' })
    patchViewMemo('h2', 'c', { tab: 'todos' })
    forgetViewMemosOfHost('h1')
    expect(readViewMemo('h1', 'c').tab).toBe('log')
    expect(readViewMemo('h2', 'c').tab).toBe('todos')
  })
  it('the store forgetting a host clears its memos', () => {
    patchViewMemo('h1', 'c', { tab: 'todos' })
    patchViewMemo('h2', 'c', { tab: 'todos' })
    useWorkbookStore.getState().forgetHost('h1')
    expect(readViewMemo('h1', 'c').tab).toBe('log')
    expect(readViewMemo('h2', 'c').tab).toBe('todos')
  })
  it('the same conversation key keeps its memo (a relay does not reset it)', () => {
    patchViewMemo('h', 'c', { tab: 'todos', openGroups: ['t:A'] })
    patchViewMemo('h', 'c', { scroll: { log: 3, todos: 4 } })
    expect(readViewMemo('h', 'c')).toEqual({ tab: 'todos', openGroups: ['t:A'], scroll: { log: 3, todos: 4 } })
  })
})
