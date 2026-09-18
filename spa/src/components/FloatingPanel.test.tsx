import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { useRef } from 'react'
import { FloatingPanel } from './FloatingPanel'
import { getPlatformCapabilities } from '../lib/platform'
import type { PlatformCapabilities } from '../lib/platform'

vi.mock('../lib/platform', () => ({
  getPlatformCapabilities: vi.fn(() => ({
    isElectron: false,
    canTearOffTab: false,
    canMergeWindow: false,
    canBrowserPane: false,
    canSystemTray: false,
    canNotification: false,
    devUpdateEnabled: false,
    hasLocalFilesystem: false,
  })),
}))

/** The Electron title bar's drag region (see `FloatingPanel.tsx`'s `topInset`)
 * only exists when `getPlatformCapabilities().isElectron` is true — stub it
 * the same way `TabContextMenu.test.tsx` does. */
function mockElectron(isElectron: boolean) {
  vi.mocked(getPlatformCapabilities).mockReturnValue({
    isElectron,
    canTearOffTab: isElectron,
    canMergeWindow: isElectron,
    canBrowserPane: isElectron,
    canSystemTray: isElectron,
    canNotification: isElectron,
    devUpdateEnabled: false,
    hasLocalFilesystem: false,
  } satisfies PlatformCapabilities)
}

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

function TwoPanelHarness({ onCloseFirst, onCloseSecond }: { onCloseFirst: () => void; onCloseSecond: () => void }) {
  const anchor1 = useRef<HTMLButtonElement>(null)
  const anchor2 = useRef<HTMLButtonElement>(null)
  return (
    <div>
      <button ref={anchor1} data-testid="anchor-1">a1</button>
      <button ref={anchor2} data-testid="anchor-2">a2</button>
      <FloatingPanel title="First" anchorRef={anchor1} onClose={onCloseFirst}>
        <input data-testid="inside-1" />
      </FloatingPanel>
      <FloatingPanel title="Second" anchorRef={anchor2} onClose={onCloseSecond}>
        <input data-testid="inside-2" />
      </FloatingPanel>
    </div>
  )
}

function TwoPanelToggleHarness({
  open1 = true,
  open2 = true,
  onCloseFirst,
  onCloseSecond,
}: {
  open1?: boolean
  open2?: boolean
  onCloseFirst: () => void
  onCloseSecond: () => void
}) {
  const anchor1 = useRef<HTMLButtonElement>(null)
  const anchor2 = useRef<HTMLButtonElement>(null)
  return (
    <div>
      <button ref={anchor1} data-testid="anchor-1">a1</button>
      <button ref={anchor2} data-testid="anchor-2">a2</button>
      {open1 && (
        <FloatingPanel title="First" anchorRef={anchor1} onClose={onCloseFirst}>
          <input data-testid="inside-1" />
        </FloatingPanel>
      )}
      {open2 && (
        <FloatingPanel title="Second" anchorRef={anchor2} onClose={onCloseSecond}>
          <input data-testid="inside-2" />
        </FloatingPanel>
      )}
    </div>
  )
}

function NoFocusableHarness({ onClose }: { onClose: () => void }) {
  const anchor = useRef<HTMLButtonElement>(null)
  return (
    <div>
      <button ref={anchor} data-testid="anchor">anchor</button>
      <FloatingPanel title="Main" anchorRef={anchor} onClose={onClose}>
        <span data-testid="inside">no focusable content</span>
      </FloatingPanel>
    </div>
  )
}

function rect(el: HTMLElement, r: Partial<DOMRect>) {
  el.getBoundingClientRect = () => ({ left: 100, top: 50, width: 40, height: 20, right: 140, bottom: 70, x: 100, y: 50, toJSON() {} , ...r }) as DOMRect
}

beforeEach(() => {
  Object.defineProperty(window, 'innerWidth', { value: 1000, configurable: true })
  Object.defineProperty(window, 'innerHeight', { value: 800, configurable: true })
  mockElectron(false)
})

describe('FloatingPanel', () => {
  it('renders into document.body as a dialog titled with `title`, positioned under the anchor', () => {
    render(<Harness onClose={() => {}} />)
    const panel = screen.getByRole('dialog', { name: 'Main' })
    expect(panel.parentElement).toBe(document.body)
    expect(panel.style.position).toBe('fixed')
  })

  it('positions below the anchor rect, clamping only the left edge to the viewport', () => {
    const { unmount } = render(<Harness onClose={() => {}} />)
    unmount()
    // re-render with a stubbed anchor rect: stub before the panel mounts by rendering closed first
    const { rerender } = render(<Harness onClose={() => {}} open={false} />)
    // Near the right edge (exercises left clamping) but not near the bottom, so
    // placement stays the plain "below the anchor" case, not the near-bottom one.
    rect(screen.getByTestId('anchor'), { left: 990, top: 50, bottom: 70, right: 1000 })
    rerender(<Harness onClose={() => {}} open />)
    const panel = screen.getByTestId('floating-panel')
    expect(parseInt(panel.style.left)).toBeLessThanOrEqual(1000 - 320 - 4)
    expect(parseInt(panel.style.top)).toBe(70 + 4)
  })

  it('opens below the anchor when there is plenty of room', () => {
    const { rerender } = render(<Harness onClose={() => {}} open={false} />)
    rect(screen.getByTestId('anchor'), { bottom: 100 })
    rerender(<Harness onClose={() => {}} open />)
    const panel = screen.getByTestId('floating-panel')
    expect(parseInt(panel.style.top)).toBe(104)
    expect(panel.style.maxHeight).toBe('692px')
  })

  it('slides up just enough to keep MIN_PANEL_HEIGHT of room when the anchor is near the bottom', () => {
    const { rerender } = render(<Harness onClose={() => {}} open={false} />)
    rect(screen.getByTestId('anchor'), { bottom: 790 })
    rerender(<Harness onClose={() => {}} open />)
    const panel = screen.getByTestId('floating-panel')
    expect(parseInt(panel.style.top)).toBe(636)
    expect(panel.style.maxHeight).toBe('160px')
  })

  it('applies the same floor even when placing below the anchor would still leave less than MIN_PANEL_HEIGHT', () => {
    const { rerender } = render(<Harness onClose={() => {}} open={false} />)
    rect(screen.getByTestId('anchor'), { bottom: 700 })
    rerender(<Harness onClose={() => {}} open />)
    const panel = screen.getByTestId('floating-panel')
    expect(parseInt(panel.style.top)).toBe(636)
    expect(panel.style.maxHeight).toBe('160px')
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

  it('a drag never moves the panel fully off-screen, nor above the Electron title bar under Electron', () => {
    mockElectron(true)
    render(<Harness onClose={() => {}} />)
    const panel = screen.getByTestId('floating-panel')
    const handle = screen.getByTestId('floating-panel-handle')
    handle.setPointerCapture = () => {}
    handle.releasePointerCapture = () => {}
    fireEvent.pointerDown(handle, { clientX: 0, clientY: 0, pointerId: 1, button: 0 })
    fireEvent.pointerMove(handle, { clientX: -5000, clientY: -5000, pointerId: 1 })
    expect(parseInt(panel.style.left)).toBeGreaterThanOrEqual(-320 + 40)
    expect(parseInt(panel.style.top)).toBeGreaterThanOrEqual(36)
  })

  it('a drag never moves the panel fully off-screen in the browser (no title bar region to avoid)', () => {
    render(<Harness onClose={() => {}} />)
    const panel = screen.getByTestId('floating-panel')
    const handle = screen.getByTestId('floating-panel-handle')
    handle.setPointerCapture = () => {}
    handle.releasePointerCapture = () => {}
    fireEvent.pointerDown(handle, { clientX: 0, clientY: 0, pointerId: 1, button: 0 })
    fireEvent.pointerMove(handle, { clientX: -5000, clientY: -5000, pointerId: 1 })
    expect(parseInt(panel.style.left)).toBeGreaterThanOrEqual(-320 + 40)
    expect(parseInt(panel.style.top)).toBeGreaterThanOrEqual(4)
  })

  it('is marked no-drag so the Electron title bar region does not intercept its pointer events', () => {
    render(<Harness onClose={() => {}} />)
    const panel = screen.getByTestId('floating-panel')
    // jsdom doesn't recognize `-webkit-app-region` as a real CSS property, so it
    // never reaches `cssText` / `getAttribute('style')` via `getPropertyValue` or
    // `setProperty` — but React sets it with a plain camelCase assignment
    // (`style.WebkitAppRegion = ...`), which jsdom's CSSStyleDeclaration stores as
    // an ordinary own property and does expose back under that same name.
    const withCamel = panel.style as unknown as { WebkitAppRegion?: string }
    expect(withCamel.WebkitAppRegion).toBe('no-drag')
  })

  it('never exceeds the viewport height in the browser, and scrolls its body instead', () => {
    Object.defineProperty(window, 'innerHeight', { value: 300, configurable: true })
    render(<Harness onClose={() => {}} />)
    const panel = screen.getByTestId('floating-panel')
    expect(panel.style.maxHeight).toBe('292px')
    const body = screen.getByTestId('inside').closest('div')
    expect(body?.style.overflowY).toBe('auto')
  })

  it('never exceeds the viewport height under Electron, leaving room for the title bar', () => {
    mockElectron(true)
    Object.defineProperty(window, 'innerHeight', { value: 300, configurable: true })
    render(<Harness onClose={() => {}} />)
    const panel = screen.getByTestId('floating-panel')
    expect(panel.style.maxHeight).toBe('260px')
    const body = screen.getByTestId('inside').closest('div')
    expect(body?.style.overflowY).toBe('auto')
  })

  it('re-applies the max-height clamp on resize', () => {
    render(<Harness onClose={() => {}} />)
    const panel = screen.getByTestId('floating-panel')
    Object.defineProperty(window, 'innerHeight', { value: 250, configurable: true })
    fireEvent(window, new Event('resize'))
    expect(panel.style.maxHeight).toBe('242px')
  })

  it('still opens below the anchor under Electron, but the MIN_PANEL_HEIGHT floor never pushes it above the title bar', () => {
    mockElectron(true)
    Object.defineProperty(window, 'innerHeight', { value: 40, configurable: true })
    const { rerender } = render(<Harness onClose={() => {}} open={false} />)
    rect(screen.getByTestId('anchor'), { top: 38, bottom: 40, left: 10, right: 50 })
    rerender(<Harness onClose={() => {}} open />)
    const panel = screen.getByTestId('floating-panel')
    expect(parseInt(panel.style.top)).toBe(36)
    expect(parseInt(panel.style.top)).toBeGreaterThanOrEqual(36)
  })

  it('in the browser, only needs the ordinary padding above a short viewport (no title bar region)', () => {
    Object.defineProperty(window, 'innerHeight', { value: 40, configurable: true })
    const { rerender } = render(<Harness onClose={() => {}} open={false} />)
    rect(screen.getByTestId('anchor'), { top: 38, bottom: 40, left: 10, right: 50 })
    rerender(<Harness onClose={() => {}} open />)
    const panel = screen.getByTestId('floating-panel')
    expect(parseInt(panel.style.top)).toBeGreaterThanOrEqual(4)
  })

  it('pointer events inside the body do not start a drag', () => {
    render(<Harness onClose={() => {}} />)
    const panel = screen.getByTestId('floating-panel')
    const left0 = panel.style.left
    fireEvent.pointerDown(screen.getByTestId('inside'), { clientX: 10, clientY: 10, pointerId: 1, button: 0 })
    fireEvent.pointerMove(screen.getByTestId('inside'), { clientX: 60, clientY: 40, pointerId: 1 })
    expect(panel.style.left).toBe(left0)
  })

  it('moves focus into the panel on mount, to the first focusable descendant', () => {
    render(<Harness onClose={() => {}} />)
    expect(document.activeElement).toBe(screen.getByTestId('inside'))
  })

  it('restores focus to whatever had it before opening, once the panel unmounts', () => {
    const { rerender } = render(<Harness onClose={() => {}} open={false} />)
    const anchor = screen.getByTestId('anchor')
    anchor.focus()
    expect(document.activeElement).toBe(anchor)
    rerender(<Harness onClose={() => {}} open />)
    expect(document.activeElement).toBe(screen.getByTestId('inside'))
    rerender(<Harness onClose={() => {}} open={false} />)
    expect(document.activeElement).toBe(anchor)
  })

  it('focuses the panel root when it has no focusable child', () => {
    render(<NoFocusableHarness onClose={() => {}} />)
    expect(document.activeElement).toBe(screen.getByTestId('floating-panel'))
  })

  it('re-anchors on scroll when it has not been dragged (follows the anchor to its new rect)', () => {
    render(<Harness onClose={() => {}} />)
    const anchor = screen.getByTestId('anchor')
    const panel = screen.getByTestId('floating-panel')
    rect(anchor, { bottom: 300 })
    fireEvent.scroll(document)
    expect(parseInt(panel.style.top)).toBe(300 + 4)
  })

  it('clamps the dragged position to the viewport on resize instead of re-anchoring', () => {
    render(<Harness onClose={() => {}} />)
    const panel = screen.getByTestId('floating-panel')
    const handle = screen.getByTestId('floating-panel-handle')
    handle.setPointerCapture = () => {}
    handle.releasePointerCapture = () => {}
    fireEvent.pointerDown(handle, { clientX: 0, clientY: 0, pointerId: 1, button: 0 })
    fireEvent.pointerMove(handle, { clientX: 900, clientY: 700, pointerId: 1 })
    fireEvent.pointerUp(handle, { pointerId: 1 })
    Object.defineProperty(window, 'innerWidth', { value: 300, configurable: true })
    Object.defineProperty(window, 'innerHeight', { value: 200, configurable: true })
    fireEvent(window, new Event('resize'))
    expect(parseInt(panel.style.left)).toBeLessThanOrEqual(300 - 40)
    expect(parseInt(panel.style.top)).toBeLessThanOrEqual(200 - 40)
  })

  it('re-derives maxHeight from the dragged top on resize, not from the topInset', () => {
    render(<Harness onClose={() => {}} />)
    const panel = screen.getByTestId('floating-panel')
    const handle = screen.getByTestId('floating-panel-handle')
    handle.setPointerCapture = () => {}
    handle.releasePointerCapture = () => {}
    const top0 = parseInt(panel.style.top)
    fireEvent.pointerDown(handle, { clientX: 0, clientY: 0, pointerId: 1, button: 0 })
    fireEvent.pointerMove(handle, { clientX: 0, clientY: 300 - top0, pointerId: 1 })
    fireEvent.pointerUp(handle, { pointerId: 1 })
    expect(parseInt(panel.style.top)).toBe(300)
    fireEvent(window, new Event('resize'))
    expect(panel.style.maxHeight).toBe('496px')
  })

  it('recomputes maxHeight live while dragging: shrinks moving down, grows back moving up', () => {
    const { rerender } = render(<Harness onClose={() => {}} open={false} />)
    rect(screen.getByTestId('anchor'), { bottom: 100 })
    rerender(<Harness onClose={() => {}} open />)
    const panel = screen.getByTestId('floating-panel')
    const handle = screen.getByTestId('floating-panel-handle')
    handle.setPointerCapture = () => {}
    handle.releasePointerCapture = () => {}
    expect(parseInt(panel.style.top)).toBe(104)
    fireEvent.pointerDown(handle, { clientX: 0, clientY: 0, pointerId: 1, button: 0 })
    fireEvent.pointerMove(handle, { clientX: 0, clientY: 396, pointerId: 1 })
    expect(parseInt(panel.style.top)).toBe(500)
    expect(panel.style.maxHeight).toBe('296px')
    fireEvent.pointerMove(handle, { clientX: 0, clientY: 0, pointerId: 1 })
    expect(parseInt(panel.style.top)).toBe(104)
    expect(panel.style.maxHeight).toBe('692px')
  })

  it('does not re-anchor on scroll once the panel has been dragged', () => {
    render(<Harness onClose={() => {}} />)
    const panel = screen.getByTestId('floating-panel')
    const handle = screen.getByTestId('floating-panel-handle')
    handle.setPointerCapture = () => {}
    handle.releasePointerCapture = () => {}
    fireEvent.pointerDown(handle, { clientX: 0, clientY: 0, pointerId: 1, button: 0 })
    fireEvent.pointerMove(handle, { clientX: 50, clientY: 30, pointerId: 1 })
    fireEvent.pointerUp(handle, { pointerId: 1 })
    const left0 = panel.style.left
    const top0 = panel.style.top
    rect(screen.getByTestId('anchor'), { bottom: 700 })
    fireEvent.scroll(document)
    expect(panel.style.left).toBe(left0)
    expect(panel.style.top).toBe(top0)
  })

  it('ignores an Escape sent by IME composition; a plain Escape still closes', () => {
    const onClose = vi.fn()
    render(<Harness onClose={onClose} />)
    fireEvent.keyDown(document, { key: 'Escape', isComposing: true })
    expect(onClose).not.toHaveBeenCalled()
    fireEvent.keyDown(document, { key: 'Escape' })
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('Escape only closes the topmost (last-mounted) panel when several are open', () => {
    const onCloseFirst = vi.fn()
    const onCloseSecond = vi.fn()
    render(<TwoPanelHarness onCloseFirst={onCloseFirst} onCloseSecond={onCloseSecond} />)
    fireEvent.keyDown(document, { key: 'Escape' })
    expect(onCloseFirst).not.toHaveBeenCalled()
    expect(onCloseSecond).toHaveBeenCalledTimes(1)
  })

  it('unmounting one panel does not steal focus from another still-open panel', () => {
    const { rerender } = render(
      <TwoPanelToggleHarness onCloseFirst={() => {}} onCloseSecond={() => {}} />,
    )
    // Mount order runs First's focus effect before Second's, so focus ends up in Second.
    const inside2 = screen.getByTestId('inside-2')
    expect(document.activeElement).toBe(inside2)
    rerender(<TwoPanelToggleHarness open1={false} onCloseFirst={() => {}} onCloseSecond={() => {}} />)
    expect(document.activeElement).toBe(inside2)
  })

  it('falls back to its own anchor when the previously focused element is gone', () => {
    const { rerender } = render(
      <TwoPanelToggleHarness open1 open2={false} onCloseFirst={() => {}} onCloseSecond={() => {}} />,
    )
    // Focus starts in the first panel.
    expect(document.activeElement).toBe(screen.getByTestId('inside-1'))
    // Opening the second panel moves focus into it; Second remembers inside-1 as "previously focused".
    rerender(<TwoPanelToggleHarness open1 open2 onCloseFirst={() => {}} onCloseSecond={() => {}} />)
    expect(document.activeElement).toBe(screen.getByTestId('inside-2'))
    // Unmounting the first while the second is open must not touch focus (Second still owns it).
    rerender(<TwoPanelToggleHarness open1={false} open2 onCloseFirst={() => {}} onCloseSecond={() => {}} />)
    expect(document.activeElement).toBe(screen.getByTestId('inside-2'))
    // Unmounting the second: its remembered element (inside-1) is gone, so it falls back to its own anchor.
    rerender(<TwoPanelToggleHarness open1={false} open2={false} onCloseFirst={() => {}} onCloseSecond={() => {}} />)
    expect(document.activeElement).toBe(screen.getByTestId('anchor-2'))
  })
})
