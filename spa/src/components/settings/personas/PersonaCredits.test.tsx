import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { PersonaCredits } from './PersonaCredits'
import { AVATAR_CREDITS, creditsRequiringAttribution } from '../../../lib/names/persona-credits'

describe('PersonaCredits', () => {
  it('lists every avatar style with its artist and licence, each as a link', () => {
    render(<PersonaCredits />)
    for (const c of AVATAR_CREDITS) {
      const row = screen.getByTestId(`persona-credit-${c.style}`)
      expect(row.textContent).toContain(c.label)
      expect(row.textContent).toContain(c.artist)
      expect(row.textContent).toContain(c.license)
      expect(Array.from(row.querySelectorAll('a')).map((a) => a.getAttribute('href'))).toEqual([c.artistUrl, c.licenseUrl])
    }
  })

  it('credits the style that requires it (CC BY), and opens links without leaking the opener', () => {
    render(<PersonaCredits />)
    for (const c of creditsRequiringAttribution()) {
      expect(screen.getByTestId(`persona-credit-${c.style}`).textContent).toContain('CC BY 4.0')
    }
    for (const a of screen.getAllByRole('link')) {
      expect(a.getAttribute('target')).toBe('_blank')
      expect(a.getAttribute('rel')).toContain('noopener')
    }
  })
})
