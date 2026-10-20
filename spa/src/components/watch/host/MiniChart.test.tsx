import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent, within } from '@testing-library/react'
import { MiniChart } from './MiniChart'
import { miniChartsOf, type ChartDef } from '../../../lib/pool/host-charts'
import { RANGE_MS, type HostRange, type HostSample } from '../../../lib/pool/host-history'
import { useI18nStore } from '../../../stores/useI18nStore'

const t = (k: string, p?: Record<string, string | number>) => useI18nStore.getState().t(k, p)
const NOW = Date.parse('2026-10-20T12:00:00Z')
const W = 600
const H = 90
const sample = (at: number, over: Partial<HostSample> = {}): HostSample => ({
  at, cpuPct: 50, load1: 2, load5: 1, ncpu: 8, memUsedMb: 8192, memAvailMb: 8192, memTotalMb: 16384, pageOut: 5, swapOut: 1, swapUsedMb: 512, diskUsedPct: 50, diskFreeGb: 100,
  runHeavy: 1, runLight: 0, slotHeavy: 2, slotLight: 4, workerOn: true, zone: 'normal', ...over,
})
/** Samples every `stepMs` ending at NOW, `n` of them. */
const series = (n: number, stepMs: number, over: (i: number) => Partial<HostSample> = () => ({})): HostSample[] => Array.from({ length: n }, (_, i) => sample(NOW - (n - 1 - i) * stepMs, over(i)))
const defOf = (samples: HostSample[], id: string, range: HostRange = '5m'): ChartDef => miniChartsOf(samples, null, range).find((d) => d.id === id)!

function show(def: ChartDef, samples: HostSample[], over: Partial<Parameters<typeof MiniChart>[0]> = {}) {
  const onHover = vi.fn()
  const view = render(<MiniChart def={def} samples={samples} range="5m" now={NOW} hoverIndex={null} onHover={onHover} dead={null} {...over} />)
  return { onHover, ...view }
}
const pathOf = (id: string) => screen.getByTestId(`mini-path-${id}`).getAttribute('d') ?? ''
const subpaths = (d: string) => (d.match(/M/g) ?? []).length
const nums = (d: string) => (d.match(/-?\d+(\.\d+)?/g) ?? []).map(Number)
const hm = (ms: number) => { const d = new Date(ms); return `${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}` }

describe('MiniChart — drawing (M42)', () => {
  const s5 = series(5, 60_000, (i) => ({ cpuPct: 10 * (i + 1) }))
  it('a card: the title, a plot about 90 px tall in a viewBox that stretches', () => {
    const { container } = show(defOf(s5, 'cpu'), s5)
    expect(screen.getByTestId('mini-chart')).toHaveTextContent(t('watch.host.chart.cpu.title'))
    const svg = container.querySelector('svg')!
    expect(svg.getAttribute('preserveAspectRatio')).toBe('none')
    expect(svg.getAttribute('viewBox')).toBe(`0 0 ${W} ${H}`)
  })
  it('each series that has data is one path; a pending series has none', () => {
    const s = series(5, 60_000, () => ({ load1: null, load5: null }))
    show(defOf(s, 'load'), s)
    expect(screen.queryByTestId('mini-path-load1')).toBeNull()
    expect(screen.queryByTestId('mini-path-load5')).toBeNull()
  })
  it('two series, two paths', () => {
    show(defOf(s5, 'paging'), s5)
    expect(screen.getByTestId('mini-path-page')).toBeTruthy()
    expect(screen.getByTestId('mini-path-swap')).toBeTruthy()
  })
  it('a point is placed by its time: the newest at the right edge, the one half a range back in the middle', () => {
    const s = [sample(NOW - RANGE_MS['5m'] / 2, { cpuPct: 10 }), sample(NOW, { cpuPct: 10 })]
    show(defOf(s, 'cpu'), s)
    // the two are a half range apart, so each is a run of its own: read where each run starts
    const starts = [...pathOf('cpu').matchAll(/M([\d.]+) ([\d.]+)/g)].map((m) => Number(m[1]))
    expect(starts[0]).toBeCloseTo(W / 2, 0)
    expect(starts[1]).toBeCloseTo(W, 0)
  })
  it('a value is placed by its height between the min and the max', () => {
    const s = [sample(NOW - 60_000, { cpuPct: 0 }), sample(NOW, { cpuPct: 100 })]
    show(defOf(s, 'cpu'), s)
    const ys = nums(pathOf('cpu')).filter((_, i) => i % 2 === 1)
    expect(ys[0]).toBeCloseTo(H, 0)
    expect(ys[ys.length - 1]).toBeCloseTo(0, 0)
  })
  it('a null value breaks the line in two', () => {
    const s = series(5, 5000, (i) => (i === 2 ? { cpuPct: null } : { cpuPct: 30 })) // 5 s apart: the density a 5 minute range is read at
    show(defOf(s, 'cpu'), s)
    expect(subpaths(pathOf('cpu'))).toBe(2)
  })
  it('two points more than 3 steps apart also break it (5 s steps, a 20 s hole)', () => {
    const s = [sample(NOW - 30_000), sample(NOW - 25_000), sample(NOW - 5_000), sample(NOW)]
    show(defOf(s, 'cpu'), s)
    expect(subpaths(pathOf('cpu'))).toBe(2)
  })
  it('3 steps apart is still one line', () => {
    const s = [sample(NOW - 30_000), sample(NOW - 15_000), sample(NOW)]
    show(defOf(s, 'cpu'), s)
    expect(subpaths(pathOf('cpu'))).toBe(1)
  })
  it('the 1 minute series breaks after 3 minutes, not 15 seconds', () => {
    const s = series(4, 120_000)
    show(defOf(s, 'cpu', '6h'), s, { range: '6h' })
    expect(subpaths(pathOf('cpu'))).toBe(1)
  })
  it('a step series is horizontal then vertical', () => {
    const s = series(4, 5000, (i) => ({ runHeavy: i }))
    show(defOf(s, 'run'), s)
    const d = pathOf('runHeavy')
    expect(d).toContain('H')
    expect(d).toContain('V')
    expect(d).not.toMatch(/L/)
  })
  it('a dashed series is drawn dashed, a faint one lighter', () => {
    show(defOf(s5, 'run'), s5)
    expect(screen.getByTestId('mini-path-slotHeavy').getAttribute('stroke-dasharray')).toBeTruthy()
    expect(screen.getByTestId('mini-path-runHeavy').getAttribute('stroke-dasharray')).toBeNull()
  })
  it('a stacked second layer sits on top of the first: its line is higher', () => {
    show(defOf(s5, 'mem'), s5)
    const firstY = nums(pathOf('used'))[1]
    const secondY = nums(pathOf('avail'))[1]
    expect(secondY).toBeLessThan(firstY)
    expect(secondY).toBeCloseTo(0, 0) // used + available = the total = the top of the axis
  })
  it('an area series has a filled area under its line', () => {
    const dense = series(5, 5000, (i) => ({ cpuPct: 10 * (i + 1) }))
    show(defOf(dense, 'cpu'), dense)
    expect(screen.getByTestId('mini-area-cpu').getAttribute('d')).toMatch(/Z$/)
  })
  it('a threshold is a dashed line at its height: 70 % of a 0 to 100 axis', () => {
    show(defOf(s5, 'cpu'), s5)
    const lines = screen.getAllByTestId(/^mini-threshold-/)
    expect(lines).toHaveLength(2)
    expect(Number(lines[0].getAttribute('data-y'))).toBeCloseTo(H - 0.7 * H, 1)
    expect(Number(lines[1].getAttribute('data-y'))).toBeCloseTo(H - 0.9 * H, 1)
    expect(lines[0].getAttribute('stroke-dasharray')).toBeTruthy()
  })
  it('a threshold with a label writes it (the core count)', () => {
    show(defOf(s5, 'load'), s5)
    expect(screen.getByTestId('mini-threshold-label-0')).toHaveTextContent(t('watch.host.chart.cores', { n: 8 }))
  })
  it('three y ticks (the top, the middle, 0) and three x times (the start, the middle, the end of the range)', () => {
    show(defOf(s5, 'cpu'), s5)
    expect(screen.getAllByTestId(/^mini-ytick-/).map((e) => e.textContent)).toEqual(['100', '50', '0'])
    expect(screen.getAllByTestId(/^mini-xtick-/).map((e) => e.textContent)).toEqual([hm(NOW - RANGE_MS['5m']), hm(NOW - RANGE_MS['5m'] / 2), hm(NOW)])
  })
})

describe('MiniChart — what is not there yet', () => {
  it('every series pending: the plot says so and draws nothing', () => {
    const s = series(5, 60_000, () => ({ load1: null, load5: null }))
    show(defOf(s, 'load'), s)
    expect(screen.getByTestId('mini-pending')).toHaveTextContent(t('watch.host.chart.pending_all'))
    expect(screen.queryByTestId('mini-path-load1')).toBeNull()
  })
  it('one series pending: the others are drawn and the legend names the missing one', () => {
    const s = series(5, 60_000, () => ({ slotHeavy: null, slotLight: null, workerOn: null }))
    show(defOf(s, 'run', '6h'), s, { range: '6h' })
    expect(screen.queryByTestId('mini-pending')).toBeNull()
    expect(screen.getByTestId('mini-path-runHeavy')).toBeTruthy()
    const legend = screen.getByTestId('mini-legend-slotHeavy')
    expect(legend).toHaveTextContent(t('watch.host.chart.pending_series'))
    expect(screen.getByTestId('mini-legend-runHeavy')).not.toHaveTextContent(t('watch.host.chart.pending_series'))
  })
  it('no samples at all: pending', () => {
    show(defOf([], 'cpu'), [])
    expect(screen.getByTestId('mini-pending')).toBeTruthy()
  })
})

describe('MiniChart — hover (M42)', () => {
  const s5 = series(5, 60_000, (i) => ({ cpuPct: 10 * (i + 1) }))
  const plot = () => screen.getByTestId('mini-plot')
  const rect = (left = 0, width = W) => { plot().getBoundingClientRect = () => ({ left, width, right: left + width, top: 0, bottom: H, height: H, x: left, y: 0, toJSON: () => ({}) }) as DOMRect }

  it('the pointer on the right edge picks the newest point, on the left edge the nearest to the start', () => {
    const { onHover } = show(defOf(s5, 'cpu'), s5)
    rect()
    fireEvent.mouseMove(plot(), { clientX: W })
    expect(onHover).toHaveBeenLastCalledWith(4)
    fireEvent.mouseMove(plot(), { clientX: 0 })
    expect(onHover).toHaveBeenLastCalledWith(0)
  })
  it('the nearest point, not the one to the left: just right of the middle of two points picks the right one', () => {
    const { onHover } = show(defOf(s5, 'cpu'), s5)
    rect()
    // points are at 120, 240, 360, 480, 600 px (a 5 minute range, one a minute from 4 minutes back): the middle of the second and the third is 300
    fireEvent.mouseMove(plot(), { clientX: 305 })
    expect(onHover).toHaveBeenLastCalledWith(2)
    fireEvent.mouseMove(plot(), { clientX: 295 })
    expect(onHover).toHaveBeenLastCalledWith(1)
  })
  it('the plot need not start at the left of the window', () => {
    const { onHover } = show(defOf(s5, 'cpu'), s5)
    rect(400, W)
    fireEvent.mouseMove(plot(), { clientX: 400 + W })
    expect(onHover).toHaveBeenLastCalledWith(4)
  })
  it('leaving sends null', () => {
    const { onHover } = show(defOf(s5, 'cpu'), s5)
    fireEvent.mouseLeave(plot())
    expect(onHover).toHaveBeenLastCalledWith(null)
  })
  it('no samples: nothing to hover', () => {
    const { onHover } = show(defOf([], 'cpu'), [])
    rect()
    fireEvent.mouseMove(plot(), { clientX: 100 })
    expect(onHover).toHaveBeenLastCalledWith(null)
  })
  it('a hovered point draws a line and its time, and the big number is that point\'s', () => {
    show(defOf(s5, 'cpu'), s5, { hoverIndex: 1 })
    expect(screen.getByTestId('mini-big')).toHaveTextContent('20')
    expect(screen.getByTestId('mini-hover-line')).toBeTruthy()
    expect(Number(screen.getByTestId('mini-hover-line').getAttribute('x1'))).toBeCloseTo(240, 0)
    expect(screen.getByTestId('mini-hover-time')).toBeTruthy()
  })
  it('not hovered: the big number is the newest point\'s and there is no line', () => {
    show(defOf(s5, 'cpu'), s5)
    expect(screen.getByTestId('mini-big')).toHaveTextContent('50')
    expect(screen.queryByTestId('mini-hover-line')).toBeNull()
  })
})

describe('MiniChart — a host that has gone (M22)', () => {
  const s5 = series(5, 60_000)
  it('a slanted-line area from where the host went to the right edge', () => {
    show(defOf(s5, 'cpu'), s5, { dead: { fromAt: NOW - RANGE_MS['5m'] / 4, tone: 'error' } })
    const dead = screen.getByTestId('mini-dead')
    expect(Number(dead.getAttribute('data-from-pct'))).toBeCloseTo(75, 1)
    expect(Number(dead.getAttribute('x'))).toBeCloseTo(0.75 * W, 0)
    expect(Number(dead.getAttribute('width'))).toBeCloseTo(0.25 * W, 0)
    expect(dead.getAttribute('data-tone')).toBe('error')
  })
  it('it starts at the left edge when the host went before the range, and is gone when it went after now', () => {
    const { unmount } = show(defOf(s5, 'cpu'), s5, { dead: { fromAt: NOW - 2 * RANGE_MS['5m'], tone: 'muted' } })
    expect(Number(screen.getByTestId('mini-dead').getAttribute('data-from-pct'))).toBe(0)
    unmount()
    show(defOf(s5, 'cpu'), s5, { dead: { fromAt: NOW + 60_000, tone: 'muted' } })
    expect(screen.queryByTestId('mini-dead')).toBeNull()
  })
  it('no dead host: no slanted area', () => {
    show(defOf(s5, 'cpu'), s5)
    expect(screen.queryByTestId('mini-dead')).toBeNull()
  })
  it('the big number is the last point before the host went, not a later one', () => {
    show(defOf(series(5, 60_000, (i) => ({ cpuPct: 10 * (i + 1) })), 'cpu'), series(5, 60_000, (i) => ({ cpuPct: 10 * (i + 1) })), { dead: { fromAt: NOW - 90_000, tone: 'error' } })
    // points at -240, -180, -120, -60, 0 seconds: the last one before -90 s is the third (value 30)
    expect(within(screen.getByTestId('mini-chart')).getByTestId('mini-big')).toHaveTextContent('30')
  })
  it('the big number keeps the reading taken exactly at the moment the host went (fromAt = lastFactsAt)', () => {
    const s = series(5, 60_000, (i) => ({ cpuPct: 10 * (i + 1) }))
    show(defOf(s, 'cpu'), s, { dead: { fromAt: s[3].at, tone: 'error' } })
    // fromAt is the 4th point's own time: that reading (value 40) still counts
    expect(within(screen.getByTestId('mini-chart')).getByTestId('mini-big')).toHaveTextContent('40')
  })
})
