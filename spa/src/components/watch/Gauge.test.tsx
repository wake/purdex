import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { Gauge } from './Gauge'
import { metricDef } from '../../lib/watch/metrics'
import { fast } from '../../lib/fleet/__fixtures__/builders'
import { useI18nStore } from '../../stores/useI18nStore'
const t = (k: string, p?: Record<string, string | number>) => useI18nStore.getState().t(k, p)

const C = 2 * Math.PI * 26
const arc = () => screen.queryByTestId('gauge-arc')
const drawn = () => Number.parseFloat((arc()!.getAttribute('stroke-dasharray') ?? '').split(' ')[0])

describe('Gauge', () => {
  it.each([[0, null], [50, 0.5], [100, 1]] as const)('cpu %s%% draws %s of the circle', (cpu, share) => {
    render(<Gauge def={metricDef('cpu')} fast={fast({ cpu })} offline={false} />)
    if (share === null) expect(arc()).toBeNull()
    else expect(drawn()).toBeCloseTo(C * share, 1)
    expect(screen.getByTestId('gauge-cpu')).toHaveTextContent(String(cpu))
  })

  it('the arc runs counterclockwise from 12 o\'clock, 56 px, 4 px, round cap, on a --border-default track', () => {
    const { container } = render(<Gauge def={metricDef('cpu')} fast={fast({ cpu: 30 })} offline={false} />)
    expect(arc()).toHaveAttribute('transform', 'translate(56 0) scale(-1 1) rotate(-90 28 28)')
    expect(arc()).toHaveAttribute('stroke-linecap', 'round')
    expect(arc()).toHaveAttribute('stroke-width', '4')
    expect(container.querySelector('svg')).toHaveAttribute('width', '56')
    expect(container.querySelector('circle[stroke="var(--border-default)"]')).not.toBeNull()
  })

  it.each([[30, 'ok', 'stroke-text-secondary'], [75, 'warn', 'stroke-status-warning'], [95, 'danger', 'stroke-status-error']] as const)('cpu %s is %s', (cpu, tone, cls) => {
    render(<Gauge def={metricDef('cpu')} fast={fast({ cpu })} offline={false} />)
    expect(screen.getByTestId('gauge-cpu')).toHaveAttribute('data-tone', tone)
    expect(arc()!.getAttribute('class')).toContain(cls)
  })

  it('pressure: the word in the middle, a third of the arc when normal', () => {
    render(<Gauge def={metricDef('level')} fast={fast({ level: 1 })} offline={false} />)
    expect(drawn()).toBeCloseTo(C * 0.33, 1)
    expect(screen.getByTestId('gauge-level')).toHaveTextContent(t('watch.host.pressure.normal'))
  })

  it('pressure level 0: no arc', () => {
    render(<Gauge def={metricDef('level')} fast={fast({ level: 0 })} offline={false} />)
    expect(arc()).toBeNull()
  })

  it('offline: a dash and no arc', () => {
    render(<Gauge def={metricDef('cpu')} fast={fast({ cpu: 80 })} offline />)
    expect(arc()).toBeNull()
    expect(screen.getByTestId('gauge-cpu')).toHaveTextContent('—')
    expect(screen.getByTestId('gauge-cpu')).toHaveAttribute('data-tone', 'offline')
  })

  it('no frame yet: a dash and no arc', () => {
    render(<Gauge def={metricDef('cpu')} fast={null} offline={false} />)
    expect(arc()).toBeNull()
  })
})
