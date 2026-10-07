import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import { PaneContextMenu, type MenuItem } from './PaneContextMenu'
import { TITLE_BAR_HEIGHT } from './FloatingPanel'

function renderMenu(overrides?: { canDetach?: boolean; extraItems?: MenuItem[] }) {
  const props = {
    position: { x: 100, y: 100 },
    canDetach: overrides?.canDetach ?? true,
    extraItems: overrides?.extraItems,
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

  describe('extraItems', () => {
    it('renders nothing extra by default (no trailing separator)', () => {
      const { container } = render(
        <PaneContextMenu position={{ x: 0, y: 0 }} canDetach={false} onClose={vi.fn()} onAction={vi.fn()} />,
      )
      expect(container.querySelectorAll('.border-t').length).toBe(0)
      expect(screen.queryByText('Hand to nex')).not.toBeInTheDocument()
    })

    it('renders extra items after a separator, below the built-in items', () => {
      const { container } = render(
        <PaneContextMenu
          position={{ x: 0, y: 0 }}
          canDetach={false}
          extraItems={[{ label: 'Hand to nex', action: 'hand-to-nex' }]}
          onClose={vi.fn()}
          onAction={vi.fn()}
        />,
      )
      const menu = container.firstChild as HTMLElement
      const children = Array.from(menu.children)
      const sepIdx = children.findIndex((el) => el.className.includes('border-t'))
      const extraIdx = children.findIndex((el) => el.textContent === 'Hand to nex')
      const splitIdx = children.findIndex((el) => el.textContent === 'Split Vertical')
      expect(sepIdx).toBeGreaterThan(splitIdx)
      expect(extraIdx).toBe(sepIdx + 1)
    })

    it('dispatches the extra item action and closes', () => {
      const props = renderMenu({ canDetach: true, extraItems: [{ label: 'Hand to nex', action: 'hand-to-nex' }] })
      fireEvent.click(screen.getByText('Hand to nex'))
      expect(props.onAction).toHaveBeenCalledWith('hand-to-nex')
      expect(props.onClose).toHaveBeenCalled()
    })

    it('an empty extraItems array renders no separator', () => {
      const { container } = render(
        <PaneContextMenu position={{ x: 0, y: 0 }} canDetach={false} extraItems={[]} onClose={vi.fn()} onAction={vi.fn()} />,
      )
      expect(container.querySelectorAll('.border-t').length).toBe(0)
    })
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

// #1825 (the #1801 fix, applied here). The top TITLE_BAR_HEIGHT px of the window
// is the title bar's OS drag region: a click there drags the window instead of
// choosing an item, so no part of the menu may be placed in it — a tall one
// scrolls instead.
describe('PaneContextMenu stays below the title bar (#1825)', () => {
  const saved: Array<[string, PropertyDescriptor | undefined]> = []

  /** jsdom lays nothing out: the menu's own box is the only one its placement reads. */
  function stubLayout(menu: { width: number; height: number }, viewport: { width: number; height: number }) {
    vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue(
      { left: 0, top: 0, width: menu.width, height: menu.height, right: menu.width, bottom: menu.height, x: 0, y: 0, toJSON: () => ({}) } as DOMRect,
    )
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: viewport.width })
    Object.defineProperty(window, 'innerHeight', { configurable: true, value: viewport.height })
  }

  /** The fullest menu a pane gets: split items, close / detach, and an extra item. */
  function menuAt(position: { x: number; y: number }) {
    return (
      <PaneContextMenu
        position={position}
        canDetach
        extraItems={[{ label: 'Hand to nex', action: 'hand-to-nex' }]}
        onClose={vi.fn()}
        onAction={vi.fn()}
      />
    )
  }

  beforeEach(() => {
    cleanup()
    for (const key of ['innerWidth', 'innerHeight']) saved.push([key, Object.getOwnPropertyDescriptor(window, key)])
  })
  afterEach(() => {
    cleanup()
    vi.restoreAllMocks()
    for (const [key, descriptor] of saved.splice(0)) {
      if (descriptor) Object.defineProperty(window, key, descriptor)
      else delete (window as unknown as Record<string, unknown>)[key]
    }
  })

  it('a tall menu opened near the top of a short window starts below the title bar and scrolls', () => {
    stubLayout({ width: 200, height: 400 }, { width: 1024, height: 300 })
    const { container } = render(menuAt({ x: 100, y: 40 }))
    const el = container.firstElementChild as HTMLElement
    // 40 + 400 > 300 → moved up to 300 - 400 - 4 = -104 → the title bar's bottom edge
    expect(el.style.top).toBe(`${TITLE_BAR_HEIGHT}px`)
    expect(el.style.maxHeight).toBe(`${300 - TITLE_BAR_HEIGHT - 4}px`)
    expect(el.style.overflowY).toBe('auto')
    expect(el.style.left).toBe('100px')
  })

  it('a tall menu opened near the bottom moves up only as far as the title bar, not to 4px', () => {
    stubLayout({ width: 200, height: 400 }, { width: 1024, height: 300 })
    const { container } = render(menuAt({ x: 100, y: 280 }))
    const el = container.firstElementChild as HTMLElement
    expect(el.style.top).toBe(`${TITLE_BAR_HEIGHT}px`)
    expect(el.style.maxHeight).toBe(`${300 - TITLE_BAR_HEIGHT - 4}px`)
    expect(el.style.overflowY).toBe('auto')
  })

  it('a menu opened inside the title bar band starts below it, uncapped when it fits', () => {
    stubLayout({ width: 200, height: 150 }, { width: 1024, height: 800 })
    const { container } = render(menuAt({ x: 100, y: 10 }))
    const el = container.firstElementChild as HTMLElement
    expect(el.style.top).toBe(`${TITLE_BAR_HEIGHT}px`)
    expect(el.style.maxHeight).toBe('')
  })

  it('room to open where clicked: the same place as before, and no cap', () => {
    stubLayout({ width: 200, height: 150 }, { width: 1024, height: 800 })
    const { container } = render(menuAt({ x: 100, y: 100 }))
    const el = container.firstElementChild as HTMLElement
    expect(el.style.left).toBe('100px')
    expect(el.style.top).toBe('100px')
    expect(el.style.maxHeight).toBe('')
    expect(el.style.overflowY).toBe('')
  })

  it('near the right and bottom edges: corrected as before, and no cap', () => {
    stubLayout({ width: 200, height: 150 }, { width: 1024, height: 800 })
    const { container } = render(menuAt({ x: 1000, y: 700 }))
    const el = container.firstElementChild as HTMLElement
    expect(el.style.left).toBe('820px') // 1024 - 200 - 4
    expect(el.style.top).toBe('646px') // 800 - 150 - 4
    expect(el.style.maxHeight).toBe('')
    expect(el.style.overflowY).toBe('')
  })

  it('a menu that ends inside the window\'s last 4px is left where it was, not clipped', () => {
    stubLayout({ width: 200, height: 150 }, { width: 1024, height: 800 })
    const { container } = render(menuAt({ x: 100, y: 648 })) // 648 + 150 = 798: fits, so never moved
    const el = container.firstElementChild as HTMLElement
    expect(el.style.top).toBe('648px')
    expect(el.style.maxHeight).toBe('')
  })

  it('drops an earlier cap when it opens again with room to spare', () => {
    stubLayout({ width: 200, height: 400 }, { width: 1024, height: 300 })
    const { container, rerender } = render(menuAt({ x: 100, y: 40 }))
    const el = container.firstElementChild as HTMLElement
    expect(el.style.maxHeight).toBe(`${300 - TITLE_BAR_HEIGHT - 4}px`)
    Object.defineProperty(window, 'innerHeight', { configurable: true, value: 800 })
    rerender(menuAt({ x: 100, y: 100 }))
    expect(el.style.top).toBe('100px')
    expect(el.style.maxHeight).toBe('')
    expect(el.style.overflowY).toBe('')
  })

  it('is marked no-drag, so the title bar region does not take its pointer events', () => {
    const { container } = render(menuAt({ x: 100, y: 100 }))
    const el = container.firstElementChild as HTMLElement
    // React assigns the camelCase property, which jsdom keeps as is (see FloatingPanel.test.tsx).
    expect((el.style as unknown as { WebkitAppRegion?: string }).WebkitAppRegion).toBe('no-drag')
  })
})
