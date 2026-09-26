import { describe, it, expect, vi, beforeEach } from 'vitest'
import { createRef } from 'react'
import { render, screen, fireEvent } from '@testing-library/react'
import ViewModeMenu from './ViewModeMenu'

const anchorRef = createRef<HTMLButtonElement>()

describe('ViewModeMenu', () => {
  beforeEach(() => { vi.clearAllMocks() })

  it('marks the current view', () => {
    const { rerender } = render(<ViewModeMenu mode="room" onModeChange={vi.fn()} anchorRef={anchorRef} onClose={vi.fn()} />)
    const room = screen.getByTestId('view-mode-room')
    const chat = screen.getByTestId('view-mode-chat')
    expect(room.getAttribute('role')).toBe('menuitemradio')
    expect(chat.getAttribute('role')).toBe('menuitemradio')
    expect(room.getAttribute('aria-checked')).toBe('true')
    expect(chat.getAttribute('aria-checked')).toBe('false')
    expect(room).toHaveTextContent('Room')
    expect(chat).toHaveTextContent('Chat')
    rerender(<ViewModeMenu mode="chat" onModeChange={vi.fn()} anchorRef={anchorRef} onClose={vi.fn()} />)
    expect(screen.getByTestId('view-mode-room').getAttribute('aria-checked')).toBe('false')
    expect(screen.getByTestId('view-mode-chat').getAttribute('aria-checked')).toBe('true')
  })

  it("choosing chat calls onModeChange('chat')", () => {
    const onModeChange = vi.fn()
    const onClose = vi.fn()
    render(<ViewModeMenu mode="room" onModeChange={onModeChange} anchorRef={anchorRef} onClose={onClose} />)
    fireEvent.click(screen.getByTestId('view-mode-chat'))
    expect(onModeChange).toHaveBeenCalledTimes(1)
    expect(onModeChange).toHaveBeenCalledWith('chat')
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('the terminal item calls onTakeBack and is not a radio', () => {
    const onTakeBack = vi.fn()
    const onClose = vi.fn()
    const { rerender } = render(
      <ViewModeMenu mode="room" onModeChange={vi.fn()} onTakeBack={onTakeBack} anchorRef={anchorRef} onClose={onClose} />,
    )
    const term = screen.getByTestId('view-mode-terminal')
    expect(term.getAttribute('role')).toBe('menuitem')
    expect(term.hasAttribute('aria-checked')).toBe(false)
    expect(term).toHaveTextContent(/take to terminal/i)
    fireEvent.click(term)
    expect(onTakeBack).toHaveBeenCalledTimes(1)
    expect(onClose).toHaveBeenCalledTimes(1)
    // Busy while a take-back is in flight.
    rerender(<ViewModeMenu mode="room" onModeChange={vi.fn()} onTakeBack={onTakeBack} takeBackBusy anchorRef={anchorRef} onClose={onClose} />)
    expect((screen.getByTestId('view-mode-terminal') as HTMLButtonElement).disabled).toBe(true)
  })

  it('has no terminal item without onTakeBack', () => {
    render(<ViewModeMenu mode="room" onModeChange={vi.fn()} anchorRef={anchorRef} onClose={vi.fn()} />)
    expect(screen.queryByTestId('view-mode-terminal')).toBeNull()
  })

  // Until ExecutionView threads the mode through (T1.4), the header may render
  // without `onModeChange`; the radios then show the view but cannot change it.
  it('disables the view radios without onModeChange', () => {
    render(<ViewModeMenu mode="room" onTakeBack={vi.fn()} anchorRef={anchorRef} onClose={vi.fn()} />)
    expect((screen.getByTestId('view-mode-room') as HTMLButtonElement).disabled).toBe(true)
    expect((screen.getByTestId('view-mode-chat') as HTMLButtonElement).disabled).toBe(true)
    expect((screen.getByTestId('view-mode-terminal') as HTMLButtonElement).disabled).toBe(false)
  })
})
