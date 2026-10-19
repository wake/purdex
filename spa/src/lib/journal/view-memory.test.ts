// spa/src/lib/journal/view-memory.test.ts — per-unit view memory retention (J-2a T-2a.5).
import { beforeEach, describe, expect, it } from 'vitest'
import { MAX_VIEW_MEMOS, clearViewMemos, forgetViewMemo, forgetViewMemosOfHost, initialViewMemo, patchScrollMemo, patchViewMemo, readScrollMemo, readViewMemo, viewMemoCount, MAX_PAGE_SCROLLS } from './view-memory'

beforeEach(() => clearViewMemos())

describe('journal view memory', () => {
  it('a patch merges into what the unit had and survives a read', () => {
    patchViewMemo('h', 'team:t1', { journalOpen: true, filter: 'confirm' })
    patchViewMemo('h', 'team:t1', { scroll: 120, pinnedToBottom: false, detailId: 'wb:1:1', widths: { journal: 480, roadmap: null, tasks: null } })
    expect(readViewMemo('h', 'team:t1')).toEqual({ ...initialViewMemo(), journalOpen: true, filter: 'confirm', scroll: 120, pinnedToBottom: false, detailId: 'wb:1:1', widths: { journal: 480, roadmap: null, tasks: null } })
  })
  it('units are separate, hosts are separate', () => {
    patchViewMemo('h1', 'solo:a', { tasksOpen: true })
    expect(readViewMemo('h1', 'solo:b').tasksOpen).toBe(false)
    expect(readViewMemo('h2', 'solo:a').tasksOpen).toBe(false)
  })
  it('is bounded: past the cap the least recently used unit is forgotten', () => {
    for (let i = 0; i < MAX_VIEW_MEMOS + 5; i++) patchViewMemo('h', `u${i}`, { journalOpen: true })
    expect(viewMemoCount()).toBe(MAX_VIEW_MEMOS)
    expect(readViewMemo('h', 'u0').journalOpen).toBe(false)
    expect(readViewMemo('h', `u${MAX_VIEW_MEMOS + 4}`).journalOpen).toBe(true)
  })
  it('a read counts as use', () => {
    for (let i = 0; i < MAX_VIEW_MEMOS; i++) patchViewMemo('h', `u${i}`, { journalOpen: true })
    readViewMemo('h', 'u0')
    patchViewMemo('h', 'extra', { journalOpen: true })
    expect(readViewMemo('h', 'u0').journalOpen).toBe(true)
    expect(readViewMemo('h', 'u1').journalOpen).toBe(false)
  })
  it('reading something never remembered stores nothing', () => {
    readViewMemo('h', 'nope')
    expect(viewMemoCount()).toBe(0)
  })
  it('forgetting a unit or a host drops only theirs', () => {
    patchViewMemo('h1', 'a', { journalOpen: true })
    patchViewMemo('h1', 'b', { journalOpen: true })
    patchViewMemo('h2', 'a', { journalOpen: true })
    forgetViewMemo('h1', 'a')
    expect(readViewMemo('h1', 'a').journalOpen).toBe(false)
    forgetViewMemosOfHost('h1')
    expect(readViewMemo('h1', 'b').journalOpen).toBe(false)
    expect(readViewMemo('h2', 'a').journalOpen).toBe(true)
  })
  it('a host id that is a prefix of another does not take its memos', () => {
    patchViewMemo('h', 'a', { journalOpen: true })
    patchViewMemo('h2', 'a', { journalOpen: true })
    forgetViewMemosOfHost('h')
    expect(readViewMemo('h2', 'a').journalOpen).toBe(true)
  })
})

describe('scroll memory per session page', () => {
  it('each page keeps its own place; a page never seen opens at the bottom', () => {
    patchScrollMemo('h', 'u', 'p1', { scroll: 40, pinnedToBottom: false })
    patchScrollMemo('h', 'u', 'p2', { scroll: 90, pinnedToBottom: true })
    expect(readScrollMemo('h', 'u', 'p1')).toEqual({ scroll: 40, pinnedToBottom: false })
    expect(readScrollMemo('h', 'u', 'p2')).toEqual({ scroll: 90, pinnedToBottom: true })
    expect(readScrollMemo('h', 'u', 'p3')).toEqual({ scroll: 0, pinnedToBottom: true })
  })
  it('the unit\'s own scroll is the no-session-pages place and is not a page\'s', () => {
    patchScrollMemo('h', 'u', '', { scroll: 7, pinnedToBottom: false })
    expect(readViewMemo('h', 'u')).toMatchObject({ scroll: 7, pinnedToBottom: false })
    expect(readScrollMemo('h', 'u', 'p1')).toEqual({ scroll: 0, pinnedToBottom: true })
  })
  it('units are separate and the page choice is remembered per unit', () => {
    patchScrollMemo('h', 'a', 'p1', { scroll: 5, pinnedToBottom: false })
    patchViewMemo('h', 'a', { page: 'p1' })
    expect(readScrollMemo('h', 'b', 'p1')).toEqual({ scroll: 0, pinnedToBottom: true })
    expect(readViewMemo('h', 'b').page).toBeNull()
    expect(readViewMemo('h', 'a').page).toBe('p1')
  })
  it('is bounded per unit', () => {
    for (let i = 0; i < MAX_PAGE_SCROLLS + 3; i++) patchScrollMemo('h', 'u', `p${i}`, { scroll: i + 1, pinnedToBottom: false })
    expect(Object.keys(readViewMemo('h', 'u').pageScroll)).toHaveLength(MAX_PAGE_SCROLLS)
    expect(readScrollMemo('h', 'u', 'p0')).toEqual({ scroll: 0, pinnedToBottom: true })
    expect(readScrollMemo('h', 'u', `p${MAX_PAGE_SCROLLS + 2}`).scroll).toBe(MAX_PAGE_SCROLLS + 3)
  })
})
