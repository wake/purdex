// spa/src/components/room/WorkerInput.test.tsx
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import WorkerInput from './WorkerInput'

beforeEach(() => {
  cleanup()
})

describe('WorkerInput', () => {
  it('renders textarea', () => {
    render(<WorkerInput onSend={vi.fn()} />)
    expect(screen.getByRole('textbox')).toBeInTheDocument()
  })

  it('calls onSend on Enter key', () => {
    const onSend = vi.fn()
    render(<WorkerInput onSend={onSend} />)
    const textarea = screen.getByRole('textbox')
    fireEvent.change(textarea, { target: { value: 'Enter test' } })
    fireEvent.keyDown(textarea, { key: 'Enter', code: 'Enter' })
    expect(onSend).toHaveBeenCalledWith('Enter test')
  })

  it('does NOT send on Shift+Enter', () => {
    const onSend = vi.fn()
    render(<WorkerInput onSend={onSend} />)
    const textarea = screen.getByRole('textbox')
    fireEvent.change(textarea, { target: { value: 'multiline' } })
    fireEvent.keyDown(textarea, { key: 'Enter', code: 'Enter', shiftKey: true })
    expect(onSend).not.toHaveBeenCalled()
  })

  it('clears textarea after send', () => {
    render(<WorkerInput onSend={vi.fn()} />)
    const textarea = screen.getByRole('textbox') as HTMLTextAreaElement
    fireEvent.change(textarea, { target: { value: 'test message' } })
    fireEvent.keyDown(textarea, { key: 'Enter', code: 'Enter' })
    expect(textarea.value).toBe('')
  })

  it('is disabled when disabled prop is true', () => {
    render(<WorkerInput onSend={vi.fn()} disabled />)
    expect(screen.getByRole('textbox')).toBeDisabled()
  })

  it('does not call onSend for empty input', () => {
    const onSend = vi.fn()
    render(<WorkerInput onSend={onSend} />)
    const textarea = screen.getByRole('textbox')
    fireEvent.keyDown(textarea, { key: 'Enter', code: 'Enter' })
    expect(onSend).not.toHaveBeenCalled()
  })

  it('focuses textarea when focused prop becomes true', async () => {
    const { rerender } = render(<WorkerInput onSend={vi.fn()} focused={false} />)
    const textarea = screen.getByRole('textbox')
    expect(document.activeElement).not.toBe(textarea)
    rerender(<WorkerInput onSend={vi.fn()} focused={true} />)
    // requestAnimationFrame delay
    await new Promise((r) => requestAnimationFrame(r))
    expect(document.activeElement).toBe(textarea)
  })

  it('does not focus textarea when disabled even if focused=true', async () => {
    render(<WorkerInput onSend={vi.fn()} focused={true} disabled />)
    await new Promise((r) => requestAnimationFrame(r))
    expect(document.activeElement).not.toBe(screen.getByRole('textbox'))
  })

  // A F5: a send coming back (disabled → enabled) while the reader types in
  // the search bar must not pull focus into the reply box — Enter would then
  // send what is left of the search to the worker.
  it('does not take focus from another field when it is enabled again', async () => {
    const search = document.createElement('input')
    document.body.appendChild(search)
    try {
      const { rerender } = render(<WorkerInput onSend={vi.fn()} focused disabled />)
      search.focus()
      rerender(<WorkerInput onSend={vi.fn()} focused disabled={false} />)
      await new Promise((r) => requestAnimationFrame(r))
      expect(document.activeElement).toBe(search)
    } finally {
      search.remove()
    }
  })

  // PR #1495 re-review P2-2: only a field the reader types into is guarded.
  // A clicked tab (dnd-kit gives it tabIndex=0 and keeps focus on it) or a
  // button is not — switching to the tab must still land in the reply box.
  describe('takes focus from what is not a text field', () => {
    const cases: [string, () => HTMLElement][] = [
      ['a focusable tab', () => Object.assign(document.createElement('div'), { tabIndex: 0 })],
      ['a button', () => document.createElement('button')],
    ]
    for (const [name, make] of cases) {
      it(`${name}, when it becomes focused`, async () => {
        const el = make()
        document.body.appendChild(el)
        try {
          const { rerender } = render(<WorkerInput onSend={vi.fn()} focused={false} />)
          el.focus()
          expect(document.activeElement).toBe(el)
          rerender(<WorkerInput onSend={vi.fn()} focused />)
          await new Promise((r) => requestAnimationFrame(r))
          expect(document.activeElement).toBe(screen.getByRole('textbox'))
        } finally {
          el.remove()
        }
      })
    }

    it('a text field inside an inert (hidden) tab', async () => {
      const hidden = document.createElement('div')
      const field = document.createElement('textarea')
      hidden.appendChild(field)
      try {
        const { rerender } = render(<WorkerInput onSend={vi.fn()} focused={false} />)
        const box = screen.getByRole('textbox')
        document.body.appendChild(hidden)
        field.focus()
        hidden.setAttribute('inert', '')
        rerender(<WorkerInput onSend={vi.fn()} focused />)
        await new Promise((r) => requestAnimationFrame(r))
        expect(document.activeElement).toBe(box)
      } finally {
        hidden.remove()
      }
    })
  })

  it('does not take focus from a contenteditable field', async () => {
    const editor = document.createElement('div')
    editor.setAttribute('contenteditable', 'true')
    editor.tabIndex = 0
    document.body.appendChild(editor)
    try {
      const { rerender } = render(<WorkerInput onSend={vi.fn()} focused disabled />)
      editor.focus()
      rerender(<WorkerInput onSend={vi.fn()} focused disabled={false} />)
      await new Promise((r) => requestAnimationFrame(r))
      expect(document.activeElement).toBe(editor)
    } finally {
      editor.remove()
    }
  })

  it('takes focus when enabled again with nothing focused', async () => {
    const { rerender } = render(<WorkerInput onSend={vi.fn()} focused disabled />)
    ;(document.activeElement as HTMLElement | null)?.blur()
    rerender(<WorkerInput onSend={vi.fn()} focused disabled={false} />)
    await new Promise((r) => requestAnimationFrame(r))
    expect(document.activeElement).toBe(screen.getByRole('textbox'))
  })

  it('seeds the textarea value from initialValue', () => {
    render(<WorkerInput onSend={vi.fn()} initialValue="restored text" />)
    expect((screen.getByRole('textbox') as HTMLTextAreaElement).value).toBe('restored text')
  })

  it('renders no attach button', () => {
    const { container } = render(<WorkerInput onSend={vi.fn()} />)
    expect(container.querySelector('button')).toBeNull()
  })

  it('draws no border box', () => {
    const { container } = render(<WorkerInput onSend={vi.fn()} />)
    const wrapper = container.firstElementChild as HTMLElement
    const classes = wrapper.className.split(/\s+/)
    expect(classes).not.toContain('rounded-xl')
    // The only border is the hairline separator above the input.
    expect(classes).not.toContain('border')
    expect(classes).toContain('border-t')
    expect(classes).toContain('w-full')
  })

  it('defaults the placeholder to worker.input.placeholder', () => {
    render(<WorkerInput onSend={vi.fn()} />)
    expect(screen.getByRole('textbox')).toHaveAttribute('placeholder', 'Reply...')
  })

  it('caps its height', () => {
    render(<WorkerInput onSend={vi.fn()} />)
    const textarea = screen.getByRole('textbox') as HTMLTextAreaElement
    // jsdom has no layout, so scrollHeight is always 0; fake a tall content box.
    Object.defineProperty(textarea, 'scrollHeight', { configurable: true, get: () => 800 })
    fireEvent.change(textarea, { target: { value: Array.from({ length: 40 }, (_, i) => `line ${i}`).join('\n') } })
    expect(textarea.style.height).toBe('200px')
    expect(textarea.style.overflowY).toBe('auto')
  })

  it('keeps overflow hidden while under the cap', () => {
    render(<WorkerInput onSend={vi.fn()} />)
    const textarea = screen.getByRole('textbox') as HTMLTextAreaElement
    Object.defineProperty(textarea, 'scrollHeight', { configurable: true, get: () => 60 })
    fireEvent.change(textarea, { target: { value: 'a\nb' } })
    expect(textarea.style.height).toBe('60px')
    expect(textarea.style.overflowY).toBe('hidden')
  })
})
