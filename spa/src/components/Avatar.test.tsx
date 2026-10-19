import { describe, it, expect } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { Avatar } from './Avatar'
import { personaColor } from '../lib/names/persona-color'

describe('Avatar', () => {
  it('draws the stored image at the requested size', () => {
    render(<Avatar size={24} code="hiro" name="Hiro" src="data:image/png;base64,AA==" />)
    const img = screen.getByRole('img', { name: 'Hiro' })
    expect(img.tagName).toBe('IMG')
    expect(img).toHaveAttribute('src', 'data:image/png;base64,AA==')
    expect(img).toHaveStyle({ width: '24px', height: '24px' })
  })

  // The persona drawings are transparent: without a disc behind them the circle (and the face on a dark theme) is lost.
  it('puts a round #dfe4ea backing under the picture, whatever its size', () => {
    for (const size of [18, 44] as const) {
      const { unmount } = render(<Avatar size={size} code="hiro" name="Hiro" src="data:image/png;base64,AA==" />)
      const img = screen.getByRole('img', { name: 'Hiro' })
      expect(img).toHaveStyle({ backgroundColor: '#dfe4ea' })
      expect(img.className).toContain('rounded-full')
      unmount()
    }
  })

  it('a persona with no picture: its own colour (personaColor), the initial in white — the same wherever it is drawn', () => {
    const { rerender } = render(<Avatar size={44} code="hiro" name="Hiro" />)
    const el = screen.getByRole('img', { name: 'Hiro' })
    expect(el).toHaveTextContent('H')
    expect(el).toHaveStyle({ backgroundColor: personaColor('hiro') })
    expect(el.className).toContain('text-white')
    rerender(<Avatar size={18} code="hiro" name="Hiro" src="" />)
    expect(screen.getByRole('img', { name: 'Hiro' })).toHaveStyle({ backgroundColor: personaColor('hiro') })
  })

  it('no persona (empty code): a neutral disc on the theme surface, the initial in text-secondary, a subtle ring — no colour of its own', () => {
    render(<Avatar size={44} code="" name="Marco" />)
    const el = screen.getByRole('img', { name: 'Marco' })
    expect(el).toHaveTextContent('M')
    expect(el.style.backgroundColor).toBe('')
    expect(el.className).toContain('bg-surface-secondary')
    expect(el.className).toContain('text-text-secondary')
    expect(el.className).toContain('ring-border-subtle')
    expect(el.className).not.toContain('text-white')
  })

  it('a picture that fails to load falls back to the persona colour', () => {
    render(<Avatar size={16} code="hiro" name="Hiro" src="data:image/png;base64,broken" />)
    fireEvent.error(screen.getByRole('img', { name: 'Hiro' }))
    expect(screen.getByRole('img', { name: 'Hiro' })).toHaveStyle({ backgroundColor: personaColor('hiro') })
  })

  it('falls back to the initial when the image fails to load', () => {
    render(<Avatar size={16} code="hiro" name="Hiro" src="data:image/png;base64,broken" />)
    fireEvent.error(screen.getByRole('img', { name: 'Hiro' }))
    expect(screen.getByRole('img', { name: 'Hiro' })).toHaveTextContent('H')
  })

  it('uses the code when there is no name', () => {
    render(<Avatar size={18} code="olga" />)
    expect(screen.getByRole('img', { name: 'olga' })).toHaveTextContent('O')
  })

  it('decorative: hidden from assistive technology, picture or initials, so the name beside it is said once', () => {
    const { container, rerender } = render(<Avatar size={16} code="ann" name="Ann" src="data:image/png;base64,AA==" decorative />)
    expect(screen.queryByRole('img')).toBeNull()
    expect(container.querySelector('img')).toHaveAttribute('aria-hidden', 'true')
    expect(container.querySelector('img')).toHaveAttribute('alt', '')
    rerender(<Avatar size={16} code="ann" name="Ann" decorative />)
    expect(screen.queryByRole('img')).toBeNull()
    expect(container.firstElementChild).toHaveAttribute('aria-hidden', 'true')
    expect(container.firstElementChild).not.toHaveAttribute('aria-label')
  })
})
