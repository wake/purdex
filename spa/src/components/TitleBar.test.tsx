import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { TitleBar } from './TitleBar'

describe('TitleBar', () => {
  it('renders the title text', () => {
    render(<TitleBar title="tmux-box — tbox2" />)
    expect(screen.getByText('tmux-box — tbox2')).toBeDefined()
  })

  it('renders layout pattern buttons', () => {
    render(<TitleBar title="test" />)
    expect(screen.getByTestId('layout-buttons')).toBeDefined()
  })

  it('applies drag region styling', () => {
    const { container } = render(<TitleBar title="test" />)
    const bar = container.firstElementChild as HTMLElement
    // Check the style attribute contains drag region
    expect(bar.getAttribute('style')).toContain('app-region')
  })
})
