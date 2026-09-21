import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import { useRef, useState } from 'react'
import { Menu, type MenuEntry, type MenuPlacement } from './Menu'
import { FloatingPanel } from './FloatingPanel'
import { getPlatformCapabilities } from '../lib/platform'
import type { PlatformCapabilities } from '../lib/platform'

const capabilities = (isElectron: boolean): PlatformCapabilities => ({
  isElectron,
  canTearOffTab: isElectron,
  canMergeWindow: isElectron,
  canBrowserPane: isElectron,
  canSystemTray: isElectron,
  canNotification: isElectron,
  devUpdateEnabled: false,
  hasLocalFilesystem: false,
})

vi.mock('../lib/platform', () => ({ getPlatformCapabilities: vi.fn() }))

beforeEach(() => {
  vi.mocked(getPlatformCapabilities).mockReturnValue(capabilities(false))
})

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

function entries(overrides: Partial<Record<'a' | 'b' | 'c', Partial<Extract<MenuEntry, { id: string }>>>> = {}, spies = { a: vi.fn(), b: vi.fn(), c: vi.fn() }): MenuEntry[] {
  return [
    { id: 'a', label: 'Alpha', testId: 'item-a', onSelect: spies.a, ...overrides.a },
    { id: 'b', label: 'Beta', testId: 'item-b', onSelect: spies.b, ...overrides.b },
    { divider: true },
    { id: 'c', label: 'Gamma', testId: 'item-c', onSelect: spies.c, ...overrides.c },
  ]
}

function Harness({ items, initiallyOpen = true, placement, onCloseSpy }: {
  items: MenuEntry[]
  initiallyOpen?: boolean
  placement?: MenuPlacement
  onCloseSpy?: () => void
}) {
  const trigger = useRef<HTMLButtonElement>(null)
  const [open, setOpen] = useState(initiallyOpen)
  return (
    <div>
      <button ref={trigger} data-testid="trigger" onClick={() => setOpen((o) => !o)}>
        open
      </button>
      <button data-testid="elsewhere">elsewhere</button>
      <Menu
        trigger={trigger}
        open={open}
        onClose={() => { onCloseSpy?.(); setOpen(false) }}
        items={items}
        label="Test menu"
        placement={placement}
        testId="menu"
      />
    </div>
  )
}

const key = (k: string) => fireEvent.keyDown(document.activeElement ?? document.body, { key: k })

describe('Menu — a11y', () => {
  it('renders nothing while closed', () => {
    render(<Harness items={entries()} initiallyOpen={false} />)
    expect(screen.queryByTestId('menu')).toBeNull()
  })

  it('is a portal on document.body, so an overflow-hidden ancestor cannot clip it', () => {
    const { container } = render(<div style={{ overflow: 'hidden' }}><Harness items={entries()} /></div>)
    const menu = screen.getByTestId('menu')
    expect(container.contains(menu)).toBe(false)
    expect(menu.parentElement).toBe(document.body)
    expect(menu.style.position).toBe('fixed')
  })

  it('role=menu with a label, role=menuitem items, a separator for the divider', () => {
    render(<Harness items={entries()} />)
    const menu = screen.getByRole('menu')
    expect(menu).toHaveAttribute('aria-label', 'Test menu')
    expect(screen.getAllByRole('menuitem')).toHaveLength(3)
    expect(screen.getAllByRole('separator')).toHaveLength(1)
  })

  it('an item with `checked` defined is a menuitemradio carrying aria-checked', () => {
    render(<Harness items={entries({ a: { checked: false }, b: { checked: true } })} />)
    expect(screen.getByTestId('item-a')).toHaveAttribute('role', 'menuitemradio')
    expect(screen.getByTestId('item-a')).toHaveAttribute('aria-checked', 'false')
    expect(screen.getByTestId('item-b')).toHaveAttribute('role', 'menuitemradio')
    expect(screen.getByTestId('item-b')).toHaveAttribute('aria-checked', 'true')
    expect(screen.getByTestId('item-c')).toHaveAttribute('role', 'menuitem')
    expect(screen.getByTestId('item-c')).not.toHaveAttribute('aria-checked')
  })

  it('disabled / busy items say so', () => {
    render(<Harness items={entries({ b: { disabled: true }, c: { busy: true } })} />)
    expect(screen.getByTestId('item-b')).toHaveAttribute('aria-disabled', 'true')
    expect(screen.getByTestId('item-a')).not.toHaveAttribute('aria-disabled')
    expect(screen.getByTestId('item-c')).toHaveAttribute('aria-busy', 'true')
  })

  it('a busy item is aria-disabled as well: it cannot be activated, and must say so', () => {
    render(<Harness items={entries({ c: { busy: true } })} />)
    expect(screen.getByTestId('item-c')).toHaveAttribute('aria-disabled', 'true')
    expect(screen.getByTestId('item-c')).toHaveAttribute('aria-busy', 'true')
  })

  it('shows hint, trailing content and a title', () => {
    render(<Harness items={entries({ a: { hint: 'a hint', trailing: <span data-testid="trail" />, title: 'Full name' } })} />)
    expect(screen.getByText('a hint')).toBeInTheDocument()
    expect(screen.getByTestId('trail')).toBeInTheDocument()
    expect(screen.getByTestId('item-a')).toHaveAttribute('title', 'Full name')
  })
})

describe('Menu — focus', () => {
  it('opening moves focus to the first enabled item', () => {
    render(<Harness items={entries({ a: { disabled: true } })} />)
    expect(screen.getByTestId('item-b')).toHaveFocus()
  })

  it('opening moves focus to the checked item when there is one', () => {
    render(<Harness items={entries({ a: { checked: false }, c: { checked: true } })} />)
    expect(screen.getByTestId('item-c')).toHaveFocus()
  })

  it('a checked but disabled item is not the landing spot', () => {
    render(<Harness items={entries({ a: { checked: false }, c: { checked: true, disabled: true } })} />)
    expect(screen.getByTestId('item-a')).toHaveFocus()
  })

  it('with nothing enabled the menu itself takes focus (Escape still reaches it)', () => {
    render(<Harness items={entries({ a: { disabled: true }, b: { disabled: true }, c: { disabled: true } })} />)
    expect(screen.getByTestId('menu')).toHaveFocus()
  })

  it('closing returns focus to the trigger', () => {
    render(<Harness items={entries()} initiallyOpen={false} />)
    fireEvent.click(screen.getByTestId('trigger'))
    expect(screen.getByTestId('item-a')).toHaveFocus()
    key('Escape')
    expect(screen.queryByTestId('menu')).toBeNull()
    expect(screen.getByTestId('trigger')).toHaveFocus()
  })

  it('does not steal focus back from an element that took it deliberately before the close', () => {
    render(<Harness items={entries()} />)
    screen.getByTestId('elsewhere').focus()
    fireEvent.mouseDown(screen.getByTestId('elsewhere'))
    expect(screen.queryByTestId('menu')).toBeNull()
    expect(screen.getByTestId('elsewhere')).toHaveFocus()
  })
})

describe('Menu — keyboard', () => {
  it('ArrowDown / ArrowUp move, skip the divider, and wrap', () => {
    render(<Harness items={entries()} />)
    expect(screen.getByTestId('item-a')).toHaveFocus()
    key('ArrowDown')
    expect(screen.getByTestId('item-b')).toHaveFocus()
    key('ArrowDown')
    expect(screen.getByTestId('item-c')).toHaveFocus()
    key('ArrowDown')
    expect(screen.getByTestId('item-a')).toHaveFocus() // wrapped
    key('ArrowUp')
    expect(screen.getByTestId('item-c')).toHaveFocus() // wrapped the other way
    key('ArrowUp')
    expect(screen.getByTestId('item-b')).toHaveFocus()
  })

  it('arrow keys skip disabled items', () => {
    render(<Harness items={entries({ b: { disabled: true } })} />)
    key('ArrowDown')
    expect(screen.getByTestId('item-c')).toHaveFocus()
    key('ArrowUp')
    expect(screen.getByTestId('item-a')).toHaveFocus()
  })

  it('Home / End go to the first / last ENABLED item', () => {
    render(<Harness items={entries({ a: { disabled: true } })} />)
    key('End')
    expect(screen.getByTestId('item-c')).toHaveFocus()
    key('Home')
    expect(screen.getByTestId('item-b')).toHaveFocus()
  })

  it('from the menu itself (nothing focused inside) ArrowDown lands on the first, ArrowUp on the last', () => {
    render(<Harness items={entries()} />)
    screen.getByTestId('menu').focus()
    key('ArrowUp')
    expect(screen.getByTestId('item-c')).toHaveFocus()
    screen.getByTestId('menu').focus()
    key('ArrowDown')
    expect(screen.getByTestId('item-a')).toHaveFocus()
  })

  it.each(['Enter', ' '])('%j activates the focused item and closes', (k) => {
    const spies = { a: vi.fn(), b: vi.fn(), c: vi.fn() }
    render(<Harness items={entries({}, spies)} />)
    key('ArrowDown')
    key(k)
    expect(spies.b).toHaveBeenCalledTimes(1)
    expect(spies.a).not.toHaveBeenCalled()
    expect(screen.queryByTestId('menu')).toBeNull()
  })

  it('Escape closes without activating', () => {
    const spies = { a: vi.fn(), b: vi.fn(), c: vi.fn() }
    const onCloseSpy = vi.fn()
    render(<Harness items={entries({}, spies)} onCloseSpy={onCloseSpy} />)
    key('Escape')
    expect(onCloseSpy).toHaveBeenCalledTimes(1)
    expect(spies.a).not.toHaveBeenCalled()
    expect(screen.queryByTestId('menu')).toBeNull()
  })

  it('an IME composition Escape is not ours', () => {
    render(<Harness items={entries()} />)
    fireEvent.keyDown(document.activeElement!, { key: 'Escape', isComposing: true })
    expect(screen.getByTestId('menu')).toBeInTheDocument()
  })

  it('Tab closes (and is not swallowed: the browser moves on from the trigger)', () => {
    render(<Harness items={entries()} />)
    const notPrevented = fireEvent.keyDown(document.activeElement!, { key: 'Tab' })
    expect(notPrevented).toBe(true)
    expect(screen.queryByTestId('menu')).toBeNull()
    expect(screen.getByTestId('trigger')).toHaveFocus()
  })

  it('navigation keys are swallowed (the page behind must not scroll)', () => {
    render(<Harness items={entries()} />)
    expect(fireEvent.keyDown(document.activeElement!, { key: 'ArrowDown' })).toBe(false)
    expect(fireEvent.keyDown(document.activeElement!, { key: ' ' })).toBe(false)
  })
})

describe('Menu — mouse', () => {
  it('clicking an item activates it and closes', () => {
    const spies = { a: vi.fn(), b: vi.fn(), c: vi.fn() }
    render(<Harness items={entries({}, spies)} />)
    fireEvent.click(screen.getByTestId('item-c'))
    expect(spies.c).toHaveBeenCalledTimes(1)
    expect(screen.queryByTestId('menu')).toBeNull()
  })

  it('a keepOpen item activates and leaves the menu open', () => {
    const spies = { a: vi.fn(), b: vi.fn(), c: vi.fn() }
    render(<Harness items={entries({ a: { keepOpen: true } }, spies)} />)
    fireEvent.click(screen.getByTestId('item-a'))
    expect(spies.a).toHaveBeenCalledTimes(1)
    expect(screen.getByTestId('menu')).toBeInTheDocument()
  })

  it('a disabled item cannot be activated — by click or by key — and does not close the menu', () => {
    const spies = { a: vi.fn(), b: vi.fn(), c: vi.fn() }
    render(<Harness items={entries({ b: { disabled: true } }, spies)} />)
    fireEvent.click(screen.getByTestId('item-b'))
    screen.getByTestId('item-b').focus()
    key('Enter')
    key(' ')
    expect(spies.b).not.toHaveBeenCalled()
    expect(screen.getByTestId('menu')).toBeInTheDocument()
  })

  it('a busy item cannot be activated again', () => {
    const spies = { a: vi.fn(), b: vi.fn(), c: vi.fn() }
    render(<Harness items={entries({ a: { busy: true } }, spies)} />)
    fireEvent.click(screen.getByTestId('item-a'))
    expect(spies.a).not.toHaveBeenCalled()
  })

  it('mousedown outside closes', () => {
    render(<Harness items={entries()} />)
    fireEvent.mouseDown(document.body)
    expect(screen.queryByTestId('menu')).toBeNull()
  })

  it('mousedown inside the menu, or on the trigger, does not (the trigger toggles by its own click)', () => {
    const onCloseSpy = vi.fn()
    render(<Harness items={entries()} onCloseSpy={onCloseSpy} />)
    fireEvent.mouseDown(screen.getByTestId('item-a'))
    fireEvent.mouseDown(screen.getByTestId('trigger'))
    expect(onCloseSpy).not.toHaveBeenCalled()
    expect(screen.getByTestId('menu')).toBeInTheDocument()
  })
})

describe('Menu — placement', () => {
  const rect = (left: number, top: number, width: number, height: number): DOMRect =>
    ({ left, top, width, height, right: left + width, bottom: top + height, x: left, y: top, toJSON: () => ({}) }) as DOMRect

  /** jsdom lays nothing out: hand the trigger and the menu their boxes. */
  function mockRects(trigger: DOMRect, menuSize: { width: number; height: number }) {
    vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(function (this: HTMLElement) {
      if (this.dataset.testid === 'trigger') return trigger
      if (this.dataset.testid === 'menu') return rect(0, 0, menuSize.width, menuSize.height)
      return rect(0, 0, 0, 0)
    })
  }

  it('bottom-start (default): under the trigger, left edges aligned', () => {
    mockRects(rect(100, 50, 80, 30), { width: 200, height: 120 })
    render(<Harness items={entries()} />)
    const menu = screen.getByTestId('menu')
    expect(menu.style.left).toBe('100px')
    expect(menu.style.top).toBe('84px')
  })

  it('right-start: beside the trigger, top edges aligned (the narrow bar sits at the far left)', () => {
    mockRects(rect(7, 8, 30, 30), { width: 200, height: 120 })
    render(<Harness items={entries()} placement="right-start" />)
    const menu = screen.getByTestId('menu')
    expect(menu.style.left).toBe('41px')
    expect(menu.style.top).toBe('8px')
  })

  it('stays inside the viewport: clamped on the right, flipped above when there is no room below', () => {
    const vw = window.innerWidth
    const vh = window.innerHeight
    mockRects(rect(vw - 50, vh - 40, 40, 30), { width: 200, height: 120 })
    render(<Harness items={entries()} />)
    const menu = screen.getByTestId('menu')
    expect(menu.style.left).toBe(`${vw - 200 - 4}px`)
    expect(menu.style.top).toBe(`${vh - 40 - 4 - 120}px`)
  })

  it('right-start flips to the left of the trigger when the right side has no room', () => {
    const vw = window.innerWidth
    mockRects(rect(vw - 40, 100, 30, 30), { width: 200, height: 120 })
    render(<Harness items={entries()} placement="right-start" />)
    expect(screen.getByTestId('menu').style.left).toBe(`${vw - 40 - 4 - 200}px`)
  })

  it('under Electron it never opens inside the title bar\'s drag region (it could not be clicked there)', () => {
    vi.mocked(getPlatformCapabilities).mockReturnValue(capabilities(true))
    mockRects(rect(7, 8, 30, 30), { width: 200, height: 120 })
    render(<Harness items={entries()} placement="right-start" />)
    expect(screen.getByTestId('menu').style.top).toBe('36px')
  })

  it('follows the trigger on resize', () => {
    let trigger = rect(100, 50, 80, 30)
    vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(function (this: HTMLElement) {
      if (this.dataset.testid === 'trigger') return trigger
      return rect(0, 0, 200, 120)
    })
    render(<Harness items={entries()} />)
    trigger = rect(300, 60, 80, 30)
    fireEvent(window, new Event('resize'))
    expect(screen.getByTestId('menu').style.left).toBe('300px')
    expect(screen.getByTestId('menu').style.top).toBe('94px')
  })
})

describe('Menu — entries changing while open', () => {
  const without = (id: string, items: MenuEntry[]) => items.filter((e) => 'divider' in e || e.id !== id)

  it('the focused item is removed → focus goes to the checked item, and the arrow keys still work', () => {
    const items = entries({ a: { checked: false }, b: { checked: false }, c: { checked: true } })
    const { rerender } = render(<Harness items={items} />)
    screen.getByTestId('item-b').focus()
    rerender(<Harness items={without('b', items)} />)
    expect(screen.getByTestId('item-c')).toHaveFocus()
    key('ArrowDown')
    expect(screen.getByTestId('item-a')).toHaveFocus()
  })

  it('no checked item → the first available one', () => {
    const items = entries({ a: { disabled: true } })
    const { rerender } = render(<Harness items={items} />)
    expect(screen.getByTestId('item-b')).toHaveFocus()
    rerender(<Harness items={without('b', items)} />)
    expect(screen.getByTestId('item-c')).toHaveFocus()
  })

  it('nothing available is left → the menu itself holds focus (Escape and Tab still reach it), once', () => {
    const items = entries({ b: { disabled: true }, c: { disabled: true } })
    const { rerender } = render(<Harness items={items} />)
    expect(screen.getByTestId('item-a')).toHaveFocus()
    const focusSpy = vi.spyOn(HTMLElement.prototype, 'focus')
    rerender(<Harness items={without('a', items)} />)
    expect(screen.getByTestId('menu')).toHaveFocus()
    expect(focusSpy).toHaveBeenCalledTimes(1)
    key('ArrowDown') // nowhere to go, and no throw
    expect(screen.getByTestId('menu')).toHaveFocus()
    key('Escape')
    expect(screen.queryByTestId('menu')).toBeNull()
  })

  it('an item that is still there keeps its focus — entries changing is not a reason to move it', () => {
    const items = entries({ a: { checked: true } })
    const { rerender } = render(<Harness items={items} />)
    screen.getByTestId('item-c').focus()
    rerender(<Harness items={entries({ a: { checked: true }, b: { hint: 'new' } })} />)
    expect(screen.getByTestId('item-c')).toHaveFocus()
  })

  it('focus the user moved elsewhere on purpose is not taken back', () => {
    const items = entries()
    const { rerender } = render(<Harness items={items} />)
    screen.getByTestId('elsewhere').focus()
    rerender(<Harness items={without('a', items)} />)
    expect(screen.getByTestId('elsewhere')).toHaveFocus()
  })
})

describe('Menu — Escape belongs to the topmost layer, and an open menu is it', () => {
  function Stacked({ onPanelClose }: { onPanelClose: () => void }) {
    const anchor = useRef<HTMLButtonElement>(null)
    return (
      <div>
        <button ref={anchor}>anchor</button>
        <FloatingPanel title="Under" anchorRef={anchor} onClose={onPanelClose} testId="panel">
          <Harness items={entries()} />
        </FloatingPanel>
      </div>
    )
  }

  it('over an open FloatingPanel: the first Escape closes the menu only, the second the panel', () => {
    const onPanelClose = vi.fn()
    render(<Stacked onPanelClose={onPanelClose} />)
    expect(screen.getByTestId('menu')).toBeInTheDocument()
    key('Escape')
    expect(screen.queryByTestId('menu')).toBeNull()
    expect(onPanelClose).not.toHaveBeenCalled()
    key('Escape')
    expect(onPanelClose).toHaveBeenCalledTimes(1)
  })

  it('no other document listener hears the Escape the menu took', () => {
    const other = vi.fn()
    document.addEventListener('keydown', other)
    render(<Harness items={entries()} />)
    key('Escape')
    document.removeEventListener('keydown', other)
    expect(other).not.toHaveBeenCalled()
  })

  it('an IME composition Escape is neither handled nor withheld from anyone else', () => {
    const other = vi.fn()
    document.addEventListener('keydown', other)
    render(<Harness items={entries()} />)
    const notPrevented = fireEvent.keyDown(document.activeElement!, { key: 'Escape', isComposing: true })
    document.removeEventListener('keydown', other)
    expect(screen.getByTestId('menu')).toBeInTheDocument()
    expect(other).toHaveBeenCalledTimes(1)
    expect(notPrevented).toBe(true)
  })

  it('other keys pass through to document listeners', () => {
    const other = vi.fn()
    document.addEventListener('keydown', other)
    render(<Harness items={entries()} />)
    key('a')
    document.removeEventListener('keydown', other)
    expect(other).toHaveBeenCalledTimes(1)
  })

  it('a closed menu listens to nothing: Escape reaches whoever is below (a terminal, an editor)', () => {
    const other = vi.fn()
    document.addEventListener('keydown', other)
    render(<Harness items={entries()} initiallyOpen={false} />)
    key('Escape')
    document.removeEventListener('keydown', other)
    expect(other).toHaveBeenCalledTimes(1)
  })
})

