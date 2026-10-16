// spa/src/components/journal/JournalFilters.test.tsx — the filter chips of the journal card (J-2c, spec Q-C). The chosen filter lives
// in the unit's view memo (Tab-hosted rule), so the test reads it from there, not from the DOM.
import { describe, it, expect, beforeEach, afterEach } from 'vitest'
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { JournalFilters } from './JournalFilters'
import { clearViewMemos, patchViewMemo, readViewMemo } from '../../lib/journal/view-memory'
import { useI18nStore } from '../../stores/useI18nStore'

const H = 'h1'
const K = 'solo:a'
const click = (id: string) => act(() => { fireEvent.click(screen.getByTestId(`journal-filter-${id}`)) })

beforeEach(() => {
  cleanup()
  clearViewMemos()
  useI18nStore.getState().setLocale('en')
})
afterEach(() => cleanup())

describe('JournalFilters', () => {
  it('clicking a filter stores it in the view memo; clicking the active one again clears it (M: the filter sticks)', () => {
    render(<JournalFilters host={H} unitKey={K} openCount={0} />)
    click('exec')
    expect(readViewMemo(H, K).filter).toBe('exec')
    expect(screen.getByTestId('journal-filter-exec').getAttribute('aria-selected')).toBe('true')
    click('exec')
    expect(readViewMemo(H, K).filter).toBe('all')
    expect(screen.getByTestId('journal-filter-all').getAttribute('aria-selected')).toBe('true')
  })
  it('switching straight from one filter to another does not pass through all', () => {
    render(<JournalFilters host={H} unitKey={K} openCount={0} />)
    click('task')
    click('comm')
    expect(readViewMemo(H, K).filter).toBe('comm')
  })
  it('shows the filter the memo holds (a remount keeps it)', () => {
    patchViewMemo(H, K, { filter: 'confirm' })
    render(<JournalFilters host={H} unitKey={K} openCount={0} />)
    expect(screen.getByTestId('journal-filter-confirm').getAttribute('aria-selected')).toBe('true')
  })
  it('the 待確認 chip carries the open item count, nothing at 0', () => {
    const { rerender } = render(<JournalFilters host={H} unitKey={K} openCount={4} />)
    expect(screen.getByTestId('journal-filter-confirm-count').textContent).toBe('4')
    rerender(<JournalFilters host={H} unitKey={K} openCount={0} />)
    expect(screen.queryByTestId('journal-filter-confirm-count')).toBeNull()
  })
})
