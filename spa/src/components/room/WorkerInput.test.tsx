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

  it('renders Handoff to Term button when onHandoffToTerm is provided', () => {
    render(<WorkerInput onSend={vi.fn()} onHandoffToTerm={vi.fn()} />)
    expect(screen.getByTitle('Handoff to Term')).toBeInTheDocument()
  })

  it('does not render Handoff to Term button when onHandoffToTerm is not provided', () => {
    render(<WorkerInput onSend={vi.fn()} />)
    expect(screen.queryByTitle('Handoff to Term')).not.toBeInTheDocument()
  })

  it('calls onHandoffToTerm when button is clicked', () => {
    const onHandoffToTerm = vi.fn()
    render(<WorkerInput onSend={vi.fn()} onHandoffToTerm={onHandoffToTerm} />)
    fireEvent.click(screen.getByTitle('Handoff to Term'))
    expect(onHandoffToTerm).toHaveBeenCalledOnce()
  })

  it('disables Handoff to Term button when disabled prop is true', () => {
    render(<WorkerInput onSend={vi.fn()} onHandoffToTerm={vi.fn()} disabled />)
    expect(screen.getByTitle('Handoff to Term')).toBeDisabled()
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

  it('hides the attach button when showAttach is false', () => {
    const { container, rerender } = render(<WorkerInput onSend={() => {}} />)
    expect(container.querySelectorAll('button').length).toBeGreaterThanOrEqual(1)
    rerender(<WorkerInput onSend={() => {}} showAttach={false} />)
    expect(container.querySelector('button svg')).toBeNull()
  })

  it('seeds the textarea value from initialValue', () => {
    render(<WorkerInput onSend={vi.fn()} initialValue="restored text" />)
    expect((screen.getByRole('textbox') as HTMLTextAreaElement).value).toBe('restored text')
  })
})
