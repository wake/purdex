import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, cleanup } from '@testing-library/react'

vi.mock('../lib/icon-path-cache', () => ({
  getIconPath: (name: string) => {
    if (name === 'Rocket') return 'M0,0L10,10Z'
    if (name === 'Acorn') return [{ d: 'M0,0', o: 0.2 }, 'M5,5L20,20']
    return null
  },
  isWeightLoaded: () => true,
  prefetchWeight: () => Promise.resolve(),
}))

import { WorkspaceIcon } from './WorkspaceIcon'

describe('WorkspaceIcon', () => {
  beforeEach(() => cleanup())

  it('shows first char of name when icon is undefined', () => {
    render(<WorkspaceIcon icon={undefined} name="Default" size={18} />)
    expect(screen.getByText('D')).toBeInTheDocument()
  })

  it('shows emoji icon as text', () => {
    render(<WorkspaceIcon icon="🚀" name="Test" size={18} />)
    expect(screen.getByText('🚀')).toBeInTheDocument()
  })

  it('renders SVG for valid Phosphor icon name', () => {
    const { container } = render(<WorkspaceIcon icon="Rocket" name="Test" size={18} />)
    const svg = container.querySelector('svg')
    expect(svg).toBeInTheDocument()
    expect(svg?.getAttribute('viewBox')).toBe('0 0 256 256')
    expect(svg?.getAttribute('width')).toBe('18')
    const path = svg?.querySelector('path')
    expect(path?.getAttribute('d')).toBe('M0,0L10,10Z')
  })

  it('renders duotone SVG with multiple paths and opacity', () => {
    const { container } = render(<WorkspaceIcon icon="Acorn" name="Test" size={18} weight="duotone" />)
    const paths = container.querySelectorAll('path')
    expect(paths).toHaveLength(2)
    expect(paths[0].getAttribute('opacity')).toBe('0.2')
    expect(paths[1].getAttribute('d')).toBe('M5,5L20,20')
  })

  it('shows fallback when icon name has no path data', () => {
    render(<WorkspaceIcon icon="NonExistent" name="Foo" size={18} />)
    expect(screen.getByText('F')).toBeInTheDocument()
  })

  it('shows single-char icon as text (legacy)', () => {
    render(<WorkspaceIcon icon="X" name="Test" size={18} />)
    expect(screen.getByText('X')).toBeInTheDocument()
  })
})
