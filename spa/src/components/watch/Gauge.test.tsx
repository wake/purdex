import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { Gauge } from './Gauge'
import type { GaugeView } from '../../lib/pool/host-charts'

const C = 2 * Math.PI * 26
const arc = () => screen.queryByTestId('gauge-arc')
const drawn = () => Number.parseFloat((arc()!.getAttribute('stroke-dasharray') ?? '').split(' ')[0])
const view = (over: Partial<GaugeView> = {}): GaugeView => ({ value: '50', unit: '%', pct: 50, tone: 'ok', ...over })
const show = (reading: GaugeView | null, label = 'CPU') => render(<Gauge label={label} reading={reading} testId="gauge-cpu" />)

describe('Gauge', () => {
  it.each([[0, null], [50, 0.5], [100, 1]] as const)('a reading of %s%% draws %s of the circle', (pct, share) => {
    show(view({ value: String(pct), pct }))
    if (share === null) expect(arc()).toBeNull()
    else expect(drawn()).toBeCloseTo(C * share, 1)
    expect(screen.getByTestId('gauge-cpu')).toHaveTextContent(String(pct))
  })

  it('the arc runs counterclockwise from 12 o\'clock, 56 px, 4 px, round cap, on a --border-default track', () => {
    const { container } = show(view({ pct: 30 }))
    expect(arc()).toHaveAttribute('transform', 'translate(56 0) scale(-1 1) rotate(-90 28 28)')
    expect(arc()).toHaveAttribute('stroke-linecap', 'round')
    expect(arc()).toHaveAttribute('stroke-width', '4')
    expect(container.querySelector('svg')).toHaveAttribute('width', '56')
    expect(container.querySelector('circle[stroke="var(--border-default)"]')).not.toBeNull()
  })

  it.each([['ok', 'stroke-status-success-text'], ['warn', 'stroke-status-warning-text'], ['danger', 'stroke-status-error-text']] as const)('a %s reading is drawn in its colour', (tone, cls) => {
    show(view({ tone }))
    expect(screen.getByTestId('gauge-cpu')).toHaveAttribute('data-tone', tone)
    expect(arc()!.getAttribute('class')).toContain(cls)
  })

  it('a word in the middle (pressure): the value as given, the arc as long as the reading says', () => {
    show(view({ value: 'Normal', unit: undefined, pct: 33 }), 'Pressure')
    expect(drawn()).toBeCloseTo(C * 0.33, 1)
    expect(screen.getByTestId('gauge-cpu')).toHaveTextContent('Normal')
    expect(screen.getByTestId('gauge-cpu')).toHaveTextContent('Pressure')
  })

  it('a length over the circle is cut to it', () => {
    show(view({ pct: 250 }))
    expect(drawn()).toBeCloseTo(C, 1)
  })

  it('the unit is shown under the value, and the label under that', () => {
    show(view({ value: '3/6', unit: 'jobs' }), 'Slots')
    expect(screen.getByTestId('gauge-cpu')).toHaveTextContent('3/6')
    expect(screen.getByTestId('gauge-cpu')).toHaveTextContent('jobs')
    expect(screen.getByTestId('gauge-cpu')).toHaveTextContent('Slots')
  })

  it('no reading: a dash and no arc', () => {
    show(null)
    expect(arc()).toBeNull()
    expect(screen.getByTestId('gauge-cpu')).toHaveTextContent('—')
    expect(screen.getByTestId('gauge-cpu')).toHaveAttribute('data-tone', 'nodata')
  })
})
