import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { useRef } from 'react'
import { FloatingPanel } from './FloatingPanel'

function Harness({ onClose, open = true }: { onClose: () => void; open?: boolean }) {
  const anchor = useRef<HTMLButtonElement>(null)
  return (
    <div>
      <button ref={anchor} data-testid="anchor">anchor</button>
      <button data-testid="elsewhere">elsewhere</button>
      {open && (
        <FloatingPanel title="Main" anchorRef={anchor} onClose={onClose}>
          <input data-testid="inside" />
        </FloatingPanel>
      )}
    </div>
  )
}

function rect(el: HTMLElement, r: Partial<DOMRect>) {
  el.getBoundingClientRect = () => ({ left: 100, top: 50, width: 40, height: 20, right: 140, bottom: 70, x: 100, y: 50, toJSON() {} , ...r }) as DOMRect
}

beforeEach(() => {
  Object.defineProperty(window, 'innerWidth', { value: 1000, configurable: true })
  Object.defineProperty(window, 'innerHeight', { value: 800, configurable: true })
})

describe('FloatingPanel', () => {
  it('renders into document.body as a dialog titled with `title`, positioned under the anchor', () => {
    render(<Harness onClose={() => {}} />)
    const panel = screen.getByRole('dialog', { name: 'Main' })
    expect(panel.parentElement).toBe(document.body)
    expect(panel.style.position).toBe('fixed')
  })

  it('positions below the anchor rect, clamped to the viewport', () => {
    const { unmount } = render(<Harness onClose={() => {}} />)
    unmount()
    // re-render with a stubbed anchor rect: stub before the panel mounts by rendering closed first
    const { rerender } = render(<Harness onClose={() => {}} open={false} />)
    rect(screen.getByTestId('anchor'), { left: 990, top: 790, bottom: 800, right: 1000 })
    rerender(<Harness onClose={() => {}} open />)
    const panel = screen.getByTestId('floating-panel')
    expect(parseInt(panel.style.left)).toBeLessThanOrEqual(1000 - 320 - 4)
    expect(parseInt(panel.style.top)).toBeLessThanOrEqual(800 - 4)
  })

  it('closes on mousedown outside, not on mousedown inside or on the anchor', () => {
    const onClose = vi.fn()
    render(<Harness onClose={onClose} />)
    fireEvent.mouseDown(screen.getByTestId('inside'))
    fireEvent.mouseDown(screen.getByTestId('anchor'))
    expect(onClose).not.toHaveBeenCalled()
    fireEvent.mouseDown(screen.getByTestId('elsewhere'))
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('closes on Escape and on the close button', () => {
    const onClose = vi.fn()
    render(<Harness onClose={onClose} />)
    fireEvent.keyDown(document, { key: 'Escape' })
    fireEvent.click(screen.getByTestId('floating-panel-close'))
    expect(onClose).toHaveBeenCalledTimes(2)
  })

  it('dragging the handle moves the panel by the pointer delta and keeps the position', () => {
    render(<Harness onClose={() => {}} />)
    const panel = screen.getByTestId('floating-panel')
    const handle = screen.getByTestId('floating-panel-handle')
    handle.setPointerCapture = () => {}
    handle.releasePointerCapture = () => {}
    const left0 = parseInt(panel.style.left), top0 = parseInt(panel.style.top)
    fireEvent.pointerDown(handle, { clientX: 10, clientY: 10, pointerId: 1, button: 0 })
    fireEvent.pointerMove(handle, { clientX: 60, clientY: 40, pointerId: 1 })
    expect(parseInt(panel.style.left)).toBe(left0 + 50)
    expect(parseInt(panel.style.top)).toBe(top0 + 30)
    fireEvent.pointerUp(handle, { pointerId: 1 })
    fireEvent.pointerMove(handle, { clientX: 500, clientY: 500, pointerId: 1 })
    expect(parseInt(panel.style.left)).toBe(left0 + 50)
  })

  it('a drag never moves the panel fully off-screen', () => {
    render(<Harness onClose={() => {}} />)
    const panel = screen.getByTestId('floating-panel')
    const handle = screen.getByTestId('floating-panel-handle')
    handle.setPointerCapture = () => {}
    handle.releasePointerCapture = () => {}
    fireEvent.pointerDown(handle, { clientX: 0, clientY: 0, pointerId: 1, button: 0 })
    fireEvent.pointerMove(handle, { clientX: -5000, clientY: -5000, pointerId: 1 })
    expect(parseInt(panel.style.left)).toBeGreaterThanOrEqual(-320 + 40)
    expect(parseInt(panel.style.top)).toBeGreaterThanOrEqual(0)
  })

  it('pointer events inside the body do not start a drag', () => {
    render(<Harness onClose={() => {}} />)
    const panel = screen.getByTestId('floating-panel')
    const left0 = panel.style.left
    fireEvent.pointerDown(screen.getByTestId('inside'), { clientX: 10, clientY: 10, pointerId: 1, button: 0 })
    fireEvent.pointerMove(screen.getByTestId('inside'), { clientX: 60, clientY: 40, pointerId: 1 })
    expect(panel.style.left).toBe(left0)
  })
})
