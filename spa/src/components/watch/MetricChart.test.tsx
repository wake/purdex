import { describe, it, expect } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import { MetricRow, TimeAxis } from './MetricChart'
import { metricDef } from '../../lib/watch/metrics'
import type { HistoryPoint } from '../../lib/fleet/history'
import { useI18nStore } from '../../stores/useI18nStore'
const t = (k: string, p?: Record<string, string | number>) => useI18nStore.getState().t(k, p)

const NOW = 10_000_000_000
const at = (secAgo: number, avg: number, max = avg): HistoryPoint => ({ t: NOW - secAgo * 1000, avg, max })
const row = (pts: HistoryPoint[], k: 'cpu' | 'level' = 'cpu', offline = false) =>
  render(<MetricRow def={metricDef(k)} points={pts} now={NOW} range="5m" offline={offline} />)

describe('MetricRow', () => {
  it('splits the line at the warning line: only the stretches above it take the warning or error colour', () => {
    row([at(100, 50), at(80, 60), at(60, 80), at(40, 95), at(20, 40)])
    const tones = within(screen.getByTestId('metric-cpu')).getAllByTestId('chart-line').map((p) => p.getAttribute('data-tone'))
    expect(tones).toEqual(['ok', 'warn', 'danger'])
    expect(screen.getAllByTestId('chart-line')[0].getAttribute('class')).toContain('text-text-secondary')
    expect(screen.getAllByTestId('chart-line')[2].getAttribute('class')).toContain('text-status-error')
  })

  it('draws the dashed warning line', () => {
    const { container } = row([at(10, 10), at(5, 20)])
    expect(container.querySelector('line[stroke-dasharray="3 3"]')).not.toBeNull()
  })

  it('the faint band follows the peak (max), the line the average', () => {
    row([at(20, 40, 95), at(10, 40, 95)])
    // viewBox height 36: Y(v) = 35 - v/100 * 32. avg 40 -> 22.2, max 95 -> 4.6
    const band = screen.getByTestId('chart-band').getAttribute('d')!
    const line = screen.getByTestId('chart-line').getAttribute('d')!
    expect(band).toContain('4.6')
    expect(line).toContain('22.2')
    expect(line).not.toContain('4.6')
  })

  it('shows 平均 and 峰值 over the range', () => {
    row([at(20, 10, 30), at(10, 20, 90)])
    expect(screen.getByTestId('metric-cpu')).toHaveTextContent(t('watch.host.avg_peak', { avg: '15%', peak: '90%' }))
  })

  it('pressure reads as words', () => {
    row([at(20, 1), at(10, 4)], 'level')
    expect(screen.getByTestId('metric-level')).toHaveTextContent(t('watch.host.avg_peak', { avg: t('watch.host.pressure.warn'), peak: t('watch.host.pressure.critical') }))
  })

  it('an empty series: dashes, no line', () => {
    row([])
    expect(screen.queryByTestId('chart-line')).toBeNull()
    expect(screen.getByTestId('metric-cpu')).toHaveTextContent(t('watch.host.avg_peak', { avg: '—', peak: '—' }))
  })

  it('offline dims the chart', () => {
    row([at(20, 10), at(10, 20)], 'cpu', true)
    expect(screen.getByTestId('chart-area').className).toContain('opacity-50')
  })
})

describe('TimeAxis', () => {
  it('says how much has been collected while the history is shorter than the range', () => {
    render(<TimeAxis points={[at(150, 1)]} now={NOW} range="5m" />)
    expect(screen.getByTestId('axis-collecting')).toHaveTextContent(t('watch.host.collecting', { n: 2 }))
  })
  it('says nothing once the range is covered', () => {
    render(<TimeAxis points={[at(300, 1)]} now={NOW} range="5m" />)
    expect(screen.getByTestId('axis-collecting')).toHaveTextContent(/^$/)
  })
  it('an empty series has collected 0 minutes', () => {
    render(<TimeAxis points={[]} now={NOW} range="1h" />)
    expect(screen.getByTestId('axis-collecting')).toHaveTextContent(t('watch.host.collecting', { n: 0 }))
  })
  it('labels now … −n 分 / −n 小時', () => {
    const { unmount } = render(<TimeAxis points={[]} now={NOW} range="5m" />)
    expect(screen.getByTestId('axis')).toHaveTextContent(t('watch.host.axis_now'))
    expect(screen.getByTestId('axis')).toHaveTextContent(t('watch.host.axis_min', { n: 5 }))
    unmount()
    render(<TimeAxis points={[]} now={NOW} range="6h" />)
    expect(screen.getByTestId('axis')).toHaveTextContent(t('watch.host.axis_hour', { n: 6 }))
  })
})
