import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import { PaneContextMenu } from './PaneContextMenu'

function renderMenu(overrides?: { canDetach?: boolean }) {
  const props = {
    position: { x: 100, y: 100 },
    canDetach: overrides?.canDetach ?? true,
    onClose: vi.fn(),
    onAction: vi.fn(),
  }
  render(<PaneContextMenu {...props} />)
  return props
}

describe('PaneContextMenu', () => {
  beforeEach(() => { cleanup(); vi.clearAllMocks() })

  it('always renders Split Horizontal / Split Vertical', () => {
    renderMenu({ canDetach: false })
    expect(screen.getByText('Split Horizontal')).toBeInTheDocument()
    expect(screen.getByText('Split Vertical')).toBeInTheDocument()
  })

  it('hides Close pane / Detach to tab when canDetach is false', () => {
    renderMenu({ canDetach: false })
    expect(screen.queryByText('Close pane')).not.toBeInTheDocument()
    expect(screen.queryByText('Detach to tab')).not.toBeInTheDocument()
  })

  it('shows Close pane / Detach to tab when canDetach is true', () => {
    renderMenu({ canDetach: true })
    expect(screen.getByText('Close pane')).toBeInTheDocument()
    expect(screen.getByText('Detach to tab')).toBeInTheDocument()
  })

  it('calls onAction(split-h) + onClose when clicking Split Horizontal', () => {
    const props = renderMenu({ canDetach: false })
    fireEvent.click(screen.getByText('Split Horizontal'))
    expect(props.onAction).toHaveBeenCalledWith('split-h')
    expect(props.onClose).toHaveBeenCalled()
  })

  it('fires split-v / close / detach actions', () => {
    const p1 = renderMenu({ canDetach: true })
    fireEvent.click(screen.getByText('Split Vertical'))
    expect(p1.onAction).toHaveBeenCalledWith('split-v')
    cleanup()

    const p2 = renderMenu({ canDetach: true })
    fireEvent.click(screen.getByText('Close pane'))
    expect(p2.onAction).toHaveBeenCalledWith('close')
    cleanup()

    const p3 = renderMenu({ canDetach: true })
    fireEvent.click(screen.getByText('Detach to tab'))
    expect(p3.onAction).toHaveBeenCalledWith('detach')
  })

  it('calls onClose on Escape', () => {
    const props = renderMenu()
    fireEvent.keyDown(document, { key: 'Escape' })
    expect(props.onClose).toHaveBeenCalled()
  })

  it('calls onClose on click-outside', () => {
    const props = renderMenu()
    fireEvent.mouseDown(document.body)
    expect(props.onClose).toHaveBeenCalled()
  })

  it('flips position to stay within the viewport near the right/bottom edge', () => {
    const spy = vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue({
      width: 200, height: 150, top: 0, left: 0, right: 0, bottom: 0, x: 0, y: 0, toJSON: () => {},
    } as DOMRect)
    const props = {
      position: { x: window.innerWidth - 5, y: window.innerHeight - 5 },
      canDetach: true,
      onClose: vi.fn(),
      onAction: vi.fn(),
    }
    const { container } = render(<PaneContextMenu {...props} />)
    const menu = container.firstChild as HTMLElement
    expect(parseFloat(menu.style.left)).toBe(window.innerWidth - 200 - 4)
    expect(parseFloat(menu.style.top)).toBe(window.innerHeight - 150 - 4)
    spy.mockRestore()
  })
})
