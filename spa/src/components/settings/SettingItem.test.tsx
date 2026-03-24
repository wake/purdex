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

  it('applies disabled styling', () => {
    const { container } = render(
      <SettingItem label="X" disabled>
        <span />
      </SettingItem>,
    )
    const wrapper = container.firstChild as HTMLElement
    expect(wrapper.className).toContain('opacity-50')
  })
})
