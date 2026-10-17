// spa/src/stores/useJournalUiStore.test.ts — the persisted card arrangement (J-2b T-2b.3).
import { beforeEach, describe, expect, it } from 'vitest'
import { act, renderHook } from '@testing-library/react'
import { CARD_DEFAULT, CARD_MAX, CARD_MIN, cardWidth, useJournalUiStore } from './useJournalUiStore'
import { useViewMemo, patchUnitMemo } from '../lib/journal/use-view-memo'
import { clearViewMemos } from '../lib/journal/view-memory'

beforeEach(() => { localStorage.clear(); useJournalUiStore.setState({ lastWidths: {} }); clearViewMemos() })

describe('useJournalUiStore', () => {
  it('clamps a width and ignores a non-finite one', () => {
    useJournalUiStore.getState().setLastWidth('journal', 10)
    expect(useJournalUiStore.getState().lastWidths.journal).toBe(CARD_MIN)
    useJournalUiStore.getState().setLastWidth('journal', 9999)
    expect(useJournalUiStore.getState().lastWidths.journal).toBe(CARD_MAX)
    useJournalUiStore.getState().setLastWidth('journal', NaN)
    expect(useJournalUiStore.getState().lastWidths.journal).toBe(CARD_MAX)
  })
  it('an old save\'s team card state is dropped on load (the team card is gone, JV-2)', () => {
    localStorage.setItem('purdex-journal-ui', JSON.stringify({ state: { lastWidths: { journal: 500, team: 400 }, teamOpen: { 'h\u0000t1': true } }, version: 1 }))
    void useJournalUiStore.persist.rehydrate()
    expect(useJournalUiStore.getState().lastWidths).toEqual({ journal: 500 })
    expect('teamOpen' in useJournalUiStore.getState()).toBe(false)
  })
  it('a card width is the unit own, else the last used, else the default', () => {
    expect(cardWidth('tasks', 500, { tasks: 400 })).toBe(500)
    expect(cardWidth('tasks', null, { tasks: 400 })).toBe(400)
    expect(cardWidth('tasks', null, {})).toBe(CARD_DEFAULT.tasks)
  })
})

describe('useViewMemo', () => {
  it('re-renders when the unit memo is patched, and not for a null unit', () => {
    const { result } = renderHook(() => useViewMemo('h1', 'team:a'))
    expect(result.current.journalOpen).toBe(false)
    act(() => patchUnitMemo('h1', 'team:a', { journalOpen: true }))
    expect(result.current.journalOpen).toBe(true)
    const none = renderHook(() => useViewMemo('h1', null))
    act(() => patchUnitMemo('h1', null, { journalOpen: true }))
    expect(none.result.current.journalOpen).toBe(false)
  })
})
