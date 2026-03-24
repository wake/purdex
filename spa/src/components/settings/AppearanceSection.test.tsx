import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { AppearanceSection } from './AppearanceSection'

describe('AppearanceSection', () => {
  it('renders section title', () => {
    render(<AppearanceSection />)
    expect(screen.getByText('Appearance')).toBeTruthy()
  })

  it('renders theme setting with disabled controls', () => {
    render(<AppearanceSection />)
    expect(screen.getByText('Theme')).toBeTruthy()
    expect(screen.getByText('Dark')).toBeTruthy()
    expect(screen.getByText('Light')).toBeTruthy()
    // The SettingItem wrapper (outermost div containing Theme) should have pointer-events-none
    const label = screen.getByText('Theme')
    // Walk up: label span → label div → SettingItem wrapper
    const wrapper = label.parentElement!.parentElement!
    expect(wrapper.className).toContain('pointer-events-none')
  })

  it('renders language setting with disabled select', () => {
    render(<AppearanceSection />)
    expect(screen.getByText('Language')).toBeTruthy()
    const selectEl = screen.getByRole('combobox')
    expect(selectEl).toHaveProperty('disabled', true)
  })
})
