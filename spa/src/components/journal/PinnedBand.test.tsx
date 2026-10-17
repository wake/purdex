// spa/src/components/journal/PinnedBand.test.tsx — the pinned band above the stream (J-2c, spec §11).
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { PinnedBand } from './PinnedBand'
import type { RowContext } from './JournalRow'
import { useI18nStore } from '../../stores/useI18nStore'
import type { JournalEntry } from '../../lib/journal/types'

const entry = (over: Partial<JournalEntry> = {}): JournalEntry => ({
  id: 'e1', kind: 'exec', by: '', at: new Date(2026, 9, 14, 14, 5).getTime(), actor: 'Adam', text: 'did the thing', refs: [], items: [], detail: '', pinned: true, ...over,
})
const confirm = (over: Partial<JournalEntry> = {}) => entry({ id: 'c1', kind: 'confirm', actor: 'Kenji → Wake', text: 'decide', items: [{ text: 'a', done: true }, { text: 'b', done: false }], ...over })
const ctx = (): RowContext => ({ personName: 'Wake', remoteUrl: '', selectedId: null, onToggleDetail: vi.fn(), onTogglePin: vi.fn() })

beforeEach(() => { cleanup(); useI18nStore.getState().setLocale('en') })
afterEach(() => cleanup())

describe('PinnedBand', () => {
  it('lists the pinned entries in the order given, with a title carrying the count', () => {
    render(<PinnedBand pinned={[confirm(), entry({ id: 'e2', text: 'pinned exec' })]} ctx={ctx()} />)
    expect(screen.getAllByTestId('journal-entry').map((n) => n.getAttribute('data-entry-id'))).toEqual(['c1', 'e2'])
    expect(screen.getByTestId('journal-pinned-band').textContent).toContain('Pinned (2)')
  })
  it('a 待確認 shows 「answered n of m」 and not its checklist', () => {
    render(<PinnedBand pinned={[confirm()]} ctx={ctx()} />)
    expect(screen.getByTestId('journal-answered').textContent).toBe('1/2 answered')
    expect(screen.queryByTestId('journal-checklist')).toBeNull()
  })
  it('nothing pinned: nothing drawn', () => {
    const { container } = render(<PinnedBand pinned={[]} ctx={ctx()} />)
    expect(container.firstChild).toBeNull()
  })
  it('every row of the band offers Unpin, even if the entry arrived without the flag', () => {
    const c = ctx()
    render(<PinnedBand pinned={[entry({ pinned: false })]} ctx={c} />)
    const btn = screen.getByTestId('journal-pin-toggle')
    expect(btn.getAttribute('aria-label')).toBe('Unpin')
    fireEvent.click(btn)
    expect(c.onTogglePin).toHaveBeenCalledTimes(1)
  })
})
