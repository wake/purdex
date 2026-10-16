import { describe, it, expect } from 'vitest'
import { METRICS, metricDef, gaugeOf, niceMax } from './metrics'
import { fast } from '../fleet/__fixtures__/builders'

const t = (k: string) => k
const tone = (k: Parameters<typeof metricDef>[0], v: number) => metricDef(k).toneOf(v)

describe('metric tones', () => {
  it.each([[69.9, 'ok'], [70, 'warn'], [89.9, 'warn'], [90, 'danger']] as const)('cpu and mem at %s are %s', (v, want) => {
    expect(tone('cpu', v)).toBe(want)
    expect(tone('mem', v)).toBe(want)
  })
  it.each([[1, 'ok'], [2, 'warn'], [4, 'danger']] as const)('pressure level %s is %s', (v, want) => expect(tone('level', v)).toBe(want))
  it.each([[0, 'ok'], [50, 'ok'], [51, 'warn'], [800, 'warn'], [801, 'danger']] as const)('page_rate %s is %s', (v, want) => expect(tone('page_rate', v)).toBe(want))
  it.each([[84.9, 'ok'], [85, 'warn'], [94.9, 'warn'], [95, 'danger']] as const)('disk %s is %s', (v, want) => expect(tone('disk', v)).toBe(want))
})

describe('the gauge of a reading', () => {
  it('cpu / disk: the value is the percentage', () => {
    expect(gaugeOf('cpu', fast({ cpu: 42.4 }), t)).toMatchObject({ value: '42', unit: '%', pct: 42.4, tone: 'ok' })
    expect(gaugeOf('disk', fast({ disk: 96 }), t)).toMatchObject({ pct: 96, tone: 'danger' })
  })
  it.each([[1, 33, 'ok'], [2, 66, 'warn'], [4, 100, 'danger']] as const)('pressure level %s fills %s%% in %s', (level, pct, want) => {
    expect(gaugeOf('level', fast({ level }), t)).toMatchObject({ pct, tone: want })
  })
  it('pressure level 0 (unknown) has no arc', () => {
    expect(gaugeOf('level', fast({ level: 0 }), t)).toMatchObject({ pct: 0, value: '—' })
  })
  it('paging is full at 800 pages/s', () => {
    expect(gaugeOf('page_rate', fast({ page_rate: 400 }), t)).toMatchObject({ pct: 50, tone: 'warn' })
    expect(gaugeOf('page_rate', fast({ page_rate: 801 }), t).pct).toBe(100)
  })
})

describe('chart scale', () => {
  it('the five readings, in order, with warning lines', () => {
    expect(METRICS.map((d) => [d.k, d.warnAt])).toEqual([['cpu', 70], ['mem', 70], ['level', 1.75], ['page_rate', 50], ['disk', 85]])
  })
  it('paging grows its axis past 100', () => {
    expect(niceMax(0)).toBe(100)
    expect(niceMax(300)).toBe(400)
  })
  it('fmt', () => {
    expect(metricDef('cpu').fmt(42.4, t)).toBe('42%')
    expect(metricDef('level').fmt(4, t)).toBe('watch.host.pressure.critical')
    expect(metricDef('level').fmt(1, t)).toBe('watch.host.pressure.normal')
  })
})
