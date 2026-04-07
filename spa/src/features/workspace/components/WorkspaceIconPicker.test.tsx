import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'

vi.mock('../generated/icon-loader', () => ({
  ALL_ICON_NAMES: ['House', 'Star', 'Heart', 'Rocket', 'Terminal'],
  iconLoaders: {
    House: () => new Promise(() => {}),
    Star: () => new Promise(() => {}),
    Heart: () => new Promise(() => {}),
    Rocket: () => new Promise(() => {}),
    Terminal: () => new Promise(() => {}),
  },
}))

import { WorkspaceIconPicker } from './WorkspaceIconPicker'
import { CURATED_ICON_CATEGORIES } from '../constants'

const firstCategory = Object.keys(CURATED_ICON_CATEGORIES)[0]
const firstCategoryIcons = CURATED_ICON_CATEGORIES[firstCategory]

describe('WorkspaceIconPicker', () => {
  beforeEach(() => { cleanup() })

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

  it('filters icons by search text', () => {
    render(<WorkspaceIconPicker currentIcon={undefined} onSelect={vi.fn()} onCancel={vi.fn()} />)
    const search = screen.getByPlaceholderText(/search/i)
    fireEvent.change(search, { target: { value: 'House' } })
    const buttons = screen.getAllByRole('button').filter((b) => b.getAttribute('data-icon'))
    expect(buttons.length).toBeGreaterThanOrEqual(1)
    expect(buttons.some((b) => b.getAttribute('data-icon') === 'House')).toBe(true)
  })

  it('renders clear button and calls onSelect with empty string', () => {
    const onSelect = vi.fn()
    render(<WorkspaceIconPicker currentIcon="Star" onSelect={onSelect} onCancel={vi.fn()} />)
    const clearBtn = screen.getByTestId('clear-icon')
    fireEvent.click(clearBtn)
    expect(onSelect).toHaveBeenCalledWith('')
  })
})
