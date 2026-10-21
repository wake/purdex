import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { SettingItem } from './SettingItem'

describe('SettingItem', () => {
  it('renders label and children', () => {
    render(
      <SettingItem label="My Setting">
        <input data-testid="ctrl" />
      </SettingItem>,
    )
    expect(screen.getByText('My Setting')).toBeTruthy()
    expect(screen.getByTestId('ctrl')).toBeTruthy()
  })

  it('renders description when provided', () => {
    render(
      <SettingItem label="X" description="Some help text">
        <span />
      </SettingItem>,
    )
    expect(screen.getByText('Some help text')).toBeTruthy()
  })

  it('applies disabled styling only to children', () => {
    const { container } = render(
      <SettingItem label="X" disabled>
        <span data-testid="ctrl" />
      </SettingItem>,
    )
    const wrapper = container.firstChild as HTMLElement
    // Wrapper gets pointer-events-none but NOT opacity-50
    expect(wrapper.className).toContain('pointer-events-none')
    expect(wrapper.className).not.toContain('opacity-50')
    // Children wrapper gets opacity-50
    const childrenWrapper = screen.getByTestId('ctrl').parentElement!
    expect(childrenWrapper.className).toContain('opacity-50')
  })

  it('keeps the side-by-side layout by default', () => {
    const { container } = render(<SettingItem label="X"><span data-testid="ctrl" /></SettingItem>)
    expect((container.firstChild as HTMLElement).getAttribute('data-layout')).toBeNull()
    expect(screen.getByTestId('ctrl').parentElement!.className).toContain('flex-shrink-0')
  })

  it('stacked puts children under the label at full width, without flex-shrink-0', () => {
    const { container } = render(<SettingItem label="X" stacked><span data-testid="ctrl" /></SettingItem>)
    expect((container.firstChild as HTMLElement).getAttribute('data-layout')).toBe('stacked')
    const cls = screen.getByTestId('ctrl').parentElement!.className
    expect(cls).not.toContain('flex-shrink-0')
    expect(cls).toContain('w-full')
  })
})
