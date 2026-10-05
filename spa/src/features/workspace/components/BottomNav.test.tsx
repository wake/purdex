import { describe, it, expect, vi } from 'vitest'
import { createRef } from 'react'
import { render, screen, fireEvent, within } from '@testing-library/react'
import { BottomNav, type BottomNavProps } from './BottomNav'

function renderNav(over: Partial<BottomNavProps> = {}) {
  const props: BottomNavProps = {
    variant: 'wide',
    compact: false,
    workersOpen: false,
    onAddWorkspace: vi.fn(),
    onToggleWorkers: vi.fn(),
    onOpenHosts: vi.fn(),
    onOpenSettings: vi.fn(),
    onToggleCompact: vi.fn(),
    ...over,
  }
  render(<BottomNav {...props} />)
  return props
}

const nav = () => screen.getByTestId('bottom-nav')
const buttons = () => within(nav()).getAllByRole('button')
const titles = () => buttons().map((b) => b.getAttribute('title'))

describe('BottomNav — wide, rows', () => {
  it('renders the four entries in order, each a labelled row with a title, then the toggle', () => {
    renderNav()
    expect(nav()).toHaveAttribute('data-compact', 'false')
    expect(titles()).toEqual(['New workspace', 'Show as one row', 'Workers', 'Hosts', 'Settings'])
    for (const label of ['New workspace', 'Workers', 'Hosts', 'Settings']) {
      const btn = screen.getByRole('button', { name: label })
      expect(btn).toHaveTextContent(label)
      expect(btn).toHaveClass('flex', 'items-center', 'gap-2', 'px-2', 'py-1.5', 'rounded-md', 'text-sm')
    }
  })

  it('puts the compact toggle at the right end of the first row', () => {
    renderNav()
    const toggle = screen.getByTestId('bottom-nav-compact-toggle')
    expect(toggle).toHaveAttribute('title', 'Show as one row')
    const firstRow = screen.getByRole('button', { name: 'New workspace' }).parentElement!
    expect(toggle.parentElement).toBe(firstRow)
    expect(firstRow.lastElementChild).toBe(toggle)
  })

  it('each entry calls its own handler; the toggle does not trigger New workspace', () => {
    const p = renderNav()
    fireEvent.click(screen.getByTestId('bottom-nav-compact-toggle'))
    expect(p.onToggleCompact).toHaveBeenCalledTimes(1)
    expect(p.onAddWorkspace).not.toHaveBeenCalled()
    fireEvent.click(screen.getByRole('button', { name: 'New workspace' }))
    fireEvent.click(screen.getByRole('button', { name: 'Workers' }))
    fireEvent.click(screen.getByRole('button', { name: 'Hosts' }))
    fireEvent.click(screen.getByRole('button', { name: 'Settings' }))
    expect(p.onAddWorkspace).toHaveBeenCalledTimes(1)
    expect(p.onToggleWorkers).toHaveBeenCalledTimes(1)
    expect(p.onOpenHosts).toHaveBeenCalledTimes(1)
    expect(p.onOpenSettings).toHaveBeenCalledTimes(1)
  })

  it('Workers is a toggle: aria-pressed and the active style follow workersOpen', () => {
    renderNav({ workersOpen: true })
    const workers = screen.getByRole('button', { name: 'Workers' })
    expect(workers).toHaveAttribute('aria-pressed', 'true')
    expect(workers).toHaveClass('text-accent-base', 'bg-accent-base/10')
  })

  it('Workers is not pressed and not highlighted when closed', () => {
    renderNav({ workersOpen: false })
    const workers = screen.getByRole('button', { name: 'Workers' })
    expect(workers).toHaveAttribute('aria-pressed', 'false')
    expect(workers).not.toHaveClass('text-accent-base')
    expect(workers).not.toHaveClass('bg-accent-base/10')
  })
})

describe('BottomNav — wide, compact', () => {
  it('renders one row of 30×30 icon-only buttons with titles, the toggle back last', () => {
    renderNav({ compact: true })
    expect(nav()).toHaveAttribute('data-compact', 'true')
    expect(nav()).toHaveClass('flex', 'flex-row', 'items-center', 'justify-between', 'px-2')
    expect(titles()).toEqual(['New workspace', 'Workers', 'Hosts', 'Settings', 'Show as list'])
    for (const b of buttons()) {
      expect(b).toHaveClass('w-[30px]', 'h-[30px]')
      expect(b).toHaveTextContent('')
    }
    const toggle = screen.getByTestId('bottom-nav-compact-toggle')
    expect(buttons().at(-1)).toBe(toggle)
  })

  // Five 30px buttons need 150px plus padding; the wide bar can be 120px. jsdom has no layout, so the classes are the
  // contract: the buttons never shrink, and the row wraps onto a second line instead.
  it('keeps every compact button 30×30 at narrow widths: buttons do not shrink, the row wraps', () => {
    renderNav({ compact: true })
    expect(nav()).toHaveClass('flex-wrap', 'gap-1')
    for (const b of buttons()) expect(b).toHaveClass('shrink-0')
  })

  it('the toggle back calls onToggleCompact; Workers keeps aria-pressed', () => {
    const p = renderNav({ compact: true, workersOpen: true })
    fireEvent.click(screen.getByTestId('bottom-nav-compact-toggle'))
    expect(p.onToggleCompact).toHaveBeenCalledTimes(1)
    const workers = screen.getByRole('button', { name: 'Workers' })
    expect(workers).toHaveAttribute('aria-pressed', 'true')
    expect(workers).toHaveClass('text-accent-base', 'bg-accent-base/10')
  })
})

describe('BottomNav — narrow', () => {
  it('renders a column of four 30×30 icon buttons including Workers, with no compact toggle', () => {
    renderNav({ variant: 'narrow', compact: true })
    expect(nav()).toHaveAttribute('data-compact', 'false')
    expect(nav()).toHaveClass('flex', 'flex-col', 'items-center')
    expect(titles()).toEqual(['New workspace', 'Workers', 'Hosts', 'Settings'])
    for (const b of buttons()) {
      expect(b).toHaveClass('w-[30px]', 'h-[30px]')
      expect(b).toHaveTextContent('')
    }
    expect(screen.queryByTestId('bottom-nav-compact-toggle')).toBeNull()
  })

  it('Workers aria-pressed follows workersOpen', () => {
    renderNav({ variant: 'narrow', workersOpen: true })
    expect(screen.getByRole('button', { name: 'Workers' })).toHaveAttribute('aria-pressed', 'true')
  })
})

// Shell polish spec §4 (rule F): a mouse press on a bottom button must not move focus off the pane. jsdom does not
// focus on mousedown, so `fireEvent.mouseDown(...) === false` proves the button is wired to `keepFocus`; that the
// helper keeps focus is proven in a real browser (spec §5).
describe('BottomNav — mouse press keeps focus where it was', () => {
  const variants = [
    ['wide rows', { variant: 'wide', compact: false }, ['New workspace', 'Show as one row', 'Workers', 'Hosts', 'Settings']],
    ['wide compact', { variant: 'wide', compact: true }, ['New workspace', 'Workers', 'Hosts', 'Settings', 'Show as list']],
    ['narrow', { variant: 'narrow', compact: false }, ['New workspace', 'Workers', 'Hosts', 'Settings']],
  ] as const

  it.each(variants)('every button prevents the mousedown default (%s)', (_name, over, expected) => {
    renderNav(over)
    expect(titles()).toEqual(expected)
    for (const b of buttons()) expect(fireEvent.mouseDown(b), b.getAttribute('title')!).toBe(false)
  })

  it.each(variants)('every button stays in the tab order (%s)', (_name, over) => {
    renderNav(over)
    for (const b of buttons()) expect(b.tabIndex, b.getAttribute('title')!).toBeGreaterThanOrEqual(0)
  })

  it.each(variants)('a press then a click still runs each handler once (%s)', (_name, over) => {
    const p = renderNav(over)
    const press = (b: HTMLElement) => {
      fireEvent.mouseDown(b)
      fireEvent.click(b)
    }
    for (const label of ['New workspace', 'Workers', 'Hosts', 'Settings']) press(screen.getByTitle(label))
    expect(p.onAddWorkspace).toHaveBeenCalledTimes(1)
    expect(p.onToggleWorkers).toHaveBeenCalledTimes(1)
    expect(p.onOpenHosts).toHaveBeenCalledTimes(1)
    expect(p.onOpenSettings).toHaveBeenCalledTimes(1)
    const toggle = screen.queryByTestId('bottom-nav-compact-toggle')
    if (toggle) {
      press(toggle)
      expect(p.onToggleCompact).toHaveBeenCalledTimes(1)
    } else {
      expect(over.variant).toBe('narrow')
    }
  })
})

describe('BottomNav — workersRef', () => {
  it.each([
    ['wide rows', { variant: 'wide', compact: false }],
    ['wide compact', { variant: 'wide', compact: true }],
    ['narrow', { variant: 'narrow', compact: false }],
  ] as const)('points at the Workers button (%s)', (_name, over) => {
    const workersRef = createRef<HTMLButtonElement>()
    renderNav({ ...over, workersRef })
    expect(workersRef.current).toBe(screen.getByRole('button', { name: 'Workers' }))
  })
})
