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

/** jsdom normalises `#3b82f6` to `rgb(59, 130, 246)` inside color-mix(). */
function colorMix(pct: number): RegExp {
  return new RegExp(
    `color-mix\\(in srgb, (#3b82f6|rgb\\(59, 130, 246\\)) ${pct}%, transparent\\)`
  )
}

const base = {
  icon: 'Laptop',
  iconWeight: 'bold' as const,
  box: 16,
  inset: 2,
  radius: 4,
  lineColor: 'host' as const,
  lineOpacity: 100,
  bgOpacity: 22,
}

describe('HostBadge', () => {
  it('renders an aria-hidden inline-flex span that is not absolutely positioned', () => {
    render(<HostBadge {...base} color="#3b82f6" />)
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
    render(<HostBadge {...base} color="#3b82f6" radius={7} />)
    expect(screen.getByTestId('host-badge').style.borderRadius).toBe('7px')
  })

  describe('color === null', () => {
    it('renders no background and a muted icon color, with data-has-color false', () => {
      render(<HostBadge {...base} color={null} />)
      const el = screen.getByTestId('host-badge')
      expect(el.dataset.hasColor).toBe('false')
      expect(el.style.background).toBe('')
      expect(el.style.backgroundColor).toBe('')
      expect(el.style.color).toBe('var(--text-muted)')
    })

    it('still renders the host icon', () => {
      const { container } = render(<HostBadge {...base} color={null} />)
      expect(container.querySelector('svg path')?.getAttribute('d')).toBe('M Laptop bold')
    })
  })

  describe('color set', () => {
    it('tints the background with bgOpacity and sets data-has-color true', () => {
      render(<HostBadge {...base} color="#3b82f6" bgOpacity={22} />)
      const el = screen.getByTestId('host-badge')
      expect(el.dataset.hasColor).toBe('true')
      expect(el.style.background).toMatch(colorMix(22))
    })

    it('uses lineOpacity for the icon color when lineColor is host', () => {
      render(<HostBadge {...base} color="#3b82f6" lineColor="host" lineOpacity={60} />)
      expect(screen.getByTestId('host-badge').style.color).toMatch(colorMix(60))
    })

    it('uses the muted color for the icon when lineColor is neutral, keeping the tinted background', () => {
      render(
        <HostBadge {...base} color="#3b82f6" lineColor="neutral" lineOpacity={60} bgOpacity={30} />
      )
      const el = screen.getByTestId('host-badge')
      expect(el.style.color).toBe('var(--text-muted)')
      expect(el.style.background).toMatch(colorMix(30))
    })
  })

  describe('icon', () => {
    it('sizes the icon to box - inset*2', () => {
      const { container } = render(<HostBadge {...base} color="#3b82f6" box={24} inset={3} />)
      const svg = container.querySelector('svg')
      expect(svg?.getAttribute('width')).toBe('18')
      expect(svg?.getAttribute('height')).toBe('18')
    })

    it('handles the boundary case box=12 inset=5 as size 2', () => {
      const { container } = render(<HostBadge {...base} color="#3b82f6" box={12} inset={5} />)
      const svg = container.querySelector('svg')
      expect(svg?.getAttribute('width')).toBe('2')
      expect(svg?.getAttribute('height')).toBe('2')
    })

    it('falls back to DEFAULT_HOST_ICON when icon is undefined', () => {
      const { container } = render(<HostBadge {...base} color="#3b82f6" icon={undefined} />)
      expect(container.querySelector('svg path')?.getAttribute('d')).toBe(
        `M ${DEFAULT_HOST_ICON} bold`
      )
    })

    it("falls back to weight 'regular' when iconWeight is undefined", () => {
      const { container } = render(<HostBadge {...base} color="#3b82f6" iconWeight={undefined} />)
      expect(container.querySelector('svg path')?.getAttribute('d')).toBe('M Laptop regular')
    })

    it('lets the icon inherit the wrapper color via currentColor', () => {
      const { container } = render(<HostBadge {...base} color="#3b82f6" />)
      expect(container.querySelector('svg')?.getAttribute('fill')).toBe('currentColor')
    })
  })

  it('uses a custom testId when given', () => {
    render(<HostBadge {...base} color="#3b82f6" testId="inline-tab-host-badge" />)
    expect(screen.getByTestId('inline-tab-host-badge')).toBeTruthy()
    expect(screen.queryByTestId('host-badge')).toBeNull()
  })
})
