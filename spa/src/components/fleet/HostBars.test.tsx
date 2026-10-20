import { describe, it, expect } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import { HostBars } from './HostBars'
import { cpuReading, memoryReading, pressureReading } from '../../lib/pool/bar-readings'

describe('non-finite readings read as 0 (#3047 review)', () => {
  it.each([NaN, Infinity, -Infinity])('%s in -> a finite pct and a finite bar height', (v) => {
    expect(cpuReading(v).pct).toBe(0)
    expect(memoryReading(v).pct).toBe(0)
    expect(pressureReading(0, v).pct).toBe(0)
    render(<HostBars left={cpuReading(v)} right={memoryReading(v)} size={16} />)
    for (const b of screen.getAllByTestId(/^host-bar-(left|right)$/)) expect(Number.isFinite(Number(b.getAttribute('data-pct')))).toBe(true)
  })
})

describe('readings — the bands are the ring\'s (70 warn, 90 danger)', () => {
  it.each([[69, 'ok'], [70, 'warn'], [89, 'warn'], [90, 'danger'], [0, 'ok']] as const)('CPU %i%% -> %s', (cpu, tone) => {
    expect(cpuReading(cpu)).toEqual({ pct: cpu, tone })
  })
  it('CPU is rounded and clamped like the ring (69.6 -> 70 warn, 140 -> 100)', () => {
    expect(cpuReading(69.6)).toEqual({ pct: 70, tone: 'warn' })
    expect(cpuReading(140)).toEqual({ pct: 100, tone: 'danger' })
    expect(cpuReading(-3)).toEqual({ pct: 0, tone: 'ok' })
  })
  it.each([[69, 'ok'], [70, 'warn'], [90, 'danger']] as const)('memory used %i%% -> %s', (mem, tone) => {
    expect(memoryReading(mem)).toEqual({ pct: mem, tone })
  })
})

describe('pressureReading — the level, falling back to memory used % when it is unknown', () => {
  it('level 1 -> a third, ok; the memory % is not looked at', () => {
    expect(pressureReading(1, 95)).toEqual({ pct: 100 / 3, tone: 'ok' })
  })
  it('level 2 -> two thirds, warn', () => {
    expect(pressureReading(2, 10)).toEqual({ pct: 200 / 3, tone: 'warn' })
  })
  it('level 4 -> full, danger', () => {
    expect(pressureReading(4, 10)).toEqual({ pct: 100, tone: 'danger' })
  })
  it.each([[69, 'ok'], [70, 'warn'], [90, 'danger']] as const)('level 0 (unknown: Linux without PSI) -> memory used %i%% -> %s', (mem, tone) => {
    expect(pressureReading(0, mem)).toEqual({ pct: mem, tone })
  })
})

describe('HostBars', () => {
  const bar = (side: 'left' | 'right') => screen.getByTestId(`host-bar-${side}`)
  const fill = (side: 'left' | 'right') => within(bar(side)).queryByTestId('host-bar-fill')

  it('two bars in the ring\'s square, on the ring\'s track colour', () => {
    render(<HostBars left={cpuReading(30)} right={pressureReading(1, 82)} size={16} />)
    const box = screen.getByTestId('host-bars')
    expect(box.style.width).toBe('16px')
    expect(box.style.height).toBe('16px')
    for (const side of ['left', 'right'] as const) expect(bar(side).style.background).toBe('var(--border-default)')
  })
  it('each fill is its reading\'s share of the track height, in its own band\'s status colour', () => {
    render(<HostBars left={cpuReading(75)} right={pressureReading(4, 0)} size={16} />)
    const track = parseFloat(bar('left').style.height)
    expect(parseFloat(fill('left')!.style.height)).toBeCloseTo(track * 0.75, 1)
    expect(fill('left')!.className).toContain('bg-status-warning')
    expect(bar('left').dataset.tone).toBe('warn')
    expect(parseFloat(fill('right')!.style.height)).toBeCloseTo(track, 1)
    expect(fill('right')!.className).toContain('bg-status-error')
  })
  it('ok is the success colour', () => {
    render(<HostBars left={cpuReading(30)} right={memoryReading(50)} size={16} />)
    expect(fill('left')!.className).toContain('bg-status-success')
    expect(fill('right')!.className).toContain('bg-status-success')
  })
  it('no reading (offline, or none yet): grey tracks, no fill', () => {
    render(<HostBars left={null} right={null} size={16} />)
    expect(fill('left')).toBeNull()
    expect(fill('right')).toBeNull()
    expect(bar('left').dataset.tone).toBeUndefined()
  })
  it('a 0 % reading draws no fill; a tiny one still shows a dot', () => {
    render(<HostBars left={cpuReading(0)} right={cpuReading(1)} size={16} />)
    expect(fill('left')).toBeNull()
    expect(parseFloat(fill('right')!.style.height)).toBeGreaterThanOrEqual(parseFloat(bar('right').style.width))
  })
  it('follows the box size', () => {
    render(<HostBars left={cpuReading(50)} right={null} size={20} />)
    expect(screen.getByTestId('host-bars').style.width).toBe('20px')
  })
})

describe('HostBars — one bar and a muted reading (MON-3b)', () => {
  it('a single bar when there is no right reading at all', () => {
    render(<HostBars left={cpuReading(30)} size={16} />)
    expect(screen.getByTestId('host-bar-left')).toBeTruthy()
    expect(screen.queryByTestId('host-bar-right')).toBeNull()
  })
  it('a right reading of null is a track without a fill, still two bars', () => {
    render(<HostBars left={cpuReading(30)} right={null} size={16} />)
    expect(screen.getByTestId('host-bar-right')).toBeTruthy()
  })
  it('a muted reading is grey', () => {
    render(<HostBars left={{ pct: 60, tone: 'muted' }} size={16} />)
    expect(within(screen.getByTestId('host-bar-left')).getByTestId('host-bar-fill').className).toContain('bg-text-muted')
  })
})
