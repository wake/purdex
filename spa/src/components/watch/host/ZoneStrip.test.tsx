// The zone strip at the top of the 狀態 tab's full charts (monitoring tabs spec M44; plan MON-7c-2): one thin bar, each sample's zone placed by its time in [now − range, now].
import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { ZoneStrip } from './ZoneStrip'
import { RANGE_MS, type HostSample } from '../../../lib/pool/host-history'
import type { PoolZone } from '../../../lib/pool/types'

const NOW = Date.parse('2026-10-20T12:00:00Z')
const R = RANGE_MS['1h']
const at = (ms: number, zone: PoolZone | null): HostSample => ({
  at: ms, cpuPct: 1, load1: 1, load5: 1, ncpu: 8, memUsedMb: 1, memAvailMb: 1, memTotalMb: 2, pageOut: 0, swapOut: 0, swapUsedMb: 0, diskUsedPct: 1, diskFreeGb: 1,
  runHeavy: 0, runLight: 0, slotHeavy: 1, slotLight: 1, workerOn: true, zone,
})
const show = (samples: HostSample[], dead: { fromAt: number; tone: 'warning' | 'error' | 'muted' } | null = null) =>
  render(<ZoneStrip samples={samples} range="1h" now={NOW} dead={dead} />)
const segs = () => screen.queryAllByTestId('zone-seg')

describe('ZoneStrip', () => {
  it('a segment for each run of one zone, in the zone\'s colour', () => {
    show([at(NOW - 4 * 60_000, 'normal'), at(NOW - 3 * 60_000, 'normal'), at(NOW - 2 * 60_000, 'warning'), at(NOW - 60_000, 'danger')])
    expect(segs().map((s) => s.getAttribute('data-zone'))).toEqual(['normal', 'warning', 'danger'])
    expect(segs()[0].className).toContain('bg-status-success')
    expect(segs()[1].className).toContain('bg-status-warning')
    expect(segs()[2].className).toContain('bg-status-error')
  })

  it('an unknown zone, and a sample with none, are drawn as unknown, never as normal', () => {
    show([at(NOW - 120_000, 'unknown'), at(NOW - 60_000, null)])
    expect(segs().map((s) => s.getAttribute('data-zone'))).toEqual(['unknown'])
    expect(segs()[0].className).toContain('bg-text-muted')
    expect(segs()[0].className).not.toContain('bg-status-success')
  })

  it('a segment is placed by time: a sample half a range back starts in the middle', () => {
    show([at(NOW - R / 2, 'normal')])
    const left = parseFloat(segs()[0].getAttribute('data-left') ?? '')
    expect(left).toBeCloseTo(50, 0)
  })

  it('where there is no sample there is nothing: a hole of more than three steps splits the bar', () => {
    show([at(NOW - 600_000, 'normal'), at(NOW - 540_000, 'normal'), at(NOW - 60_000, 'normal')])
    expect(segs()).toHaveLength(2)
  })

  it('after the host went the strip is slanted from there to the right edge, in the tone\'s colour', () => {
    show([at(NOW - 120_000, 'normal')], { fromAt: NOW - 60_000, tone: 'error' })
    const dead = screen.getByTestId('zone-dead')
    expect(dead).toHaveAttribute('data-tone', 'error')
    expect(parseFloat(dead.getAttribute('data-from-pct') ?? '')).toBeCloseTo(98.3, 0)
  })

  it('a host that is not gone has no slanted part', () => {
    show([at(NOW - 120_000, 'normal')])
    expect(screen.queryByTestId('zone-dead')).toBeNull()
  })

  it('samples after the moment the host went are not drawn', () => {
    show([at(NOW - 120_000, 'normal'), at(NOW - 30_000, 'danger')], { fromAt: NOW - 60_000, tone: 'muted' })
    expect(segs().map((s) => s.getAttribute('data-zone'))).toEqual(['normal'])
  })
})
