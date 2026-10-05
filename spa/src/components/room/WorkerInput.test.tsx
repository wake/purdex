// spa/src/components/room/WorkerInput.test.tsx
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import WorkerInput from './WorkerInput'
import type { Chip } from '../../lib/nex/worker-upload'

beforeEach(() => {
  cleanup()
})

/** Waits past the rAF the focus sites schedule (jsdom runs frames in order). */
const nextFrame = () => new Promise((r) => requestAnimationFrame(r))

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

  // Shell cleanup spec §8.2: the reply box focuses itself only at ACTIVATION —
  // its tab becoming active, or the pane mounting in the active tab — and only
  // when it is the tab's focus target. A change of `isFocusTarget` while the
  // tab stays active never focuses: that is a click, and the click already put
  // focus where the reader wanted it.
  describe('activation focus', () => {
    it('mounting active as the focus target focuses the textarea', async () => {
      render(<WorkerInput onSend={vi.fn()} isActive isFocusTarget />)
      await nextFrame()
      expect(screen.getByRole('textbox')).toHaveFocus()
    })

    it('mounting active but not the focus target does not', async () => {
      render(<WorkerInput onSend={vi.fn()} isActive isFocusTarget={false} />)
      await nextFrame()
      expect(screen.getByRole('textbox')).not.toHaveFocus()
    })

    it('mounting inactive does not, even as the focus target', async () => {
      render(<WorkerInput onSend={vi.fn()} isActive={false} isFocusTarget />)
      await nextFrame()
      expect(screen.getByRole('textbox')).not.toHaveFocus()
    })

    it('becoming active as the focus target focuses the textarea', async () => {
      const { rerender } = render(<WorkerInput onSend={vi.fn()} isActive={false} isFocusTarget />)
      const textarea = screen.getByRole('textbox')
      expect(document.activeElement).not.toBe(textarea)
      rerender(<WorkerInput onSend={vi.fn()} isActive isFocusTarget />)
      await nextFrame()
      expect(document.activeElement).toBe(textarea)
    })

    it('becoming active but not the focus target does not', async () => {
      const { rerender } = render(<WorkerInput onSend={vi.fn()} isActive={false} isFocusTarget={false} />)
      rerender(<WorkerInput onSend={vi.fn()} isActive isFocusTarget={false} />)
      await nextFrame()
      expect(screen.getByRole('textbox')).not.toHaveFocus()
    })

    it('becoming the focus target while the tab stays active does not', async () => {
      const { rerender } = render(<WorkerInput onSend={vi.fn()} isActive isFocusTarget={false} />)
      await nextFrame()
      rerender(<WorkerInput onSend={vi.fn()} isActive isFocusTarget />)
      await nextFrame()
      expect(screen.getByRole('textbox')).not.toHaveFocus()
    })

    it('does not focus a disabled textarea at activation', async () => {
      render(<WorkerInput onSend={vi.fn()} isActive isFocusTarget disabled />)
      await nextFrame()
      expect(document.activeElement).not.toBe(screen.getByRole('textbox'))
    })

    it('does not take focus from a text field at activation', async () => {
      const search = document.createElement('input')
      document.body.appendChild(search)
      try {
        const { rerender } = render(<WorkerInput onSend={vi.fn()} isActive={false} isFocusTarget />)
        search.focus()
        rerender(<WorkerInput onSend={vi.fn()} isActive isFocusTarget />)
        await nextFrame()
        expect(document.activeElement).toBe(search)
      } finally {
        search.remove()
      }
    })
  })

  // Spec §8.2 (P5 review follow-up): an activation that finds the box disabled
  // (history still loading, the stream not back yet) cannot land, so it stays
  // pending: the box takes focus the first time it is enabled — while the tab
  // is still active, the pane still the target, and the reader not typing
  // elsewhere. Deactivation or losing the target cancels it; so does that
  // first enabling, whether or not it focused.
  describe('an activation that finds the box disabled', () => {
    it('focuses the box once, when it becomes usable', async () => {
      const focus = vi.spyOn(HTMLTextAreaElement.prototype, 'focus')
      try {
        const { rerender } = render(<WorkerInput onSend={vi.fn()} isActive isFocusTarget disabled />)
        await nextFrame()
        expect(focus).not.toHaveBeenCalled()
        rerender(<WorkerInput onSend={vi.fn()} isActive isFocusTarget disabled={false} />)
        await nextFrame()
        expect(focus).toHaveBeenCalledTimes(1)
        expect(document.activeElement).toBe(screen.getByRole('textbox'))
      } finally {
        focus.mockRestore()
      }
    })

    it('the tab shown again while the box is disabled: focuses it when it becomes usable', async () => {
      const { rerender } = render(<WorkerInput onSend={vi.fn()} isActive={false} isFocusTarget disabled />)
      rerender(<WorkerInput onSend={vi.fn()} isActive isFocusTarget disabled />)
      await nextFrame()
      expect(screen.getByRole('textbox')).not.toHaveFocus()
      rerender(<WorkerInput onSend={vi.fn()} isActive isFocusTarget disabled={false} />)
      await nextFrame()
      expect(screen.getByRole('textbox')).toHaveFocus()
    })

    it('enabled before the activation frame runs: one focus, from the activation', async () => {
      const focus = vi.spyOn(HTMLTextAreaElement.prototype, 'focus')
      try {
        const { rerender } = render(<WorkerInput onSend={vi.fn()} isActive isFocusTarget disabled />)
        rerender(<WorkerInput onSend={vi.fn()} isActive isFocusTarget disabled={false} />)
        await nextFrame()
        await nextFrame()
        expect(focus).toHaveBeenCalledTimes(1)
      } finally {
        focus.mockRestore()
      }
    })

    it('not when the reader moved to another pane before it became usable', async () => {
      const { rerender } = render(<WorkerInput onSend={vi.fn()} isActive isFocusTarget disabled />)
      await nextFrame()
      rerender(<WorkerInput onSend={vi.fn()} isActive isFocusTarget={false} disabled />)
      rerender(<WorkerInput onSend={vi.fn()} isActive isFocusTarget={false} disabled={false} />)
      await nextFrame()
      expect(screen.getByRole('textbox')).not.toHaveFocus()
    })

    // Same race as A1: a click on another pane between the enabling and the frame.
    it('not when the reader moves to another pane after it became usable, before the frame runs', async () => {
      const { rerender } = render(<WorkerInput onSend={vi.fn()} isActive isFocusTarget disabled />)
      await nextFrame()
      rerender(<WorkerInput onSend={vi.fn()} isActive isFocusTarget disabled={false} />)
      rerender(<WorkerInput onSend={vi.fn()} isActive isFocusTarget={false} disabled={false} />)
      await nextFrame()
      expect(screen.getByRole('textbox')).not.toHaveFocus()
    })

    // Losing the target cancels it for good: the reader clicking back into
    // this pane put focus where they wanted it themselves.
    it('not after the reader moved away and back while it was still disabled', async () => {
      const { rerender } = render(<WorkerInput onSend={vi.fn()} isActive isFocusTarget disabled />)
      await nextFrame()
      rerender(<WorkerInput onSend={vi.fn()} isActive isFocusTarget={false} disabled />)
      rerender(<WorkerInput onSend={vi.fn()} isActive isFocusTarget disabled />)
      rerender(<WorkerInput onSend={vi.fn()} isActive isFocusTarget disabled={false} />)
      await nextFrame()
      expect(screen.getByRole('textbox')).not.toHaveFocus()
    })

    it('not when the tab was left before it became usable', async () => {
      const { rerender } = render(<WorkerInput onSend={vi.fn()} isActive isFocusTarget disabled />)
      await nextFrame()
      rerender(<WorkerInput onSend={vi.fn()} isActive={false} isFocusTarget disabled />)
      rerender(<WorkerInput onSend={vi.fn()} isActive={false} isFocusTarget disabled={false} />)
      await nextFrame()
      expect(screen.getByRole('textbox')).not.toHaveFocus()
    })

    it('not when the reader is typing in another field as it becomes usable, nor at a later enabling', async () => {
      const search = document.createElement('input')
      document.body.appendChild(search)
      try {
        const { rerender, container } = render(<WorkerInput onSend={vi.fn()} isActive isFocusTarget disabled />)
        const box = container.querySelector('textarea')!
        await nextFrame()
        search.focus()
        rerender(<WorkerInput onSend={vi.fn()} isActive isFocusTarget disabled={false} />)
        await nextFrame()
        expect(document.activeElement).toBe(search)
        // That first enabling used it up: the stream lost and back later is not the activation.
        search.blur()
        rerender(<WorkerInput onSend={vi.fn()} isActive isFocusTarget disabled />)
        await nextFrame()
        rerender(<WorkerInput onSend={vi.fn()} isActive isFocusTarget disabled={false} />)
        await nextFrame()
        expect(box).not.toHaveFocus()
      } finally {
        search.remove()
      }
    })

    // The first enabling uses it up even when its frame never runs: the box
    // disabled again before then (the stream lost, a send, a take-back)
    // cancels that focus, and a later, unrelated enabling is not the activation.
    it('used up by the first enabling even when the box is disabled again before its frame runs', async () => {
      const focus = vi.spyOn(HTMLTextAreaElement.prototype, 'focus')
      try {
        const { rerender } = render(<WorkerInput onSend={vi.fn()} isActive isFocusTarget disabled />)
        await nextFrame()
        expect(focus).not.toHaveBeenCalled()
        rerender(<WorkerInput onSend={vi.fn()} isActive isFocusTarget disabled={false} />)
        rerender(<WorkerInput onSend={vi.fn()} isActive isFocusTarget disabled />)
        await nextFrame()
        expect(focus).not.toHaveBeenCalled()
        rerender(<WorkerInput onSend={vi.fn()} isActive isFocusTarget disabled={false} />)
        await nextFrame()
        await nextFrame()
        expect(focus).not.toHaveBeenCalled()
        expect(screen.getByRole('textbox')).not.toHaveFocus()
      } finally {
        focus.mockRestore()
      }
    })

    it('once fulfilled, a later enabling (the stream back) does not focus again', async () => {
      const focus = vi.spyOn(HTMLTextAreaElement.prototype, 'focus')
      try {
        const { rerender } = render(<WorkerInput onSend={vi.fn()} isActive isFocusTarget disabled />)
        await nextFrame()
        rerender(<WorkerInput onSend={vi.fn()} isActive isFocusTarget disabled={false} />)
        await nextFrame()
        expect(focus).toHaveBeenCalledTimes(1)
        screen.getByRole('textbox').blur()
        rerender(<WorkerInput onSend={vi.fn()} isActive isFocusTarget disabled />)
        await nextFrame()
        rerender(<WorkerInput onSend={vi.fn()} isActive isFocusTarget disabled={false} />)
        await nextFrame()
        expect(focus).toHaveBeenCalledTimes(1)
      } finally {
        focus.mockRestore()
      }
    })
  })

  // Spec §8.2 (P5 review A2): the refocus after a send is driven by the
  // pane's own send — `pendingSend` true → false — not by the aggregated
  // `disabled`, which also covers stream loss, history load, encoding and
  // take-back. It only lands for the focus target of the active tab — the
  // reader sent from this pane — and only on a box that is enabled when the
  // frame runs. The tests that expect a focus mount the box enabled and send
  // after the activation landed: an activation on a disabled box would leave
  // a pending focus of its own.
  describe('after a send comes back', () => {
    it('takes focus as the focus target of the active tab', async () => {
      const { rerender } = render(<WorkerInput onSend={vi.fn()} isActive isFocusTarget />)
      // Let the mount's activation land, so what follows tests the send coming back alone.
      await nextFrame()
      ;(document.activeElement as HTMLElement | null)?.blur()
      rerender(<WorkerInput onSend={vi.fn()} isActive isFocusTarget pendingSend disabled />)
      rerender(<WorkerInput onSend={vi.fn()} isActive isFocusTarget pendingSend={false} disabled={false} />)
      await nextFrame()
      expect(document.activeElement).toBe(screen.getByRole('textbox'))
    })

    it('does not take focus when it is not the focus target', async () => {
      const { rerender } = render(<WorkerInput onSend={vi.fn()} isActive isFocusTarget={false} pendingSend disabled />)
      await nextFrame()
      ;(document.activeElement as HTMLElement | null)?.blur()
      rerender(<WorkerInput onSend={vi.fn()} isActive isFocusTarget={false} pendingSend={false} disabled={false} />)
      await nextFrame()
      expect(screen.getByRole('textbox')).not.toHaveFocus()
    })

    it('does not take focus once the reader moved to another pane mid-send', async () => {
      const { rerender } = render(<WorkerInput onSend={vi.fn()} isActive isFocusTarget pendingSend disabled />)
      await nextFrame()
      ;(document.activeElement as HTMLElement | null)?.blur()
      rerender(<WorkerInput onSend={vi.fn()} isActive isFocusTarget={false} pendingSend disabled />)
      rerender(<WorkerInput onSend={vi.fn()} isActive isFocusTarget={false} pendingSend={false} disabled={false} />)
      await nextFrame()
      expect(screen.getByRole('textbox')).not.toHaveFocus()
    })

    // Same race as the activation focus (P5 review A1): a click on another
    // pane between the send coming back and the frame moves the target.
    it('does not take focus when the reader moves to another pane before the frame runs', async () => {
      const { rerender } = render(<WorkerInput onSend={vi.fn()} isActive isFocusTarget pendingSend disabled />)
      await nextFrame()
      ;(document.activeElement as HTMLElement | null)?.blur()
      rerender(<WorkerInput onSend={vi.fn()} isActive isFocusTarget pendingSend={false} disabled={false} />)
      rerender(<WorkerInput onSend={vi.fn()} isActive isFocusTarget={false} pendingSend={false} disabled={false} />)
      await nextFrame()
      expect(screen.getByRole('textbox')).not.toHaveFocus()
    })

    it('mounting enabled is not a send coming back: one focus, from the activation', async () => {
      const focus = vi.spyOn(HTMLTextAreaElement.prototype, 'focus')
      try {
        render(<WorkerInput onSend={vi.fn()} isActive isFocusTarget />)
        await nextFrame()
        await nextFrame()
        expect(focus).toHaveBeenCalledTimes(1)
      } finally {
        focus.mockRestore()
      }
    })

    it('does not take focus while its tab is not active', async () => {
      const { rerender } = render(<WorkerInput onSend={vi.fn()} isActive={false} isFocusTarget pendingSend disabled />)
      rerender(<WorkerInput onSend={vi.fn()} isActive={false} isFocusTarget pendingSend={false} disabled={false} />)
      await nextFrame()
      expect(screen.getByRole('textbox')).not.toHaveFocus()
    })

    // The send ended but the box stays disabled for another reason (the
    // worker ended, the stream died with it): no focus — and the box enabling
    // later is not this send coming back either.
    it('a send that comes back while the box stays disabled: no focus, then or when it is enabled later', async () => {
      const focus = vi.spyOn(HTMLTextAreaElement.prototype, 'focus')
      try {
        const { rerender } = render(<WorkerInput onSend={vi.fn()} isActive isFocusTarget />)
        await nextFrame()
        rerender(<WorkerInput onSend={vi.fn()} isActive isFocusTarget pendingSend disabled />)
        focus.mockClear()
        rerender(<WorkerInput onSend={vi.fn()} isActive isFocusTarget pendingSend={false} disabled />)
        await nextFrame()
        expect(focus).not.toHaveBeenCalled()
        rerender(<WorkerInput onSend={vi.fn()} isActive isFocusTarget pendingSend={false} disabled={false} />)
        await nextFrame()
        expect(focus).not.toHaveBeenCalled()
      } finally {
        focus.mockRestore()
      }
    })

    // The box is checked when the frame runs, not at the render the send
    // came back in: an image send that fails clears `pendingSend` a few
    // microtasks before its encoding flag, and still lands back in the box.
    it('a send that comes back while disabled, enabled before the frame runs, takes focus', async () => {
      const { rerender } = render(<WorkerInput onSend={vi.fn()} isActive isFocusTarget />)
      await nextFrame()
      ;(document.activeElement as HTMLElement | null)?.blur()
      rerender(<WorkerInput onSend={vi.fn()} isActive isFocusTarget pendingSend disabled />)
      rerender(<WorkerInput onSend={vi.fn()} isActive isFocusTarget pendingSend={false} disabled />)
      rerender(<WorkerInput onSend={vi.fn()} isActive isFocusTarget pendingSend={false} disabled={false} />)
      await nextFrame()
      expect(document.activeElement).toBe(screen.getByRole('textbox'))
    })
  })

  // P5 review A2: the input is disabled for more than this pane's send — the
  // live stream lost and found again, image encoding, a take-back in flight.
  // None of them ending, after the activation landed, is a send coming back.
  // (An activation that finds the box disabled is the one exception, above.)
  it('the input enabling with no send of its own (stream back, encoding or take-back done) does not take focus', async () => {
    const focus = vi.spyOn(HTMLTextAreaElement.prototype, 'focus')
    try {
      const { rerender } = render(<WorkerInput onSend={vi.fn()} isActive isFocusTarget />)
      await nextFrame()
      focus.mockClear()
      for (let i = 0; i < 2; i++) {
        rerender(<WorkerInput onSend={vi.fn()} isActive isFocusTarget disabled />)
        await nextFrame()
        rerender(<WorkerInput onSend={vi.fn()} isActive isFocusTarget disabled={false} />)
        await nextFrame()
      }
      expect(focus).not.toHaveBeenCalled()
    } finally {
      focus.mockRestore()
    }
  })

  // A F5: a send coming back while the reader types in the search bar must
  // not pull focus into the reply box — Enter would then send what is left
  // of the search to the worker.
  it('does not take focus from another field when a send comes back', async () => {
    const search = document.createElement('input')
    document.body.appendChild(search)
    try {
      const { rerender } = render(<WorkerInput onSend={vi.fn()} isActive isFocusTarget pendingSend disabled />)
      await nextFrame()
      search.focus()
      rerender(<WorkerInput onSend={vi.fn()} isActive isFocusTarget pendingSend={false} disabled={false} />)
      await nextFrame()
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
      it(`${name}, when it becomes active`, async () => {
        const el = make()
        document.body.appendChild(el)
        try {
          const { rerender } = render(<WorkerInput onSend={vi.fn()} isActive={false} isFocusTarget />)
          el.focus()
          expect(document.activeElement).toBe(el)
          rerender(<WorkerInput onSend={vi.fn()} isActive isFocusTarget />)
          await nextFrame()
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
        const { rerender } = render(<WorkerInput onSend={vi.fn()} isActive={false} isFocusTarget />)
        const box = screen.getByRole('textbox')
        document.body.appendChild(hidden)
        field.focus()
        hidden.setAttribute('inert', '')
        rerender(<WorkerInput onSend={vi.fn()} isActive isFocusTarget />)
        await nextFrame()
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
      const { rerender } = render(<WorkerInput onSend={vi.fn()} isActive isFocusTarget pendingSend disabled />)
      await nextFrame()
      item.focus()
      rerender(<WorkerInput onSend={vi.fn()} isActive isFocusTarget pendingSend={false} disabled={false} />)
      await nextFrame()
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
      const { rerender } = render(<WorkerInput onSend={vi.fn()} isActive isFocusTarget pendingSend disabled />)
      await nextFrame()
      editor.focus()
      rerender(<WorkerInput onSend={vi.fn()} isActive isFocusTarget pendingSend={false} disabled={false} />)
      await nextFrame()
      expect(document.activeElement).toBe(editor)
    } finally {
      editor.remove()
    }
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
  const uploading: Chip = { key: 'u', kind: 'path', name: 'u.txt', status: 'uploading' }
  const failed: Chip = { key: 'f', kind: 'path', name: 'f.txt', status: 'failed', error: 'network' }
  const done: Chip = { key: 'd', kind: 'path', name: 'd.txt', status: 'done', path: '/w/d.txt' }
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
