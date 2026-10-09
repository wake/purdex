import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, render, screen } from '@testing-library/react'
import { QrCode } from './QrCode'

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

function pathOf(container: HTMLElement): string {
  return container.querySelector('path')?.getAttribute('d') ?? ''
}

describe('QrCode', () => {
  it('renders an svg with one path of dark modules and a light background', () => {
    const { container } = render(<QrCode value="purdex://pair?v=1&relay=x&code=ABCD2345" label="pairing QR" />)
    const svg = container.querySelector('svg')!
    expect(svg).toBeTruthy()
    expect(svg.getAttribute('role')).toBe('img')
    expect(svg.getAttribute('aria-label')).toBe('pairing QR')
    expect(svg.getAttribute('shape-rendering')).toBe('crispEdges')
    expect(container.querySelectorAll('path')).toHaveLength(1)
    expect(pathOf(container).length).toBeGreaterThan(10)
    const rect = container.querySelector('rect')!
    expect(rect.getAttribute('fill')).toBe('#ffffff')
    expect(container.querySelector('path')!.getAttribute('fill')).toBe('#000000')
  })

  it('keeps a quiet zone of 4 modules around the matrix', () => {
    const { container } = render(<QrCode value="abc" />)
    const [, , w] = (container.querySelector('svg')!.getAttribute('viewBox') ?? '').split(' ').map(Number).slice(0, 3)
    // version 1 is 21 modules; the viewBox adds 4 on each side
    expect(w).toBe(21 + 8)
    // no dark module may start inside the margin
    const starts = [...pathOf(container).matchAll(/M(\d+) (\d+)/g)].map((m) => [Number(m[1]), Number(m[2])])
    expect(starts.length).toBeGreaterThan(0)
    for (const [x, y] of starts) {
      expect(x).toBeGreaterThanOrEqual(4)
      expect(y).toBeGreaterThanOrEqual(4)
    }
  })

  it('different values give different paths; the same value gives identical output', () => {
    const a = render(<QrCode value="alpha" />)
    const b = render(<QrCode value="bravo" />)
    const c = render(<QrCode value="alpha" />)
    expect(pathOf(a.container)).not.toBe(pathOf(b.container))
    expect(a.container.innerHTML).toBe(c.container.innerHTML)
  })

  it('never touches the network', () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch')
    render(<QrCode value="no network please" />)
    expect(fetchSpy).not.toHaveBeenCalled()
  })

  it('shows a fallback instead of throwing for a value too long to encode', () => {
    const { container } = render(<QrCode value={'x'.repeat(5000)} />)
    expect(screen.getByTestId('qr-error')).toBeTruthy()
    expect(container.querySelector('svg')).toBeNull()
  })
})
