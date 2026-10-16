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
  // The callers that pass neither `tone` nor `noShape` (TeamCell, TeamPanelRow) must render exactly what they rendered
  // before those props existed: recorded from the component before the change.
  it('existing callers: byte-identical markup', () => {
    const { container: a } = render(<ContextRing pct={40} model="opus" size={16} symbolColor="rgba(34, 197, 94, 1)" />)
    expect(a.innerHTML).toMatchInlineSnapshot(`"<span class="relative inline-grid flex-shrink-0" style="width: 16px; height: 16px; color: rgb(34, 197, 94);" data-testid="context-ring"><svg width="16" height="16" viewBox="0 0 16 16" class="absolute inset-0 block"><circle cx="8" cy="8" r="6" fill="none" stroke="var(--border-default)" stroke-width="2.5"></circle><circle data-testid="context-ring-arc" data-shown="40" data-tone="ok" data-direction="ccw" class="stroke-status-success" cx="8" cy="8" r="6" fill="none" stroke-width="2.5" stroke-linecap="round" stroke-dasharray="15.08 37.70" transform="translate(16 0) scale(-1 1) rotate(-90 8 8)"></circle><g data-testid="model-icon-opus" fill="currentColor" role="img" aria-label="Opus"><path d="M6 .6 11.4 6 6 11.4.6 6Z" transform="translate(4.6400, 4.6400) scale(0.56000)"></path></g></svg></span>"`)
    const { container: b } = render(<ContextRing pct={undefined} model={undefined} size={16} />)
    expect(b.innerHTML).toMatchInlineSnapshot(`"<span class="relative inline-grid flex-shrink-0" style="width: 16px; height: 16px;" data-testid="context-ring"><svg width="16" height="16" viewBox="0 0 16 16" class="absolute inset-0 block"><circle cx="8" cy="8" r="6" fill="none" stroke="var(--border-default)" stroke-width="2.5"></circle><g data-testid="model-icon-unknown" fill="currentColor" stroke="currentColor"><circle cx="8" cy="8" r="3.36" fill="none" stroke-width="0.8" stroke-dasharray="1.2 1"></circle><text x="8" y="8" text-anchor="middle" dominant-baseline="central" stroke="none" font-size="7.392">?</text></g></svg></span>"`)
  })
})

// The fleet host cell (fleet spec §8.1): its arc's colour is the zone the daemon sent, not the score; a host has no model,
// so its middle is empty.
describe('ContextRing — tone and noShape (fleet host cell)', () => {
  it('`tone` sets the arc colour regardless of the length', () => {
    render(<ContextRing pct={95} model={undefined} tone="ok" />)
    const arc = screen.getByTestId('context-ring-arc')
    expect(arc.getAttribute('data-tone')).toBe('ok')
    expect(arc.getAttribute('class')).toBe('stroke-status-success')
    expect(arc.getAttribute('data-shown')).toBe('95')
  })
  it('`tone` danger on a short arc', () => {
    render(<ContextRing pct={5} model={undefined} tone="danger" />)
    expect(screen.getByTestId('context-ring-arc').getAttribute('class')).toBe('stroke-status-error')
  })
  it('`tone` without a reading still draws no arc', () => {
    render(<ContextRing pct={undefined} model={undefined} tone="warn" />)
    expect(screen.queryByTestId('context-ring-arc')).toBeNull()
  })
  it('`noShape`: nothing in the middle, neither the "?" nor a model shape; only the track and the arc', () => {
    const { container } = render(<ContextRing pct={30} model={undefined} size={16} noShape />)
    const svg = container.querySelector('[data-testid="context-ring"] > svg')!
    expect(screen.queryByTestId('model-icon-unknown')).toBeNull()
    expect(svg.querySelectorAll('g, path, text')).toHaveLength(0)
    expect(svg.querySelectorAll('circle')).toHaveLength(2)
  })
  it('`noShape` wins over a model', () => {
    render(<ContextRing pct={30} model="opus" noShape />)
    expect(screen.queryByTestId('model-icon-opus')).toBeNull()
  })
  it('`noShape` without a reading: the track alone', () => {
    const { container } = render(<ContextRing pct={undefined} model={undefined} noShape />)
    const svg = container.querySelector('[data-testid="context-ring"] > svg')!
    expect(svg.children).toHaveLength(1)
    expect(svg.children[0].getAttribute('stroke')).toBe('var(--border-default)')
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
