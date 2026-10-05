// spa/src/components/ConfirmDialog.test.tsx — the shared confirm modal's focus handling (shell polish spec §4, "A dialog
// opened from a covered button takes focus itself"): it takes focus onto its panel when it opens, keeps Tab inside
// while it is up, and hands focus back on close only while it still holds it. Plus the behaviour it already had:
// Escape / backdrop cancel, inert while busy.
//
// jsdom does not move focus on Tab by itself, so every Tab here is the dialog's own keydown handler at work.
import { describe, it, expect, vi } from 'vitest'
import { useState } from 'react'
import { render, screen, fireEvent } from '@testing-library/react'
import { ConfirmDialog, type ConfirmDialogProps } from './ConfirmDialog'

type Over = Partial<Omit<ConfirmDialogProps, 'testIdPrefix' | 'title' | 'body' | 'confirmLabel'>>

/** A textarea standing in for the pane that had focus, the dialog when `open`, and a button outside the dialog. */
function Page({ open, withPane = true, over = {} }: { open: boolean; withPane?: boolean; over?: Over }) {
  return (
    <div>
      {withPane && <textarea data-testid="pane" />}
      <button data-testid="outside">outside</button>
      {open && <ConfirmDialog testIdPrefix="x" title="Title" body="Body" confirmLabel="OK" onCancel={() => {}} onConfirm={() => {}} {...over} />}
    </div>
  )
}

/** The dialog closes itself on Cancel, the way every caller wires it. */
function SelfClosing() {
  const [open, setOpen] = useState(false)
  return (
    <div>
      <textarea data-testid="pane" />
      <button data-testid="open" onClick={() => setOpen(true)}>open</button>
      {open && <ConfirmDialog testIdPrefix="x" title="Title" body="Body" confirmLabel="OK" onCancel={() => setOpen(false)} onConfirm={() => setOpen(false)} />}
    </div>
  )
}

const pane = () => screen.getByTestId('pane')
const panel = () => screen.getByTestId('x-panel')
const cancel = () => screen.getByTestId('x-cancel')
const confirm = () => screen.getByTestId('x-confirm')
const box = () => screen.getByTestId('box')
const active = () => document.activeElement

/** Opens the dialog with `pane` focused, as a covered title bar / status bar button does (rule F). */
function openFromPane(over: Over = {}) {
  const view = render(<Page open={false} over={over} />)
  pane().focus()
  expect(active()).toBe(pane())
  view.rerender(<Page open over={over} />)
  return view
}

const tab = (shift = false) => fireEvent.keyDown(active()!, { key: 'Tab', shiftKey: shift })

describe('ConfirmDialog — takes focus when it opens', () => {
  it('focus moves from the pane to the dialog panel itself — not to any button', () => {
    const onConfirm = vi.fn()
    const onCancel = vi.fn()
    openFromPane({ onConfirm, onCancel })
    expect(active()).toBe(panel())
    expect(active()).not.toBe(pane())
    expect(active()?.tagName).not.toBe('BUTTON')
    expect(panel().tabIndex).toBe(-1)
    expect(screen.getByTestId('x-dialog').contains(panel())).toBe(true)

    // Enter (or any key typed) lands on the panel: it neither confirms nor cancels.
    fireEvent.keyDown(active()!, { key: 'Enter' })
    fireEvent.keyUp(active()!, { key: 'Enter' })
    expect(onConfirm).not.toHaveBeenCalled()
    expect(onCancel).not.toHaveBeenCalled()
  })
})

describe('ConfirmDialog — Tab stays inside', () => {
  const withBox: Over = { children: <input type="checkbox" data-testid="box" /> }

  it('Tab walks the panel → first control → … → last, then wraps to the first', () => {
    openFromPane(withBox)
    expect(tab()).toBe(false)
    expect(active()).toBe(box())
    tab()
    expect(active()).toBe(cancel())
    tab()
    expect(active()).toBe(confirm())
    expect(tab()).toBe(false)
    expect(active()).toBe(box())
  })

  it('Shift+Tab walks backwards: from the panel to the last control, and from the first back round to the last', () => {
    openFromPane(withBox)
    expect(tab(true)).toBe(false)
    expect(active()).toBe(confirm())
    tab(true)
    expect(active()).toBe(cancel())
    tab(true)
    expect(active()).toBe(box())
    expect(tab(true)).toBe(false)
    expect(active()).toBe(confirm())
  })

  it('a disabled control is skipped: with Confirm inert, Cancel is the last stop', () => {
    openFromPane({ confirmDisabled: true })
    tab()
    expect(active()).toBe(cancel())
    tab()
    expect(active()).toBe(cancel())
    tab(true)
    expect(active()).toBe(cancel())
  })

  it('busy (every control disabled): Tab keeps focus on the panel', () => {
    openFromPane({ busy: true })
    expect(tab()).toBe(false)
    expect(active()).toBe(panel())
    expect(tab(true)).toBe(false)
    expect(active()).toBe(panel())
  })

  it('focus outside the dialog while it is up (e.g. on body): Tab brings it back in, never further out', () => {
    openFromPane(withBox)
    ;(active() as HTMLElement).blur()
    expect(active()).toBe(document.body)
    tab()
    expect(active()).toBe(box())
    screen.getByTestId('outside').focus()
    tab(true)
    expect(active()).toBe(confirm())
  })

  it('other keys, and Tab with Ctrl / Cmd / Alt (Ctrl+Tab switches tabs), are left alone', () => {
    openFromPane(withBox)
    expect(fireEvent.keyDown(active()!, { key: 'a' })).toBe(true)
    expect(fireEvent.keyDown(active()!, { key: 'Tab', ctrlKey: true })).toBe(true)
    expect(fireEvent.keyDown(active()!, { key: 'Tab', ctrlKey: true, shiftKey: true })).toBe(true)
    expect(fireEvent.keyDown(active()!, { key: 'Tab', metaKey: true })).toBe(true)
    expect(fireEvent.keyDown(active()!, { key: 'Tab', altKey: true })).toBe(true)
    expect(active()).toBe(panel())
  })
})

describe('ConfirmDialog — gives focus back on close only while it still holds it', () => {
  it('focus on the panel at close → back to the pane', () => {
    const view = openFromPane()
    expect(active()).toBe(panel())
    view.rerender(<Page open={false} />)
    expect(active()).toBe(pane())
  })

  it('Cancel clicked (focus on a dialog button) → back to the pane', () => {
    render(<SelfClosing />)
    pane().focus()
    fireEvent.click(screen.getByTestId('open'))
    cancel().focus()
    fireEvent.click(cancel())
    expect(screen.queryByTestId('x-dialog')).toBeNull()
    expect(active()).toBe(pane())
  })

  it('Escape → back to the pane', () => {
    render(<SelfClosing />)
    pane().focus()
    fireEvent.click(screen.getByTestId('open'))
    fireEvent.keyDown(active()!, { key: 'Escape' })
    expect(screen.queryByTestId('x-dialog')).toBeNull()
    expect(active()).toBe(pane())
  })

  it('focus already taken by something outside (a tab switch focused the new pane) → left where it is', () => {
    const view = openFromPane()
    const elsewhere = screen.getByTestId('outside')
    elsewhere.focus()
    view.rerender(<Page open={false} />)
    expect(active()).toBe(elsewhere)
  })

  it('the element that had focus is gone by the close → no throw, focus is not forced anywhere', () => {
    const view = openFromPane()
    view.rerender(<Page open withPane={false} />)
    expect(screen.queryByTestId('pane')).toBeNull()
    expect(() => view.rerender(<Page open={false} withPane={false} />)).not.toThrow()
    expect(active()).toBe(document.body)
  })

  it('nothing focused when it opened (body) → body afterwards', () => {
    const view = render(<Page open={false} />)
    expect(active()).toBe(document.body)
    view.rerender(<Page open />)
    expect(active()).toBe(panel())
    view.rerender(<Page open={false} />)
    expect(active()).toBe(document.body)
  })
})

describe('ConfirmDialog — what it already did', () => {
  it('Escape cancels — from the panel, from a button, and from the document', () => {
    const onCancel = vi.fn()
    openFromPane({ onCancel })
    fireEvent.keyDown(panel(), { key: 'Escape' })
    expect(onCancel).toHaveBeenCalledTimes(1)
    fireEvent.keyDown(cancel(), { key: 'Escape' })
    expect(onCancel).toHaveBeenCalledTimes(2)
    fireEvent.keyDown(document, { key: 'Escape' })
    expect(onCancel).toHaveBeenCalledTimes(3)
  })

  it('busy: Escape and the backdrop do nothing; both buttons are disabled', () => {
    const onCancel = vi.fn()
    const onConfirm = vi.fn()
    openFromPane({ busy: true, onCancel, onConfirm })
    fireEvent.keyDown(panel(), { key: 'Escape' })
    fireEvent.click(screen.getByTestId('x-dialog'))
    expect(onCancel).not.toHaveBeenCalled()
    expect(cancel()).toBeDisabled()
    expect(confirm()).toBeDisabled()
  })

  it('a backdrop click cancels; a click on the panel does not', () => {
    const onCancel = vi.fn()
    openFromPane({ onCancel })
    fireEvent.click(panel())
    expect(onCancel).not.toHaveBeenCalled()
    fireEvent.click(screen.getByTestId('x-dialog'))
    expect(onCancel).toHaveBeenCalledTimes(1)
  })

  it('confirmDisabled: Confirm is inert, Cancel stays live', () => {
    const onCancel = vi.fn()
    const onConfirm = vi.fn()
    openFromPane({ confirmDisabled: true, onCancel, onConfirm })
    expect(confirm()).toBeDisabled()
    fireEvent.click(confirm())
    expect(onConfirm).not.toHaveBeenCalled()
    fireEvent.click(cancel())
    expect(onCancel).toHaveBeenCalledTimes(1)
  })
})
