import { describe, it, expect, afterEach, vi } from 'vitest'
import { render, screen, cleanup } from '@testing-library/react'

// WorkspaceIcon only renders an <svg> once the Phosphor weight is loaded, which never
// happens in jsdom (it fetches `/icons/<weight>.json`). Stub the cache so every name
// resolves, encoding name + weight into the path data so the tests can assert which
// icon/weight HostBadge handed down.
vi.mock('../features/workspace/lib/icon-path-cache', () => ({
  getIconPath: (name: string, weight: string) => `M ${name} ${weight}`,
  isWeightLoaded: () => true,
  prefetchWeight: () => Promise.resolve(),
}))

import { HostBadge } from './HostBadge'
import { DEFAULT_HOST_ICON } from '../lib/host-color'

afterEach(cleanup)

const COLORS = {
  main: 'rgba(59, 130, 246, 1)',
  middle: 'rgba(59, 130, 246, 0.6)',
  light: 'rgba(59, 130, 246, 0.22)',
}

const base = {
  colors: COLORS,
  icon: undefined,
  iconWeight: undefined,
  box: 16,
  inset: 2,
  radius: 4,
  lineColor: 'host' as const,
}

describe('HostBadge', () => {
  it('renders an aria-hidden inline-flex span that is not absolutely positioned', () => {
    render(<HostBadge {...base} />)
    const el = screen.getByTestId('host-badge')
    expect(el.tagName).toBe('SPAN')
    expect(el.getAttribute('aria-hidden')).toBe('true')
    expect(el.style.display).toBe('inline-flex')
    expect(el.style.alignItems).toBe('center')
    expect(el.style.justifyContent).toBe('center')
    expect(el.style.flexShrink).toBe('0')
    expect(el.style.position).not.toBe('absolute')
    expect(el.style.width).toBe('16px')
    expect(el.style.height).toBe('16px')
  })

  it('applies the radius in px', () => {
    render(<HostBadge {...base} radius={7} />)
    expect(screen.getByTestId('host-badge').style.borderRadius).toBe('7px')
  })

  describe('colors === null', () => {
    it('renders no background, a muted icon color, no custom properties, data-has-color false', () => {
      render(<HostBadge {...base} colors={null} />)
      const el = screen.getByTestId('host-badge')
      expect(el.dataset.hasColor).toBe('false')
      expect(el.style.background).toBe('')
      expect(el.style.color).toBe('var(--text-muted)')
      expect(el.style.getPropertyValue('--hb-main')).toBe('')
      expect(el.style.getPropertyValue('--hb-middle')).toBe('')
    })

    it('still renders the host icon', () => {
      render(<HostBadge {...base} colors={null} />)
      expect(screen.getByTestId('host-badge').querySelector('svg')).not.toBeNull()
    })
  })

  describe('colors set', () => {
    it('paints light as background, exposes main/middle as custom properties, icon falls back to middle', () => {
      render(<HostBadge {...base} />)
      const el = screen.getByTestId('host-badge')
      expect(el.dataset.hasColor).toBe('true')
      expect(el).toHaveAttribute('data-host-badge')
      expect(el.style.background).toBe(COLORS.light)
      expect(el.style.getPropertyValue('--hb-main')).toBe(COLORS.main)
      expect(el.style.getPropertyValue('--hb-middle')).toBe(COLORS.middle)
      expect(el.style.color).toBe('var(--hb-icon, var(--hb-middle))')
    })

    it('uses the muted color for the icon when lineColor is neutral, keeping the tinted background', () => {
      render(<HostBadge {...base} lineColor="neutral" />)
      const el = screen.getByTestId('host-badge')
      expect(el.style.color).toBe('var(--text-muted)')
      expect(el.style.background).toBe(COLORS.light)
      expect(el.style.getPropertyValue('--hb-main')).toBe('')
    })
  })

  describe('icon', () => {
    it('sizes the icon to box - inset*2', () => {
      const { container } = render(<HostBadge {...base} box={24} inset={3} />)
      const svg = container.querySelector('svg')
      expect(svg?.getAttribute('width')).toBe('18')
      expect(svg?.getAttribute('height')).toBe('18')
    })

    it('handles the boundary case box=12 inset=5 as size 2', () => {
      const { container } = render(<HostBadge {...base} box={12} inset={5} />)
      const svg = container.querySelector('svg')
      expect(svg?.getAttribute('width')).toBe('2')
      expect(svg?.getAttribute('height')).toBe('2')
    })

    it('falls back to DEFAULT_HOST_ICON when icon is undefined', () => {
      const { container } = render(<HostBadge {...base} icon={undefined} />)
      expect(container.querySelector('svg path')?.getAttribute('d')).toBe(
        `M ${DEFAULT_HOST_ICON} regular`
      )
    })

    it("falls back to weight 'regular' when iconWeight is undefined", () => {
      const { container } = render(<HostBadge {...base} icon="Laptop" iconWeight={undefined} />)
      expect(container.querySelector('svg path')?.getAttribute('d')).toBe('M Laptop regular')
    })

    it('lets the icon inherit the wrapper color via currentColor', () => {
      const { container } = render(<HostBadge {...base} />)
      expect(container.querySelector('svg')?.getAttribute('fill')).toBe('currentColor')
    })
  })

  it('uses a custom testId when given', () => {
    render(<HostBadge {...base} testId="inline-tab-host-badge" />)
    expect(screen.getByTestId('inline-tab-host-badge')).toBeTruthy()
    expect(screen.queryByTestId('host-badge')).toBeNull()
  })
})
