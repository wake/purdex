import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { keepFocus } from './keep-focus'

// jsdom does not run the browser's "focus the button on mousedown" default, so these tests can only show that the
// default is prevented. That preventing it keeps focus where it was is shown in a real browser (shell polish spec §5).
describe('keepFocus', () => {
  it('prevents the mousedown default', () => {
    render(<button onMouseDown={keepFocus}>chrome</button>)
    expect(fireEvent.mouseDown(screen.getByRole('button'))).toBe(false)
  })

  it('leaves the click alone', () => {
    const onClick = vi.fn()
    render(<button onMouseDown={keepFocus} onClick={onClick}>chrome</button>)
    fireEvent.mouseDown(screen.getByRole('button'))
    fireEvent.click(screen.getByRole('button'))
    expect(onClick).toHaveBeenCalledTimes(1)
  })

  it('does not stop propagation, so document mousedown listeners (outside-click closers) still see the press', () => {
    const onDocMouseDown = vi.fn()
    document.addEventListener('mousedown', onDocMouseDown)
    try {
      render(<button onMouseDown={keepFocus}>chrome</button>)
      fireEvent.mouseDown(screen.getByRole('button'))
      expect(onDocMouseDown).toHaveBeenCalledTimes(1)
    } finally {
      document.removeEventListener('mousedown', onDocMouseDown)
    }
  })
})
