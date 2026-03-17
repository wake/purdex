// spa/src/components/StreamInput.test.tsx
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import StreamInput from './StreamInput'

beforeEach(() => {
  cleanup()
})

describe('StreamInput', () => {
  it('renders text input', () => {
    render(<StreamInput onSend={vi.fn()} />)
    expect(screen.getByRole('textbox')).toBeInTheDocument()
  })

  it('calls onSend with text when send button clicked', () => {
    const onSend = vi.fn()
    render(<StreamInput onSend={onSend} />)
    const input = screen.getByRole('textbox')
    fireEvent.change(input, { target: { value: 'Hello!' } })
    fireEvent.click(screen.getByTestId('send-btn'))
    expect(onSend).toHaveBeenCalledWith('Hello!')
  })

  it('clears input after send', () => {
    render(<StreamInput onSend={vi.fn()} />)
    const input = screen.getByRole('textbox') as HTMLInputElement
    fireEvent.change(input, { target: { value: 'test message' } })
    fireEvent.click(screen.getByTestId('send-btn'))
    expect(input.value).toBe('')
  })

  it('calls onSend on Enter key', () => {
    const onSend = vi.fn()
    render(<StreamInput onSend={onSend} />)
    const input = screen.getByRole('textbox')
    fireEvent.change(input, { target: { value: 'Enter test' } })
    fireEvent.keyDown(input, { key: 'Enter', code: 'Enter' })
    expect(onSend).toHaveBeenCalledWith('Enter test')
  })

  it('is disabled when disabled prop is true', () => {
    render(<StreamInput onSend={vi.fn()} disabled />)
    expect(screen.getByRole('textbox')).toBeDisabled()
    expect(screen.getByTestId('send-btn')).toBeDisabled()
  })

  it('does not call onSend for empty input', () => {
    const onSend = vi.fn()
    render(<StreamInput onSend={onSend} />)
    fireEvent.click(screen.getByTestId('send-btn'))
    expect(onSend).not.toHaveBeenCalled()
  })
})
