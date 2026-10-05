import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent, within } from '@testing-library/react'
import { LayoutClosingList, LayoutKeepPicker } from './LayoutKeepPicker'
import type { Pane, PaneContent } from '../types/tab'

const terminal = (id: string): Pane => ({
  id,
  content: { kind: 'tmux-session', hostId: 'h1', sessionCode: id, mode: 'terminal', cachedName: `term-${id}`, tmuxInstance: '' },
})
const editorPane = (id: string): Pane => ({ id, content: { kind: 'editor', source: { type: 'inapp' }, filePath: `/src/${id}.md` } as PaneContent })

const box = (id: string) => screen.getByTestId(`layout-keep-option-${id}`) as HTMLInputElement
const confirmBtn = () => screen.getByTestId('layout-keep-confirm') as HTMLButtonElement
const closingLabels = () =>
  within(screen.getByTestId('layout-keep-closing')).queryAllByRole('listitem').map((li) => li.textContent)

function renderPicker(k: number, candidates: Pane[], preselected: string[]) {
  const onConfirm = vi.fn()
  const onCancel = vi.fn()
  render(<LayoutKeepPicker k={k} candidates={candidates} preselected={preselected} onConfirm={onConfirm} onCancel={onCancel} />)
  return { onConfirm, onCancel }
}

describe('LayoutKeepPicker (shell cleanup spec §10, case 3)', () => {
  it('lists one checkbox per candidate with its label, and ticks the preselected ones', () => {
    renderPicker(1, [terminal('a'), terminal('b')], ['b'])
    expect(screen.getByTestId('layout-keep-dialog')).toBeTruthy()
    expect(box('a').checked).toBe(false)
    expect(box('b').checked).toBe(true)
    expect(screen.getByTestId('layout-keep-options').textContent).toContain('term-a')
    expect(screen.getByTestId('layout-keep-options').textContent).toContain('term-b')
  })

  it('k = 1 behaves like a radio: ticking another moves the tick', () => {
    renderPicker(1, [terminal('a'), terminal('b')], ['b'])
    fireEvent.click(box('a'))
    expect(box('a').checked).toBe(true)
    expect(box('b').checked).toBe(false)
  })

  it('k = 2 refuses a third tick', () => {
    renderPicker(2, [terminal('a'), terminal('b'), terminal('c')], ['c', 'a'])
    expect(box('b').disabled).toBe(true)
    fireEvent.click(box('b'))
    expect(box('b').checked).toBe(false)
    expect(box('a').checked).toBe(true)
    expect(box('c').checked).toBe(true)
  })

  it('confirm is disabled until exactly k are ticked', () => {
    renderPicker(2, [terminal('a'), terminal('b'), terminal('c')], ['a', 'b'])
    expect(confirmBtn().disabled).toBe(false)
    fireEvent.click(box('a'))
    expect(confirmBtn().disabled).toBe(true)
    fireEvent.click(box('c'))
    expect(confirmBtn().disabled).toBe(false)
  })

  it('confirm is disabled at zero ticks (k = 1)', () => {
    renderPicker(1, [terminal('a'), terminal('b')], ['a'])
    fireEvent.click(box('a'))
    expect(box('a').checked).toBe(false)
    expect(confirmBtn().disabled).toBe(true)
  })

  it('confirmLocked keeps confirm inert even at exactly k ticks; the ticks still work', () => {
    const onConfirm = vi.fn()
    const props = { k: 1, candidates: [terminal('a'), terminal('b')], preselected: ['a'], onConfirm, onCancel: vi.fn() }
    const { rerender } = render(<LayoutKeepPicker {...props} confirmLocked />)
    expect(confirmBtn().disabled).toBe(true)
    fireEvent.click(box('b'))
    expect(box('b').checked).toBe(true)
    fireEvent.click(confirmBtn())
    expect(onConfirm).not.toHaveBeenCalled()

    rerender(<LayoutKeepPicker {...props} confirmLocked={false} />)
    expect(confirmBtn().disabled).toBe(false)
    fireEvent.click(confirmBtn())
    expect(onConfirm).toHaveBeenCalledWith(['b'])
  })

  it('confirm hands back the ticked pane ids', () => {
    const { onConfirm } = renderPicker(2, [terminal('a'), terminal('b'), terminal('c')], ['a', 'b'])
    fireEvent.click(box('a'))
    fireEvent.click(box('c'))
    fireEvent.click(confirmBtn())
    expect(onConfirm).toHaveBeenCalledTimes(1)
    expect([...onConfirm.mock.calls[0][0]].sort()).toEqual(['b', 'c'])
  })

  it('the closing list updates live with what is not ticked', () => {
    renderPicker(1, [terminal('a'), terminal('b'), terminal('c')], ['a'])
    expect(closingLabels()).toEqual(['term-b', 'term-c'])
    fireEvent.click(box('c'))
    expect(closingLabels()).toEqual(['term-a', 'term-b'])
  })

  it('cancel calls onCancel and never onConfirm', () => {
    const { onConfirm, onCancel } = renderPicker(1, [terminal('a'), terminal('b')], ['a'])
    fireEvent.click(screen.getByTestId('layout-keep-cancel'))
    expect(onCancel).toHaveBeenCalledTimes(1)
    expect(onConfirm).not.toHaveBeenCalled()
  })

  it('warns about unsaved changes only while an editor is among the panes that close', () => {
    renderPicker(1, [editorPane('notes'), terminal('a')], ['notes'])
    expect(screen.queryByTestId('layout-keep-editor-note')).toBeNull()
    fireEvent.click(box('a'))
    expect(screen.getByTestId('layout-keep-editor-note')).toBeTruthy()
  })
})

describe('LayoutClosingList', () => {
  it('lists each pane by its display label', () => {
    render(<LayoutClosingList testIdPrefix="x" panes={[terminal('a'), editorPane('readme')]} />)
    const items = within(screen.getByTestId('x-closing')).getAllByRole('listitem').map((li) => li.textContent)
    expect(items).toEqual(['term-a', 'readme.md'])
    expect(screen.getByTestId('x-editor-note')).toBeTruthy()
  })
})
