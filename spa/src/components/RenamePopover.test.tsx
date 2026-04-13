import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import { RenamePopover } from './RenamePopover'

describe('RenamePopover', () => {
  const defaultProps = {
    anchorRect: { left: 100, top: 30, width: 120, height: 26, bottom: 56, right: 220 } as DOMRect,
    currentName: 'my-session',
    onConfirm: vi.fn().mockResolvedValue(undefined),
    onCancel: vi.fn(),
  }

  beforeEach(() => { cleanup(); vi.clearAllMocks() })
  afterEach(() => cleanup())

  it('renders input with current name', () => {
    render(<RenamePopover {...defaultProps} />)
    const input = screen.getByDisplayValue('my-session')
    expect(input).toBeInTheDocument()
  })

  it('selects all text on mount', () => {
    render(<RenamePopover {...defaultProps} />)
    const input = screen.getByDisplayValue('my-session') as HTMLInputElement
    expect(input.selectionStart).toBe(0)
    expect(input.selectionEnd).toBe('my-session'.length)
  })

  it('calls onConfirm with new name on Enter', async () => {
    render(<RenamePopover {...defaultProps} />)
    const input = screen.getByDisplayValue('my-session')
    fireEvent.change(input, { target: { value: 'new-name' } })
    fireEvent.keyDown(input, { key: 'Enter' })
    expect(defaultProps.onConfirm).toHaveBeenCalledWith('new-name')
  })

  it('calls onCancel on Escape', () => {
    render(<RenamePopover {...defaultProps} />)
    const input = screen.getByDisplayValue('my-session')
    fireEvent.keyDown(input, { key: 'Escape' })
    expect(defaultProps.onCancel).toHaveBeenCalled()
  })

  it('does not call onConfirm with empty name', () => {
    render(<RenamePopover {...defaultProps} />)
    const input = screen.getByDisplayValue('my-session')
    fireEvent.change(input, { target: { value: '' } })
    fireEvent.keyDown(input, { key: 'Enter' })
    expect(defaultProps.onConfirm).not.toHaveBeenCalled()
  })

  it('does not call onConfirm when name unchanged', () => {
    render(<RenamePopover {...defaultProps} />)
    const input = screen.getByDisplayValue('my-session')
    fireEvent.keyDown(input, { key: 'Enter' })
    expect(defaultProps.onConfirm).not.toHaveBeenCalled()
  })

  it('shows error message when provided', () => {
    render(<RenamePopover {...defaultProps} error="Rename failed" />)
    expect(screen.getByText('Rename failed')).toBeInTheDocument()
  })

  describe('vertical viewport clamping', () => {
    let offsetHeightDescriptor: PropertyDescriptor | undefined
    let innerHeightDescriptor: PropertyDescriptor | undefined

    beforeEach(() => {
      offsetHeightDescriptor = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'offsetHeight')
      innerHeightDescriptor = Object.getOwnPropertyDescriptor(window, 'innerHeight')
    })

    afterEach(() => {
      if (offsetHeightDescriptor) {
        Object.defineProperty(HTMLElement.prototype, 'offsetHeight', offsetHeightDescriptor)
      }
      if (innerHeightDescriptor) {
        Object.defineProperty(window, 'innerHeight', innerHeightDescriptor)
      }
    })

    it('positions popover below anchor when space is sufficient', () => {
      Object.defineProperty(HTMLElement.prototype, 'offsetHeight', { configurable: true, get: () => 40 })
      Object.defineProperty(window, 'innerHeight', { configurable: true, value: 800 })
      const anchor = { left: 100, top: 30, width: 120, height: 26, bottom: 56, right: 220 } as DOMRect
      const { container } = render(<RenamePopover {...defaultProps} anchorRect={anchor} />)
      const el = container.firstElementChild as HTMLElement
      expect(el.style.top).toBe(`${anchor.bottom + 4}px`)
    })

    it('flips popover above anchor when below would overflow', () => {
      Object.defineProperty(HTMLElement.prototype, 'offsetHeight', { configurable: true, get: () => 40 })
      Object.defineProperty(window, 'innerHeight', { configurable: true, value: 200 })
      const anchor = { left: 100, top: 170, width: 120, height: 20, bottom: 190, right: 220 } as DOMRect
      const { container } = render(<RenamePopover {...defaultProps} anchorRect={anchor} />)
      const el = container.firstElementChild as HTMLElement
      // anchorRect.top - PADDING - popoverHeight = 170 - 4 - 40 = 126
      expect(el.style.top).toBe('126px')
    })

    it('clamps to PADDING when both above and below overflow', () => {
      Object.defineProperty(HTMLElement.prototype, 'offsetHeight', { configurable: true, get: () => 40 })
      Object.defineProperty(window, 'innerHeight', { configurable: true, value: 50 })
      const anchor = { left: 100, top: 10, width: 120, height: 20, bottom: 30, right: 220 } as DOMRect
      const { container } = render(<RenamePopover {...defaultProps} anchorRect={anchor} />)
      const el = container.firstElementChild as HTMLElement
      // below: 30 + 4 = 34, 34 + 40 = 74 > 50 - 4 = 46 → flip
      // above: 10 - 4 - 40 = -34 < 4 → clamp to PADDING
      expect(el.style.top).toBe('4px')
    })

    it('recalculates position when error changes popover height', () => {
      Object.defineProperty(HTMLElement.prototype, 'offsetHeight', { configurable: true, get: () => 40 })
      Object.defineProperty(window, 'innerHeight', { configurable: true, value: 200 })
      // Anchor near bottom but popover (height=40) fits below: 160 + 4 + 40 = 204 > 196 → actually flips
      // Use anchor where height=40 fits below but height=70 does not
      const anchor = { left: 100, top: 130, width: 120, height: 20, bottom: 150, right: 220 } as DOMRect
      // below: 150 + 4 = 154, 154 + 40 = 194 < 200 - 4 = 196 → fits below
      const { container, rerender } = render(<RenamePopover {...defaultProps} anchorRect={anchor} />)
      const el = container.firstElementChild as HTMLElement
      expect(el.style.top).toBe(`${anchor.bottom + 4}px`)

      // Now error appears, popover grows taller
      Object.defineProperty(HTMLElement.prototype, 'offsetHeight', { configurable: true, get: () => 70 })
      rerender(<RenamePopover {...defaultProps} anchorRect={anchor} error="Name taken" />)
      // below: 150 + 4 = 154, 154 + 70 = 224 > 196 → flip above
      // above: 130 - 4 - 70 = 56
      expect(el.style.top).toBe('56px')
    })
  })
})
