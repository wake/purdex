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
