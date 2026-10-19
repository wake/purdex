// 7a-4b: the thin band above the charts that colours each sample by its zone.
import { describe, it, expect, afterEach } from 'vitest'
import { render, screen, cleanup } from '@testing-library/react'
import { ZoneBand } from './ZoneBand'
import type { ZonePoint } from '../../lib/pool/history'

afterEach(() => cleanup())

const NOW = 1_000_000_000
const pt = (secAgo: number, zone: ZonePoint['zone']): ZonePoint => ({ t: NOW - secAgo * 1000, zone })
const segs = () => screen.getAllByTestId('zone-seg')

describe('ZoneBand', () => {
  it('one segment per sample in time order, coloured by its zone', () => {
    render(<ZoneBand points={[pt(60, 'normal'), pt(55, 'warning'), pt(50, 'danger'), pt(45, 'unknown')]} now={NOW} />)
    expect(segs().map((s) => s.getAttribute('data-zone'))).toEqual(['normal', 'warning', 'danger', 'unknown'])
    const cls = (i: number) => segs()[i].getAttribute('class')!
    expect(cls(0)).toContain('text-status-success-text')
    expect(cls(1)).toContain('text-status-warning-text')
    expect(cls(2)).toContain('text-status-error-text')
    expect(cls(3)).toContain('text-text-muted')
  })

  it('a segment runs to the next sample; the last one is one sample wide', () => {
    render(<ZoneBand points={[pt(60, 'normal'), pt(30, 'warning')]} now={NOW} />)
    const w = (i: number) => Number(segs()[i].getAttribute('width'))
    expect(w(0)).toBeCloseTo((30_000 / 300_000) * 400, 1)
    expect(w(1)).toBeCloseTo((5_000 / 300_000) * 400, 1)
    expect(Number(segs()[0].getAttribute('x'))).toBeCloseTo(((300_000 - 60_000) / 300_000) * 400, 1)
  })

  it('a sample older than the 5 minutes is cut at the left edge', () => {
    render(<ZoneBand points={[pt(310, 'danger'), pt(250, 'normal')]} now={NOW} />)
    expect(segs()[0].getAttribute('x')).toBe('0')
  })

  it('no samples: nothing is drawn', () => {
    const { container } = render(<ZoneBand points={[]} now={NOW} />)
    expect(container.textContent).toBe('')
    expect(screen.queryByTestId('zone-band')).toBeNull()
  })

  it('names itself for assistive tech', () => {
    render(<ZoneBand points={[pt(10, 'normal')]} now={NOW} />)
    expect(screen.getByTestId('zone-band').getAttribute('aria-label')).toBe('Zone')
  })
})
