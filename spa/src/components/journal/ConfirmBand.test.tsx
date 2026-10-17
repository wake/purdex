// spa/src/components/journal/ConfirmBand.test.tsx — the 待確認 band above the stream (JV-3, spec §3.4).
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { ConfirmBand } from './ConfirmBand'
import type { RowContext } from './JournalRow'
import { useI18nStore } from '../../stores/useI18nStore'
import type { JournalEntry } from '../../lib/journal/types'

const confirm = (over: Partial<JournalEntry> = {}): JournalEntry => ({
  id: 'c1', kind: 'confirm', by: '', at: new Date(2026, 9, 14, 14, 5).getTime(), actor: 'Kenji → Wake', text: 'decide', refs: [], detail: '',
  items: [{ text: 'a', done: true }, { text: 'b', done: false }], ...over,
})
const ctx = (over: Partial<RowContext> = {}): RowContext => ({ personName: 'Wake', remoteUrl: '', selectedId: null, onToggleDetail: vi.fn(), ...over })

beforeEach(() => { cleanup(); useI18nStore.getState().setLocale('en') })
afterEach(() => cleanup())

describe('ConfirmBand', () => {
  it('lists the entries in the order given, with a title carrying the count', () => {
    render(<ConfirmBand entries={[confirm(), confirm({ id: 'c2' })]} ctx={ctx()} />)
    expect(screen.getAllByTestId('journal-entry').map((n) => n.getAttribute('data-entry-id'))).toEqual(['c1', 'c2'])
    expect(screen.getByTestId('journal-confirm-band').textContent).toContain('To confirm (2)')
  })
  it('shows 「answered n of m」 and the items', () => {
    render(<ConfirmBand entries={[confirm()]} ctx={ctx()} />)
    expect(screen.getByTestId('journal-answered').textContent).toBe('1/2 answered')
  })
  it('each item has a box that checks it when the context can', () => {
    const onToggleItem = vi.fn()
    render(<ConfirmBand entries={[confirm()]} ctx={ctx({ onToggleItem })} />)
    fireEvent.click(screen.getAllByTestId('journal-item-check')[1])
    expect(onToggleItem).toHaveBeenCalledWith(expect.objectContaining({ id: 'c1' }), 1, true)
  })
  it('nothing open: nothing drawn', () => {
    const { container } = render(<ConfirmBand entries={[]} ctx={ctx()} />)
    expect(container.firstChild).toBeNull()
  })
})
