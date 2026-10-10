import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { ContextRing } from './ModelIcon'

describe('ContextRing', () => {
  it.each([[15, 'ok', 'stroke-status-success'], [75, 'warn', 'stroke-status-warning'], [95, 'danger', 'stroke-status-error']])(
    'used %i: counterclockwise, share = used, tone %s', (pct, tone, cls) => {
      render(<ContextRing pct={pct} model="opus" size={22} />)
      const arc = screen.getByTestId('context-ring-arc')
      expect(arc.getAttribute('data-direction')).toBe('ccw')
      expect(arc.getAttribute('data-shown')).toBe(String(pct))
      expect(arc.getAttribute('data-tone')).toBe(tone)
      expect(arc.getAttribute('class')).toContain(cls)
    })
  it('draws no arc without a reading', () => {
    render(<ContextRing pct={undefined} model="opus" />)
    expect(screen.queryByTestId('context-ring-arc')).toBeNull()
  })
})

// r4b — the model shape is drawn INSIDE the ring's own <svg>, so its centre is the ring's centre by arithmetic, not by CSS snapping.
describe('ContextRing symbol centring', () => {
  const nums = (s: string) => (s.match(/-?\d*\.?\d+/g) ?? []).map(Number)
  // The point that must sit on the ring's centre, in the path's own 12x12 box (optical centring, Bjango "Formulas for optical
  // adjustments"). opus (diamond) and sonnet (circle) are symmetric, so it is their bbox centre; sonnet is `M cx cy-r a r r …`,
  // so cx = x0, cy = y0 + r. haiku is a triangle: the mean of its 3 vertices (the centroid). fable is a five-point star:
  // the mean of its 5 outer vertices (even-numbered ones; = the circumcentre of a regular star).
  function anchor(model: string, d: string): [number, number] {
    const n = nums(d)
    const pts: Array<[number, number]> = []
    for (let i = 0; i + 1 < n.length; i += 2) pts.push([n[i], n[i + 1]])
    const mean = (ps: Array<[number, number]>): [number, number] => [ps.reduce((a, p) => a + p[0], 0) / ps.length, ps.reduce((a, p) => a + p[1], 0) / ps.length]
    if (model === 'sonnet') return [n[0], n[1] + n[2]]
    if (model === 'haiku') return mean(pts)
    if (model === 'fable') return mean(pts.filter((_, i) => i % 2 === 0))
    const xs = pts.map((p) => p[0]), ys = pts.map((p) => p[1])
    return [(Math.min(...xs) + Math.max(...xs)) / 2, (Math.min(...ys) + Math.max(...ys)) / 2]
  }
  it.each(['opus', 'sonnet', 'haiku', 'fable'].flatMap((m) => [12, 16, 20].map((s) => [m, s] as const)))(
    '%s in a %ipx ring: optical centre (bbox / centroid / outer-vertex mean) = (size/2, size/2)', (model, size) => {
      const { container } = render(<ContextRing pct={40} model={model as never} size={size} />)
      const svg = container.querySelector('[data-testid="context-ring"] > svg')!
      const path = svg.querySelector(`[data-testid="model-icon-${model}"] path`)!
      const [tx, ty, k] = nums(path.getAttribute('transform')!)
      const [ax, ay] = anchor(model, path.getAttribute('d')!)
      expect(tx + k * ax).toBeCloseTo(size / 2, 2)
      expect(ty + k * ay).toBeCloseTo(size / 2, 2)
    })
  it('unknown model: the "?" is in the same svg, anchored middle / central', () => {
    const { container } = render(<ContextRing pct={40} model={undefined} size={16} />)
    const svg = container.querySelector('[data-testid="context-ring"] > svg')!
    const q = svg.querySelector('[data-testid="model-icon-unknown"] text')!
    expect(q.textContent).toBe('?')
    expect(q.getAttribute('text-anchor')).toBe('middle')
    expect(q.getAttribute('dominant-baseline')).toBe('central')
    expect(q.getAttribute('x')).toBe('8')
    expect(q.getAttribute('y')).toBe('8')
  })
})
