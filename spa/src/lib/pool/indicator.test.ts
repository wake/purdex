// spa/src/lib/pool/indicator.test.ts — what each indicator item reads and how it is coloured (monitoring tabs MON-3b; M8).
import { describe, it, expect } from 'vitest'
import { indicatorReading } from './indicator'
import type { CellFast, CellView } from './cell-model'

const fast = (over: Partial<CellFast> = {}): CellFast => ({ at: 1, cpu: 10, mem: 10, level: 1, page_rate: 0, disk: 10, disk_free_gb: 50, load1: 1, ncpu: 8, zone: 'normal', load: 40, ...over })
const view = (over: Partial<Pick<CellView, 'fast' | 'worker' | 'zone'>> = {}): Pick<CellView, 'fast' | 'worker' | 'zone'> => ({
  fast: fast(), worker: { enabled: true, slots: { heavy: 2, light: 4 }, running: { heavy: 0, light: 0 } }, zone: 'normal', ...over,
})

describe('indicatorReading — no reading', () => {
  it.each(['load', 'cpu', 'mem', 'pressure', 'sysload', 'disk', 'slots'] as const)('%s of a host without readings is null', (item) => {
    expect(indicatorReading(item, view({ fast: null, worker: null, zone: null }))).toBeNull()
  })
})

describe('indicatorReading — load follows the pool\'s zone, not a threshold', () => {
  it.each([
    ['normal', 'ok'], ['warning', 'warn'], ['danger', 'danger'], ['unknown', 'muted'],
  ] as const)('zone %s -> %s', (zone, tone) => {
    expect(indicatorReading('load', view({ fast: fast({ load: 91 }), zone }))).toEqual({ pct: 91, tone })
  })
  it('a high score with a normal zone is green (the score is long, the zone says fine)', () => {
    expect(indicatorReading('load', view({ fast: fast({ load: 91 }), zone: 'normal' }))).toEqual({ pct: 91, tone: 'ok' })
  })
})

describe('indicatorReading — cpu and memory: 70 warn, 90 danger', () => {
  it.each([[69, 'ok'], [70, 'warn'], [89, 'warn'], [90, 'danger']] as const)('cpu %i -> %s', (cpu, tone) => {
    expect(indicatorReading('cpu', view({ fast: fast({ cpu }) }))).toEqual({ pct: cpu, tone })
  })
  it.each([[69, 'ok'], [70, 'warn'], [90, 'danger']] as const)('mem %i -> %s', (mem, tone) => {
    expect(indicatorReading('mem', view({ fast: fast({ mem }) }))).toEqual({ pct: mem, tone })
  })
})

describe('indicatorReading — disk: 85 warn, 95 danger', () => {
  it.each([[84, 'ok'], [85, 'warn'], [94, 'warn'], [95, 'danger']] as const)('disk %i -> %s', (disk, tone) => {
    expect(indicatorReading('disk', view({ fast: fast({ disk }) }))).toEqual({ pct: disk, tone })
  })
})

describe('indicatorReading — memory pressure: 1/2/4 -> a third green, two thirds yellow, full red; 0 reads memory used', () => {
  it('level 1', () => expect(indicatorReading('pressure', view({ fast: fast({ level: 1, mem: 95 }) }))).toEqual({ pct: 100 / 3, tone: 'ok' }))
  it('level 2', () => expect(indicatorReading('pressure', view({ fast: fast({ level: 2 }) }))).toEqual({ pct: 200 / 3, tone: 'warn' }))
  it('level 4', () => expect(indicatorReading('pressure', view({ fast: fast({ level: 4 }) }))).toEqual({ pct: 100, tone: 'danger' }))
  it.each([[69, 'ok'], [70, 'warn'], [90, 'danger']] as const)('level 0 -> memory used %i -> %s', (mem, tone) => {
    expect(indicatorReading('pressure', view({ fast: fast({ level: 0, mem }) }))).toEqual({ pct: mem, tone })
  })
})

describe('indicatorReading — system load is load1 over the cores: 70 warn, 100 danger', () => {
  it.each([[5.5, 'ok', 69], [5.6, 'warn', 70], [7.9, 'warn', 99], [8, 'danger', 100], [12, 'danger', 100]] as const)('load1 %f on 8 cores -> %s', (load1, tone, pct) => {
    expect(indicatorReading('sysload', view({ fast: fast({ load1, ncpu: 8 }) }))).toEqual({ pct, tone })
  })
  it('no core count reads as 0', () => {
    expect(indicatorReading('sysload', view({ fast: fast({ load1: 3, ncpu: 0 }) }))).toEqual({ pct: 0, tone: 'ok' })
  })
})

describe('indicatorReading — slots in use: full is yellow, a worker that is off is grey', () => {
  const worker = (running: { heavy: number; light: number }, enabled = true) => ({ enabled, slots: { heavy: 2, light: 4 }, running })
  it('some in use: a share, ok', () => {
    expect(indicatorReading('slots', view({ worker: worker({ heavy: 1, light: 2 }) }))).toEqual({ pct: 50, tone: 'ok' })
  })
  it('all in use: full, warn', () => {
    expect(indicatorReading('slots', view({ worker: worker({ heavy: 2, light: 4 }) }))).toEqual({ pct: 100, tone: 'warn' })
  })
  it('the worker is off: grey, empty, whatever runs', () => {
    expect(indicatorReading('slots', view({ worker: worker({ heavy: 1, light: 0 }, false) }))).toEqual({ pct: 0, tone: 'muted' })
  })
  it('no slots at all: 0, ok', () => {
    expect(indicatorReading('slots', view({ worker: { enabled: true, slots: { heavy: 0, light: 0 }, running: { heavy: 0, light: 0 } } }))).toEqual({ pct: 0, tone: 'ok' })
  })
  it('no worker information: null', () => {
    expect(indicatorReading('slots', view({ worker: null }))).toBeNull()
  })
})

describe('indicatorReading — non-finite readings read as 0', () => {
  it.each([NaN, Infinity])('%s', (v) => {
    expect(indicatorReading('cpu', view({ fast: fast({ cpu: v }) }))?.pct).toBe(0)
    expect(indicatorReading('sysload', view({ fast: fast({ load1: v }) }))?.pct).toBe(0)
  })
})
