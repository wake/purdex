// spa/src/components/room/WorkerInput.test.tsx
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import WorkerInput from './WorkerInput'
import type { Chip } from '../../lib/nex/worker-upload'

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

  // #1495 re-review (0.45): a panel the reader has open — the header's
  // overflow menu, the cost panel (FloatingPanel, role="dialog") — keeps
  // its focus when the pane comes back to life underneath it.
  it('does not take focus from inside an open dialog panel', async () => {
    const panel = document.createElement('div')
    panel.setAttribute('role', 'dialog')
    const item = document.createElement('button')
    panel.appendChild(item)
    document.body.appendChild(panel)
    try {
      const { rerender } = render(<WorkerInput onSend={vi.fn()} focused disabled />)
      item.focus()
      rerender(<WorkerInput onSend={vi.fn()} focused disabled={false} />)
      await new Promise((r) => requestAnimationFrame(r))
      expect(document.activeElement).toBe(item)
    } finally {
      panel.remove()
    }
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

describe('WorkerInput — attachments (spec §9.1)', () => {
  const uploading: Chip = { key: 'u', name: 'u.txt', status: 'uploading' }
  const failed: Chip = { key: 'f', name: 'f.txt', status: 'failed', error: 'network' }
  const done: Chip = { key: 'd', name: 'd.txt', status: 'done', path: '/w/d.txt' }
  const enter = (ta: HTMLElement) => fireEvent.keyDown(ta, { key: 'Enter', code: 'Enter' })

  it('renders the chips above the textarea and removes one through onRemoveChip', () => {
    const onRemoveChip = vi.fn()
    render(<WorkerInput onSend={vi.fn()} chips={[done]} onRemoveChip={onRemoveChip} />)
    expect(screen.getAllByTestId('upload-chip')).toHaveLength(1)
    fireEvent.click(screen.getByRole('button', { name: 'Remove d.txt' }))
    expect(onRemoveChip).toHaveBeenCalledWith('d')
  })

  it('blocks send while a chip is uploading and says why, keeping the text', () => {
    const onSend = vi.fn()
    render(<WorkerInput onSend={onSend} chips={[uploading]} />)
    const ta = screen.getByRole('textbox') as HTMLTextAreaElement
    fireEvent.change(ta, { target: { value: 'hi' } })
    enter(ta)
    expect(onSend).not.toHaveBeenCalled()
    expect(ta.value).toBe('hi')
    expect(screen.getByTestId('upload-block').textContent).toBe('Waiting for uploads to finish…')
    expect(screen.getByTestId('upload-block')).toHaveAttribute('role', 'status')
    expect(screen.getByTestId('upload-block')).toHaveAttribute('aria-live', 'polite')
  })

  it('a failed chip blocks send until it is removed', () => {
    const onSend = vi.fn()
    const { rerender } = render(<WorkerInput onSend={onSend} chips={[failed]} />)
    const ta = screen.getByRole('textbox') as HTMLTextAreaElement
    fireEvent.change(ta, { target: { value: 'hi' } })
    enter(ta)
    expect(onSend).not.toHaveBeenCalled()
    expect(screen.getByTestId('upload-block').textContent).toBe('An upload failed — remove it to send')
    rerender(<WorkerInput onSend={onSend} chips={[]} />)
    expect(screen.queryByTestId('upload-block')).toBeNull()
    enter(ta)
    expect(onSend).toHaveBeenCalledWith('hi')
  })

  it('sends with no text when a done chip is attached', () => {
    const onSend = vi.fn()
    render(<WorkerInput onSend={onSend} chips={[done]} />)
    enter(screen.getByRole('textbox'))
    expect(onSend).toHaveBeenCalledWith('')
  })

  it('pasting files hands them to onAddFiles; pasting text does not', () => {
    const onAddFiles = vi.fn()
    render(<WorkerInput onSend={vi.fn()} onAddFiles={onAddFiles} />)
    const ta = screen.getByRole('textbox')
    const file = new File(['x'], 'shot.png', { type: 'image/png' })
    fireEvent.paste(ta, { clipboardData: { files: [file], getData: () => '' } })
    expect(onAddFiles).toHaveBeenCalledWith([file])
    onAddFiles.mockClear()
    fireEvent.paste(ta, { clipboardData: { files: [], getData: () => 'text' } })
    expect(onAddFiles).not.toHaveBeenCalled()
  })

  // A rich-text app (e.g. a chat client) puts an image rendition next to the
  // text on copy. With non-empty text present the ordinary text paste wins
  // (never cancelled) and only the image files are skipped — any other file
  // on the clipboard is still attached (PR #1522 A2).
  describe('a paste with files and non-empty text', () => {
    const png = new File(['x'], 'shot.png', { type: 'image/png' })
    const pdf = new File(['x'], 'doc.pdf', { type: 'application/pdf' })
    const paste = (files: File[]) => {
      const onAddFiles = vi.fn()
      render(<WorkerInput onSend={vi.fn()} onAddFiles={onAddFiles} />)
      const notCancelled = fireEvent.paste(screen.getByRole('textbox'), { clipboardData: { files, getData: (t: string) => (t === 'text/plain' ? 'hello' : '') } })
      return { onAddFiles, notCancelled }
    }

    it('text + image: the text pastes, no chip', () => {
      const { onAddFiles, notCancelled } = paste([png])
      expect(notCancelled).toBe(true)
      expect(onAddFiles).not.toHaveBeenCalled()
    })

    it('text + pdf: the text pastes and the pdf is attached', () => {
      const { onAddFiles, notCancelled } = paste([pdf])
      expect(notCancelled).toBe(true)
      expect(onAddFiles).toHaveBeenCalledWith([pdf])
    })

    it('text + image + pdf: only the pdf is attached', () => {
      const { onAddFiles, notCancelled } = paste([png, pdf])
      expect(notCancelled).toBe(true)
      expect(onAddFiles).toHaveBeenCalledTimes(1)
      expect(onAddFiles).toHaveBeenCalledWith([pdf])
    })
  })

  it('a files-only paste attaches every file, images included, and takes over the paste', () => {
    const onAddFiles = vi.fn()
    render(<WorkerInput onSend={vi.fn()} onAddFiles={onAddFiles} />)
    const png = new File(['x'], 'shot.png', { type: 'image/png' })
    const pdf = new File(['x'], 'doc.pdf', { type: 'application/pdf' })
    const notCancelled = fireEvent.paste(screen.getByRole('textbox'), { clipboardData: { files: [png, pdf], getData: () => '' } })
    expect(notCancelled).toBe(false)
    expect(onAddFiles).toHaveBeenCalledWith([png, pdf])
  })

  it('the + button opens a file picker whose files go to onAddFiles', () => {
    const onAddFiles = vi.fn()
    render(<WorkerInput onSend={vi.fn()} onAddFiles={onAddFiles} />)
    const picker = screen.getByTestId('attach-input') as HTMLInputElement
    const click = vi.spyOn(picker, 'click')
    fireEvent.click(screen.getByRole('button', { name: 'Attach files' }))
    expect(click).toHaveBeenCalled()
    const file = new File(['x'], 'a.txt', { type: 'text/plain' })
    fireEvent.change(picker, { target: { files: [file] } })
    expect(onAddFiles).toHaveBeenCalledWith([file])
  })

  // PR #1522 A3: the OS picker stays open while the input turns disabled
  // (a send went out, the worker ended); what it returns then is dropped.
  it('a picker that returns after the input became disabled adds nothing', () => {
    const onAddFiles = vi.fn()
    const { rerender } = render(<WorkerInput onSend={vi.fn()} onAddFiles={onAddFiles} />)
    fireEvent.click(screen.getByRole('button', { name: 'Attach files' }))
    rerender(<WorkerInput onSend={vi.fn()} onAddFiles={onAddFiles} disabled />)
    const picker = screen.getByTestId('attach-input') as HTMLInputElement
    expect(picker).toBeDisabled()
    fireEvent.change(picker, { target: { files: [new File(['x'], 'a.txt', { type: 'text/plain' })] } })
    expect(onAddFiles).not.toHaveBeenCalled()
  })

  it('has no + button without onAddFiles', () => {
    render(<WorkerInput onSend={vi.fn()} />)
    expect(screen.queryByRole('button', { name: 'Attach files' })).toBeNull()
  })
})
