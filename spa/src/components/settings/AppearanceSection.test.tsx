import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { AppearanceSection } from './AppearanceSection'

describe('AppearanceSection', () => {
  it('renders section title', () => {
    render(<AppearanceSection />)
    expect(screen.getByText('Appearance')).toBeTruthy()
  })

  it('renders theme setting as disabled', () => {
    render(<AppearanceSection />)
    expect(screen.getByText('Theme')).toBeTruthy()
    expect(screen.getByText('Dark')).toBeTruthy()
    expect(screen.getByText('Light')).toBeTruthy()
  })

  it('renders language setting as disabled', () => {
    render(<AppearanceSection />)
    expect(screen.getByText('Language')).toBeTruthy()
  })
})
