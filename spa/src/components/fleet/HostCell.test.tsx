import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent, within } from '@testing-library/react'
import { HostCell, type HostCellProps } from './HostCell'
import { cellView, fast } from './cell-view.fixture'
import type { CellView } from '../../lib/pool/cell-model'

const ID = '82b11c521df16c12'

function setup(v: CellView, over: Partial<HostCellProps> = {}) {
  const props: HostCellProps = { view: v, variant: 'wide', isActive: false, onOpen: vi.fn(), ...over }
  render(<HostCell {...props} />)
  return props
}
const tile = () => screen.getByTestId(`host-tile-${ID}`)
const ring = () => screen.getByTestId(`load-ring-${ID}`)
const bars = () => screen.getByTestId(`load-bars-${ID}`)
const barOf = (side: 'left' | 'right') => within(bars()).getByTestId(`host-bar-${side}`)
const withIndicator = (indicator: CellView['look']['indicator']) => cellView({ look: { ...cellView().look, indicator } })

describe('HostCell — the indicator of the host', () => {
  it('ring: the ring, no bars; its length is the load and its tone the zone', () => {
    setup(cellView({ fast: fast({ load: 92, zone: 'danger' }), zone: 'danger' }))
    expect(ring().dataset.zone).toBe('danger')
    expect(within(ring()).getByTestId('context-ring-arc').dataset.tone).toBe('danger')
    expect(screen.queryByTestId(`load-bars-${ID}`)).toBeNull()
  })
  it('the pool\'s zone unknown draws the ring muted', () => {
    setup(cellView({ zone: 'unknown' }))
    expect(within(ring()).getByTestId('context-ring-arc').dataset.tone).toBe('muted')
  })
  it('bars with pressure on the right: left CPU, right the pressure level', () => {
    setup({ ...withIndicator({ shape: 'bars', data: ['cpu', 'pressure'] }), fast: fast({ cpu: 75, level: 2, mem: 30 }) })
    expect(screen.queryByTestId(`load-ring-${ID}`)).toBeNull()
    expect(barOf('left').dataset.pct).toBe('75')
    expect(barOf('left').dataset.tone).toBe('warn')
    expect(Number(barOf('right').dataset.pct)).toBeCloseTo(200 / 3, 5)
  })
  it('bars with memory on the right: memory used %', () => {
    setup({ ...withIndicator({ shape: 'bars', data: ['cpu', 'mem'] }), fast: fast({ cpu: 10, level: 4, mem: 91 }) })
    expect(barOf('right').dataset.pct).toBe('91')
  })
  it('a single bar reads as the ring until the per-host shapes are drawn (MON-3b)', () => {
    setup(withIndicator({ shape: 'bar', data: ['disk'] }))
    expect(ring()).toBeTruthy()
  })
})

describe('HostCell — a host that is not ok', () => {
  it.each(['wwd_down', 'host_down', 'unknown'] as const)('%s: the badge dimmed and the track only', (liveness) => {
    setup(cellView({ liveness }))
    expect(screen.getByTestId(`host-badge-dim-${ID}`).className).toContain('opacity-40')
    expect(ring().dataset.zone).toBe('offline')
    expect(within(ring()).queryByTestId('context-ring-arc')).toBeNull()
  })
  it('the accessible name carries the state word; an ok host also its zone', () => {
    setup(cellView({ liveness: 'wwd_down' }))
    expect(tile().getAttribute('aria-label')).toBe('mlab, wwd is not answering (the machine is up)')
  })
  it('an ok host\'s name: host, state, zone', () => {
    setup(cellView())
    expect(tile().getAttribute('aria-label')).toBe('mlab, OK, Normal')
  })
  it('bars of a host that is not ok are the grey tracks only', () => {
    setup({ ...cellView({ liveness: 'host_down' }), look: { ...cellView().look, indicator: { shape: 'bars', data: ['cpu', 'pressure'] } } })
    expect(within(barOf('left')).queryByTestId('host-bar-fill')).toBeNull()
    expect(within(barOf('right')).queryByTestId('host-bar-fill')).toBeNull()
  })
})

describe('HostCell — the cell', () => {
  it('shows the badge, and no queue count (the sidebar does not read the gate)', () => {
    setup(cellView())
    expect(screen.getByTestId(`host-badge-${ID}`)).toBeTruthy()
    expect(screen.queryByTestId(`queue-badge-${ID}`)).toBeNull()
  })
  it('a breathing dot while the worker runs something', () => {
    setup(cellView({ worker: { enabled: true, slots: { heavy: 2, light: 4 }, running: { heavy: 1, light: 0 } } }))
    expect(screen.getByTestId(`worker-running-${ID}`)).toBeTruthy()
  })
  it('no dot when nothing runs, and none for a host that is not ok', () => {
    setup(cellView({ worker: { enabled: true, slots: { heavy: 2, light: 4 }, running: { heavy: 0, light: 0 } } }))
    expect(screen.queryByTestId(`worker-running-${ID}`)).toBeNull()
  })
  it('click opens; the variant and the active mark are data attributes', () => {
    const p = setup(cellView(), { variant: 'narrow', isActive: true })
    expect(tile().dataset.variant).toBe('narrow')
    expect(tile().dataset.active).toBe('true')
    fireEvent.click(tile())
    expect(p.onOpen).toHaveBeenCalledTimes(1)
  })
})

describe('HostCell — the hover card', () => {
  it('opens on enter and focus, closes on leave and blur and click', () => {
    setup(cellView())
    expect(screen.queryByTestId('host-card')).toBeNull()
    fireEvent.mouseEnter(tile())
    expect(screen.getByTestId('host-card')).toBeTruthy()
    fireEvent.mouseLeave(tile())
    expect(screen.queryByTestId('host-card')).toBeNull()
    fireEvent.focus(tile())
    expect(screen.getByTestId('host-card')).toBeTruthy()
    fireEvent.blur(tile())
    expect(screen.queryByTestId('host-card')).toBeNull()
    fireEvent.mouseEnter(tile())
    fireEvent.click(tile())
    expect(screen.queryByTestId('host-card')).toBeNull()
  })
  it('the card is on the right in the narrow bar and above in the wide one', () => {
    setup(cellView(), { variant: 'narrow' })
    fireEvent.mouseEnter(tile())
    expect(screen.getByTestId('host-card').getAttribute('data-placement')).toBe('right')
  })
  it('one card at a time', () => {
    render(<><HostCell view={cellView()} variant="wide" isActive={false} onOpen={vi.fn()} /><HostCell view={cellView({ hostId: '0123456789abcdef', name: 'air26' })} variant="wide" isActive={false} onOpen={vi.fn()} /></>)
    fireEvent.mouseEnter(screen.getByTestId(`host-tile-${ID}`))
    fireEvent.mouseEnter(screen.getByTestId('host-tile-0123456789abcdef'))
    expect(screen.getAllByTestId('host-card')).toHaveLength(1)
  })
})
