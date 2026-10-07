import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, cleanup, waitFor } from '@testing-library/react'
import { WorkspaceContextMenu } from './WorkspaceContextMenu'
import { TITLE_BAR_HEIGHT } from '../../../components/FloatingPanel'

describe('WorkspaceContextMenu', () => {
  beforeEach(() => { cleanup() })

  afterEach(() => {
    // Restore electronAPI after each test
    Object.defineProperty(window, 'electronAPI', {
      value: undefined,
      writable: true,
      configurable: true,
    })
  })

  it('renders Settings menu item', () => {
    render(<WorkspaceContextMenu position={{ x: 100, y: 200 }} onSettings={vi.fn()} onClose={vi.fn()} />)
    expect(screen.getByText(/settings/i)).toBeInTheDocument()
  })

  it('calls onSettings and onClose when clicking settings', () => {
    const onSettings = vi.fn()
    const onClose = vi.fn()
    render(<WorkspaceContextMenu position={{ x: 100, y: 200 }} onSettings={onSettings} onClose={onClose} />)
    fireEvent.click(screen.getByText(/settings/i))
    expect(onSettings).toHaveBeenCalled()
    expect(onClose).toHaveBeenCalled()
  })

  it('calls onClose on backdrop click', () => {
    const onClose = vi.fn()
    render(<WorkspaceContextMenu position={{ x: 100, y: 200 }} onSettings={vi.fn()} onClose={onClose} />)
    fireEvent.mouseDown(screen.getByTestId('context-menu-backdrop'))
    expect(onClose).toHaveBeenCalled()
  })

  it('shows tear-off option when onTearOff is provided', () => {
    Object.defineProperty(window, 'electronAPI', {
      value: { getWindows: vi.fn().mockResolvedValue([]) },
      writable: true,
      configurable: true,
    })
    render(
      <WorkspaceContextMenu
        position={{ x: 100, y: 200 }}
        onSettings={vi.fn()}
        onClose={vi.fn()}
        onTearOff={vi.fn()}
      />,
    )
    expect(screen.getByText(/move to new window/i)).toBeInTheDocument()
  })

  it('hides tear-off option when onTearOff is not provided', () => {
    render(
      <WorkspaceContextMenu
        position={{ x: 100, y: 200 }}
        onSettings={vi.fn()}
        onClose={vi.fn()}
      />,
    )
    expect(screen.queryByText(/move to new window/i)).not.toBeInTheDocument()
  })

  it('shows merge submenu trigger when onMergeTo is provided and windows exist', async () => {
    Object.defineProperty(window, 'electronAPI', {
      value: {
        getWindows: vi.fn().mockResolvedValue([
          { id: 'win-1', title: 'Window 1' },
          { id: 'win-2', title: 'Window 2' },
        ]),
      },
      writable: true,
      configurable: true,
    })
    render(
      <WorkspaceContextMenu
        position={{ x: 100, y: 200 }}
        onSettings={vi.fn()}
        onClose={vi.fn()}
        onMergeTo={vi.fn()}
      />,
    )
    await waitFor(() => {
      expect(screen.getByText(/move to window/i)).toBeInTheDocument()
    })
  })

  it('hides merge when onMergeTo is not provided', async () => {
    Object.defineProperty(window, 'electronAPI', {
      value: {
        getWindows: vi.fn().mockResolvedValue([
          { id: 'win-1', title: 'Window 1' },
        ]),
      },
      writable: true,
      configurable: true,
    })
    render(
      <WorkspaceContextMenu
        position={{ x: 100, y: 200 }}
        onSettings={vi.fn()}
        onClose={vi.fn()}
      />,
    )
    // Give time for async load
    await waitFor(() => {
      expect(screen.queryByText(/move to window/i)).not.toBeInTheDocument()
    })
  })

  it('hides merge when window list is empty', async () => {
    Object.defineProperty(window, 'electronAPI', {
      value: {
        getWindows: vi.fn().mockResolvedValue([]),
      },
      writable: true,
      configurable: true,
    })
    render(
      <WorkspaceContextMenu
        position={{ x: 100, y: 200 }}
        onSettings={vi.fn()}
        onClose={vi.fn()}
        onMergeTo={vi.fn()}
      />,
    )
    await waitFor(() => {
      expect(screen.queryByText(/move to window/i)).not.toBeInTheDocument()
    })
  })

  it('shows loading state while fetching windows', () => {
    // getWindows never resolves in this test (pending promise)
    Object.defineProperty(window, 'electronAPI', {
      value: {
        getWindows: vi.fn().mockReturnValue(new Promise(() => {})),
      },
      writable: true,
      configurable: true,
    })
    render(
      <WorkspaceContextMenu
        position={{ x: 100, y: 200 }}
        onSettings={vi.fn()}
        onClose={vi.fn()}
        onMergeTo={vi.fn()}
      />,
    )
    expect(screen.getByText(/loading/i)).toBeInTheDocument()
  })

  it('calls onTearOff and onClose when tear-off clicked', () => {
    Object.defineProperty(window, 'electronAPI', {
      value: { getWindows: vi.fn().mockResolvedValue([]) },
      writable: true,
      configurable: true,
    })
    const onTearOff = vi.fn()
    const onClose = vi.fn()
    render(
      <WorkspaceContextMenu
        position={{ x: 100, y: 200 }}
        onSettings={vi.fn()}
        onClose={onClose}
        onTearOff={onTearOff}
      />,
    )
    fireEvent.click(screen.getByText(/move to new window/i))
    expect(onTearOff).toHaveBeenCalled()
    expect(onClose).toHaveBeenCalled()
  })

  it('shows fallback label when window title is empty', async () => {
    Object.defineProperty(window, 'electronAPI', {
      value: {
        getWindows: vi.fn().mockResolvedValue([
          { id: 'win-abc', title: '' },
        ]),
      },
      writable: true,
      configurable: true,
    })
    render(
      <WorkspaceContextMenu
        position={{ x: 100, y: 200 }}
        onSettings={vi.fn()}
        onClose={vi.fn()}
        onMergeTo={vi.fn()}
      />,
    )
    await waitFor(() => {
      expect(screen.getByText('Purdex')).toBeInTheDocument()
    })
  })

  it('calls onMergeTo with windowId when merge target clicked', async () => {
    Object.defineProperty(window, 'electronAPI', {
      value: {
        getWindows: vi.fn().mockResolvedValue([
          { id: 'win-42', title: 'My Other Window' },
        ]),
      },
      writable: true,
      configurable: true,
    })
    const onMergeTo = vi.fn()
    const onClose = vi.fn()
    render(
      <WorkspaceContextMenu
        position={{ x: 100, y: 200 }}
        onSettings={vi.fn()}
        onClose={onClose}
        onMergeTo={onMergeTo}
      />,
    )
    // Wait for window list to load and appear
    const target = await screen.findByText('My Other Window')
    fireEvent.click(target)
    expect(onMergeTo).toHaveBeenCalledWith('win-42')
    expect(onClose).toHaveBeenCalled()
  })
})

// #1825. The menu used to open exactly where clicked with no viewport correction
// at all. It now gets TabContextMenu's (#1801): pulled back inside the right and
// bottom edges, never inside the title bar's OS drag region (the top
// TITLE_BAR_HEIGHT px, where a click drags the window instead of choosing an
// item), and capped + scrolling when it would still be cut off at the bottom.
describe('WorkspaceContextMenu stays inside the window and below the title bar (#1825)', () => {
  const saved: Array<[string, PropertyDescriptor | undefined]> = []
  const menu = () => screen.getByTestId('workspace-context-menu')

  /** jsdom lays nothing out: the menu's own box is the only one its placement reads. */
  function stubLayout(box: { width: number; height: number }, viewport: { width: number; height: number }) {
    vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue(
      { left: 0, top: 0, width: box.width, height: box.height, right: box.width, bottom: box.height, x: 0, y: 0, toJSON: () => ({}) } as DOMRect,
    )
    setViewport(viewport)
  }
  function setViewport(viewport: { width: number; height: number }) {
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: viewport.width })
    Object.defineProperty(window, 'innerHeight', { configurable: true, value: viewport.height })
  }
  function setElectronAPI(value: unknown) {
    Object.defineProperty(window, 'electronAPI', { value, writable: true, configurable: true })
  }
  const menuAt = (position: { x: number; y: number }, onMergeTo?: (id: string) => void) => (
    <WorkspaceContextMenu position={position} onSettings={vi.fn()} onClose={vi.fn()} onMergeTo={onMergeTo} />
  )

  beforeEach(() => {
    cleanup()
    for (const key of ['innerWidth', 'innerHeight']) saved.push([key, Object.getOwnPropertyDescriptor(window, key)])
  })
  afterEach(() => {
    cleanup()
    vi.restoreAllMocks()
    setElectronAPI(undefined)
    for (const [key, descriptor] of saved.splice(0)) {
      if (descriptor) Object.defineProperty(window, key, descriptor)
      else delete (window as unknown as Record<string, unknown>)[key]
    }
  })

  it('room to open where clicked: the same place as before, and no cap', () => {
    stubLayout({ width: 180, height: 120 }, { width: 1024, height: 800 })
    render(menuAt({ x: 100, y: 200 }))
    expect(menu().style.left).toBe('100px')
    expect(menu().style.top).toBe('200px')
    expect(menu().style.maxHeight).toBe('')
    expect(menu().style.overflowY).toBe('')
  })

  it('opened past the right and bottom edges: pulled back inside the window', () => {
    stubLayout({ width: 180, height: 120 }, { width: 1024, height: 800 })
    render(menuAt({ x: 1000, y: 750 }))
    expect(menu().style.left).toBe('840px') // 1024 - 180 - 4
    expect(menu().style.top).toBe('676px') // 800 - 120 - 4
    expect(menu().style.maxHeight).toBe('')
  })

  it('a negative x is pulled in to 4px', () => {
    stubLayout({ width: 180, height: 120 }, { width: 1024, height: 800 })
    render(menuAt({ x: -10, y: 200 }))
    expect(menu().style.left).toBe('4px')
  })

  it('a menu opened inside the title bar band starts below it, uncapped when it fits', () => {
    stubLayout({ width: 180, height: 120 }, { width: 1024, height: 800 })
    render(menuAt({ x: 100, y: 10 }))
    expect(menu().style.top).toBe(`${TITLE_BAR_HEIGHT}px`)
    expect(menu().style.maxHeight).toBe('')
  })

  it('a tall menu opened near the top of a short window starts below the title bar and scrolls', () => {
    stubLayout({ width: 180, height: 400 }, { width: 1024, height: 300 })
    render(menuAt({ x: 100, y: 40 }))
    // 40 + 400 > 300 → moved up to 300 - 400 - 4 = -104 → the title bar's bottom edge
    expect(menu().style.top).toBe(`${TITLE_BAR_HEIGHT}px`)
    expect(menu().style.maxHeight).toBe(`${300 - TITLE_BAR_HEIGHT - 4}px`)
    expect(menu().style.overflowY).toBe('auto')
    expect(menu().style.left).toBe('100px')
  })

  it('a tall menu opened near the bottom moves up only as far as the title bar, not to 4px', () => {
    stubLayout({ width: 180, height: 400 }, { width: 1024, height: 300 })
    render(menuAt({ x: 100, y: 280 }))
    expect(menu().style.top).toBe(`${TITLE_BAR_HEIGHT}px`)
    expect(menu().style.maxHeight).toBe(`${300 - TITLE_BAR_HEIGHT - 4}px`)
    expect(menu().style.overflowY).toBe('auto')
  })

  it('drops an earlier cap when it opens again with room to spare', () => {
    stubLayout({ width: 180, height: 400 }, { width: 1024, height: 300 })
    const { rerender } = render(menuAt({ x: 100, y: 40 }))
    expect(menu().style.maxHeight).toBe(`${300 - TITLE_BAR_HEIGHT - 4}px`)
    setViewport({ width: 1024, height: 800 })
    rerender(menuAt({ x: 100, y: 100 }))
    expect(menu().style.top).toBe('100px')
    expect(menu().style.maxHeight).toBe('')
    expect(menu().style.overflowY).toBe('')
  })

  it('placed again when the window list arrives and makes it taller', async () => {
    // Its height follows its rows: 50px per button (Settings + "Loading…", then Settings + 3 windows).
    vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(function (this: HTMLElement) {
      const height = this.querySelectorAll('button').length * 50
      return { left: 0, top: 0, width: 180, height, right: 180, bottom: height, x: 0, y: 0, toJSON: () => ({}) } as DOMRect
    })
    setViewport({ width: 1024, height: 800 })
    setElectronAPI({
      getWindows: vi.fn().mockResolvedValue([
        { id: 'w1', title: 'One' },
        { id: 'w2', title: 'Two' },
        { id: 'w3', title: 'Three' },
      ]),
    })
    render(menuAt({ x: 100, y: 650 }, vi.fn()))
    expect(menu().style.top).toBe('650px') // 650 + 100 fits
    await screen.findByText('Three')
    expect(menu().style.top).toBe('596px') // 650 + 200 > 800 → 800 - 200 - 4
  })

  it('keeps its scroll position when the window list arrives under a capped menu', async () => {
    // Re-measuring takes the cap off, and in that layout the content fits, so a
    // real browser drops the scroll offset. jsdom keeps it; the stub plays that part.
    vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(function (this: HTMLElement) {
      if (this.style.maxHeight === '') this.scrollTop = 0
      return { left: 0, top: 0, width: 180, height: 400, right: 180, bottom: 400, x: 0, y: 0, toJSON: () => ({}) } as DOMRect
    })
    setViewport({ width: 1024, height: 300 })
    let resolveWindows: (list: ElectronWindowInfo[]) => void = () => {}
    setElectronAPI({ getWindows: vi.fn(() => new Promise<ElectronWindowInfo[]>((resolve) => { resolveWindows = resolve })) })
    render(menuAt({ x: 100, y: 40 }, vi.fn()))
    const el = menu()
    expect(el.style.maxHeight).toBe(`${300 - TITLE_BAR_HEIGHT - 4}px`)
    el.scrollTop = 120
    resolveWindows([{ id: 'w1', title: 'One' }] as ElectronWindowInfo[])
    await screen.findByText('One')
    expect(el.style.maxHeight).toBe(`${300 - TITLE_BAR_HEIGHT - 4}px`)
    expect(el.scrollTop).toBe(120)
  })

  it('the menu is marked no-drag, so the title bar region does not take its pointer events', () => {
    render(menuAt({ x: 100, y: 200 }))
    // React assigns the camelCase property, which jsdom keeps as is (see FloatingPanel.test.tsx).
    expect((menu().style as unknown as { WebkitAppRegion?: string }).WebkitAppRegion).toBe('no-drag')
  })

  it('the backdrop is marked no-drag too, so a click on it over the title bar closes the menu instead of dragging the window', () => {
    render(menuAt({ x: 100, y: 200 }))
    const backdrop = screen.getByTestId('context-menu-backdrop')
    expect((backdrop.style as unknown as { WebkitAppRegion?: string }).WebkitAppRegion).toBe('no-drag')
  })
})
