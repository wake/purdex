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
  // bbox of a path in its own 12x12 box. opus / haiku / fable are straight-edged polygons (M + implicit L), so the
  // extremes of the vertices ARE the bbox. sonnet is a circle: `M cx cy-r a r r …` starts at the top, so cx = x0, cy = y0 + r.
  function bbox(model: string, d: string): [number, number, number, number] {
    const n = nums(d)
    if (model === 'sonnet') { const [x0, y0, r] = n; return [x0 - r, y0, x0 + r, y0 + 2 * r] }
    const xs = n.filter((_, i) => i % 2 === 0), ys = n.filter((_, i) => i % 2 === 1)
    return [Math.min(...xs), Math.min(...ys), Math.max(...xs), Math.max(...ys)]
  }
  it.each(['opus', 'sonnet', 'haiku', 'fable'].flatMap((m) => [12, 16, 20].map((s) => [m, s] as const)))(
    '%s in a %ipx ring: path bbox centre = (size/2, size/2)', (model, size) => {
      const { container } = render(<ContextRing pct={40} model={model as never} size={size} />)
      const svg = container.querySelector('[data-testid="context-ring"] > svg')!
      const path = svg.querySelector(`[data-testid="model-icon-${model}"] path`)!
      const [tx, ty, k] = nums(path.getAttribute('transform')!)
      const [x0, y0, x1, y1] = bbox(model, path.getAttribute('d')!)
      expect(tx + k * (x0 + x1) / 2).toBeCloseTo(size / 2, 2)
      expect(ty + k * (y0 + y1) / 2).toBeCloseTo(size / 2, 2)
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
