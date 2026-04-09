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

  it('layout buttons are disabled until Plan 3', () => {
    render(<TitleBar title="test" />)
    const buttons = screen.getByTestId('layout-buttons').querySelectorAll('button')
    expect(buttons).toHaveLength(4)
    for (const btn of buttons) {
      expect(btn).toHaveProperty('disabled', true)
    }
  })

  it('renders with correct height', () => {
    const { container } = render(<TitleBar title="test" />)
    const bar = container.firstElementChild as HTMLElement
    expect(bar.getAttribute('style')).toContain('height: 30px')
  })
})
