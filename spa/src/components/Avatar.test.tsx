import { describe, it, expect } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { Avatar } from './Avatar'

describe('Avatar', () => {
  it('draws the stored image at the requested size', () => {
    render(<Avatar size={24} code="hiro" name="Hiro" src="data:image/png;base64,AA==" fallbackColor="#336699" />)
    const img = screen.getByRole('img', { name: 'Hiro' })
    expect(img.tagName).toBe('IMG')
    expect(img).toHaveAttribute('src', 'data:image/png;base64,AA==')
    expect(img).toHaveStyle({ width: '24px', height: '24px' })
  })

  it('falls back to the initial on the host colour when the image is empty', () => {
    render(<Avatar size={44} code="hiro" name="Hiro" src="" fallbackColor="#336699" />)
    const el = screen.getByRole('img', { name: 'Hiro' })
    expect(el).toHaveTextContent('H')
    expect(el).toHaveStyle({ backgroundColor: '#336699' })
  })

  it('falls back to the initial when the image fails to load', () => {
    render(<Avatar size={16} code="hiro" name="Hiro" src="data:image/png;base64,broken" fallbackColor="#336699" />)
    fireEvent.error(screen.getByRole('img', { name: 'Hiro' }))
    expect(screen.getByRole('img', { name: 'Hiro' })).toHaveTextContent('H')
  })

  it('uses the code when there is no name', () => {
    render(<Avatar size={18} code="olga" fallbackColor="#336699" />)
    expect(screen.getByRole('img', { name: 'olga' })).toHaveTextContent('O')
  })

  it('decorative: hidden from assistive technology, picture or initials, so the name beside it is said once', () => {
    const { container, rerender } = render(<Avatar size={16} code="ann" name="Ann" src="data:image/png;base64,AA==" fallbackColor="#336699" decorative />)
    expect(screen.queryByRole('img')).toBeNull()
    expect(container.querySelector('img')).toHaveAttribute('aria-hidden', 'true')
    expect(container.querySelector('img')).toHaveAttribute('alt', '')
    rerender(<Avatar size={16} code="ann" name="Ann" fallbackColor="#336699" decorative />)
    expect(screen.queryByRole('img')).toBeNull()
    expect(container.firstElementChild).toHaveAttribute('aria-hidden', 'true')
    expect(container.firstElementChild).not.toHaveAttribute('aria-label')
  })
})
