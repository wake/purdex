import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'

// jsdom has no layout engine, so useVirtualizer returns 0 items.
// Mock it to render all rows without virtualization.
vi.mock('@tanstack/react-virtual', () => ({
  useVirtualizer: ({ count }: { count: number }) => ({
    getTotalSize: () => count * 38,
    getVirtualItems: () =>
      Array.from({ length: count }, (_, i) => ({
        index: i,
        key: i,
        start: i * 38,
        size: 38,
      })),
  }),
}))

vi.mock('../lib/icon-path-cache', () => ({
  getIconPath: () => 'M0,0L10,10',
  isWeightLoaded: () => true,
  prefetchWeight: () => Promise.resolve(),
}))

vi.mock('../generated/icon-meta.json', () => ({
  default: [
    { n: 'House', t: ['home', 'building'], c: ['general'] },
    { n: 'Star', t: ['favorite', 'rating'], c: ['general'] },
    { n: 'Heart', t: ['love', 'like'], c: ['general'] },
    { n: 'Envelope', t: ['mail', 'email', 'message'], c: ['communication'] },
    { n: 'Terminal', t: ['console', 'cli', 'command'], c: ['development'] },
  ],
}))

import { WorkspaceIconPicker } from './WorkspaceIconPicker'
import { CURATED_ICON_CATEGORIES } from '../constants'

const firstCategory = Object.keys(CURATED_ICON_CATEGORIES)[0]
const firstCategoryIcons = CURATED_ICON_CATEGORIES[firstCategory]

describe('WorkspaceIconPicker', () => {
  beforeEach(() => cleanup())

  it('renders category tabs', () => {
    render(<WorkspaceIconPicker currentIcon={undefined} onSelect={vi.fn()} onCancel={vi.fn()} />)
    for (const cat of Object.keys(CURATED_ICON_CATEGORIES)) {
      expect(screen.getByTestId(`category-${cat}`)).toBeInTheDocument()
    }
  })

  it('renders icon grid for default category', () => {
    render(<WorkspaceIconPicker currentIcon={undefined} onSelect={vi.fn()} onCancel={vi.fn()} />)
    const buttons = screen.getAllByRole('button').filter((b) => b.getAttribute('data-icon'))
    expect(buttons.length).toBe(firstCategoryIcons.length)
  })

  it('calls onSelect with icon name', () => {
    const onSelect = vi.fn()
    render(<WorkspaceIconPicker currentIcon={undefined} onSelect={onSelect} onCancel={vi.fn()} />)
    const buttons = screen.getAllByRole('button').filter((b) => b.getAttribute('data-icon'))
    fireEvent.click(buttons[0])
    expect(onSelect).toHaveBeenCalledWith(firstCategoryIcons[0])
  })

  it('searches by tag (fuzzy) — "mail" finds Envelope', () => {
    render(<WorkspaceIconPicker currentIcon={undefined} onSelect={vi.fn()} onCancel={vi.fn()} />)
    const search = screen.getByPlaceholderText(/search/i)
    fireEvent.change(search, { target: { value: 'mail' } })
    const buttons = screen.getAllByRole('button').filter((b) => b.getAttribute('data-icon'))
    expect(buttons.some((b) => b.getAttribute('data-icon') === 'Envelope')).toBe(true)
  })

  it('searches by name — "term" finds Terminal', () => {
    render(<WorkspaceIconPicker currentIcon={undefined} onSelect={vi.fn()} onCancel={vi.fn()} />)
    const search = screen.getByPlaceholderText(/search/i)
    fireEvent.change(search, { target: { value: 'term' } })
    const buttons = screen.getAllByRole('button').filter((b) => b.getAttribute('data-icon'))
    expect(buttons.some((b) => b.getAttribute('data-icon') === 'Terminal')).toBe(true)
  })

  it('clears icon selection', () => {
    const onSelect = vi.fn()
    render(<WorkspaceIconPicker currentIcon="Star" onSelect={onSelect} onCancel={vi.fn()} />)
    fireEvent.click(screen.getByTestId('clear-icon'))
    expect(onSelect).toHaveBeenCalledWith('')
  })

  it('renders weight toggle buttons', () => {
    render(<WorkspaceIconPicker currentIcon={undefined} onSelect={vi.fn()} onCancel={vi.fn()} />)
    for (const w of ['bold', 'regular', 'thin', 'light', 'fill', 'duotone']) {
      expect(screen.getByTestId(`weight-${w}`)).toBeInTheDocument()
    }
  })

  it('defaults weight to currentWeight prop', () => {
    render(<WorkspaceIconPicker currentIcon={undefined} onSelect={vi.fn()} onCancel={vi.fn()} currentWeight="thin" />)
    const thinBtn = screen.getByTestId('weight-thin')
    expect(thinBtn.className).toContain('bg-accent/20')
  })

  it('switches weight on click', () => {
    render(<WorkspaceIconPicker currentIcon={undefined} onSelect={vi.fn()} onCancel={vi.fn()} />)
    const regularBtn = screen.getByTestId('weight-regular')
    fireEvent.click(regularBtn)
    expect(regularBtn.className).toContain('bg-accent/20')
    const boldBtn = screen.getByTestId('weight-bold')
    expect(boldBtn.className).not.toContain('bg-accent/20')
  })

  it('calls onWeightChange when weight is switched', () => {
    const onWeightChange = vi.fn()
    render(<WorkspaceIconPicker currentIcon={undefined} onSelect={vi.fn()} onCancel={vi.fn()} onWeightChange={onWeightChange} />)
    fireEvent.click(screen.getByTestId('weight-regular'))
    expect(onWeightChange).toHaveBeenCalledWith('regular')
    fireEvent.click(screen.getByTestId('weight-duotone'))
    expect(onWeightChange).toHaveBeenCalledWith('duotone')
  })

  it('shows empty state when search has no results', () => {
    render(<WorkspaceIconPicker currentIcon={undefined} onSelect={vi.fn()} onCancel={vi.fn()} />)
    const search = screen.getByPlaceholderText(/search/i)
    fireEvent.change(search, { target: { value: 'xyznonexistent' } })
    expect(screen.getByText('No results found')).toBeInTheDocument()
  })
})
