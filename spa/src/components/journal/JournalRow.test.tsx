// spa/src/components/journal/JournalRow.test.tsx — a stream row and the pinned band (J-2c, spec §11): kind chip, actor, text, refs,
// the person's name, the read-only checklist of a 待確認 (no coloured left edge), the hover pin button.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import { JournalRow, type RowContext } from './JournalRow'
import { useI18nStore } from '../../stores/useI18nStore'
import type { JournalEntry } from '../../lib/journal/types'

const at = new Date(2026, 9, 14, 14, 5).getTime()
const entry = (over: Partial<JournalEntry> = {}): JournalEntry => ({
  id: 'e1', kind: 'exec', by: '', at, actor: 'Adam', text: 'did the thing', refs: [], items: [], detail: '', pinned: false, ...over,
})
const confirm = (over: Partial<JournalEntry> = {}) => entry({
  id: 'c1', kind: 'confirm', actor: 'Kenji → Wake', text: 'Wake, four things need a decision', pinned: true, detail: 'the question',
  items: [{ text: 'git?', done: true }, { text: 'tasks?', done: false }], ...over,
})
const ctxOf = (over: Partial<RowContext> = {}): RowContext => ({ personName: 'Wake', remoteUrl: 'https://github.com/wake/purdex', selectedId: null, onToggleDetail: vi.fn(), onTogglePin: vi.fn(), ...over })

beforeEach(() => { cleanup(); useI18nStore.getState().setLocale('en') })
afterEach(() => cleanup())

describe('JournalRow', () => {
  it('shows the time, the kind, the actor and the text', () => {
    render(<JournalRow entry={entry()} ctx={ctxOf()} />)
    expect(screen.getByText('14:05')).toBeTruthy()
    expect(screen.getByTestId('journal-kind').getAttribute('data-kind')).toBe('exec')
    expect(screen.getByText('Adam')).toBeTruthy()
    expect(screen.getByText('did the thing')).toBeTruthy()
  })
  it('the person name is highlighted in the actor and in the text, per host (M: the name is hard-coded as Wake)', () => {
    const { unmount } = render(<JournalRow entry={confirm()} ctx={ctxOf({ personName: 'Wake' })} />)
    expect(screen.getAllByTestId('journal-person').map((n) => n.textContent)).toEqual(['Wake', 'Wake'])
    unmount()
    render(<JournalRow entry={entry({ actor: 'Kenji → Mia', text: 'Mia and Wake' })} ctx={ctxOf({ personName: 'Mia' })} />)
    expect(screen.getAllByTestId('journal-person').map((n) => n.textContent)).toEqual(['Mia', 'Mia'])
  })
  it('no person name from the host: nothing highlighted', () => {
    render(<JournalRow entry={confirm()} ctx={ctxOf({ personName: '' })} />)
    expect(screen.queryAllByTestId('journal-person')).toEqual([])
  })
  it('refs are links only with a remote URL', () => {
    const e = entry({ refs: ['#2829', '7bcf08-12'] })
    const { unmount } = render(<JournalRow entry={e} ctx={ctxOf()} />)
    expect(screen.getAllByRole('link')).toHaveLength(1)
    unmount()
    render(<JournalRow entry={e} ctx={ctxOf({ remoteUrl: '' })} />)
    expect(screen.queryAllByRole('link')).toEqual([])
  })
  it('a 待確認 lists its items read-only, the answered ones struck through and checked; no coloured left edge', () => {
    render(<JournalRow entry={confirm()} ctx={ctxOf()} />)
    const items = within(screen.getByTestId('journal-checklist')).getAllByRole('listitem')
    expect(items.map((li) => li.getAttribute('data-done'))).toEqual(['true', null])
    expect(items[0].querySelector('.line-through')?.textContent).toBe('git?')
    expect(items[1].querySelector('.line-through')).toBeNull()
    expect(screen.queryAllByRole('checkbox')).toEqual([])
    const row = screen.getByTestId('journal-entry')
    expect(row.className).not.toMatch(/(^|\s)(border-l|border-s)(-\S+)?(\s|$)|border-status|border-yellow|ring-status/)
    expect(row.getAttribute('style')).toBeNull()
  })
  it('a compact 待確認 row says how many items are answered, in words and not as a raw key', () => {
    render(<JournalRow entry={confirm()} compact ctx={ctxOf()} />)
    expect(screen.getByTestId('journal-answered').textContent).toBe('1/2 answered')
    expect(screen.queryByTestId('journal-checklist')).toBeNull()
    cleanup()
    useI18nStore.getState().setLocale('zh-TW')
    render(<JournalRow entry={confirm()} compact ctx={ctxOf()} />)
    expect(screen.getByTestId('journal-answered').textContent).toBe('已答 1／2 項')
    expect(document.body.textContent).not.toContain('journal.band')
  })
  it('a 待確認 row opens its detail through one labelled button; other rows have none', () => {
    const c = ctxOf()
    const { unmount } = render(<JournalRow entry={confirm()} ctx={c} />)
    const open = screen.getByRole('button', { name: 'Open details' })
    fireEvent.click(open)
    expect(c.onToggleDetail).toHaveBeenCalledWith('c1')
    unmount()
    const c2 = ctxOf()
    render(<JournalRow entry={entry()} ctx={c2} />)
    expect(screen.queryByRole('button', { name: 'Open details' })).toBeNull()
    fireEvent.click(screen.getByTestId('journal-entry'))
    expect(c2.onToggleDetail).not.toHaveBeenCalled()
  })
  it('the row itself is a plain container, and no button sits inside another button (M: a role="button" row holding the pin button)', () => {
    const { container } = render(<JournalRow entry={confirm({ refs: ['#2829'] })} ctx={ctxOf()} />)
    const row = screen.getByTestId('journal-entry')
    expect(row.getAttribute('role')).toBeNull()
    expect(row.getAttribute('tabindex')).toBeNull()
    expect(container.querySelectorAll('[role="button"]')).toHaveLength(0)
    expect(container.querySelectorAll('button button, button a, [role="button"] button, [role="button"] a')).toHaveLength(0)
    expect(container.querySelectorAll('button')).toHaveLength(2) // open details, pin
  })
  it('the open-details button names itself in Chinese too', () => {
    useI18nStore.getState().setLocale('zh-TW')
    render(<JournalRow entry={confirm()} ctx={ctxOf()} />)
    expect(screen.getByRole('button', { name: '打開詳情' })).toBeTruthy()
  })
  it('the pin button toggles the pin and does not open the detail', () => {
    const c = ctxOf()
    const e = confirm()
    render(<JournalRow entry={e} ctx={c} />)
    const btn = screen.getByTestId('journal-pin-toggle')
    expect(btn.getAttribute('aria-label')).toBe('Unpin')
    fireEvent.click(btn)
    expect(c.onTogglePin).toHaveBeenCalledWith(e)
    expect(c.onToggleDetail).not.toHaveBeenCalled()
  })
  it('an unpinned row offers Pin, hidden until hover', () => {
    render(<JournalRow entry={entry()} ctx={ctxOf()} />)
    const btn = screen.getByTestId('journal-pin-toggle')
    expect(btn.getAttribute('aria-label')).toBe('Pin')
    expect(btn.className).toContain('opacity-0')
    expect(btn.className).toContain('group-hover:opacity-100')
  })
  it('uses theme tokens only: no inline colours', () => {
    const { container } = render(<JournalRow entry={confirm()} ctx={ctxOf()} />)
    expect(container.innerHTML).not.toMatch(/style=|#[0-9a-fA-F]{3,8}\b|rgb\(/)
  })
})
