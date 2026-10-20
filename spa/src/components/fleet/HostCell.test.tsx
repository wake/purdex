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

describe('HostCell — the indicator of the host (M9)', () => {
  it('ring: the ring, no bars; its length is the item\'s share and its tone the load\'s zone', () => {
    setup(cellView({ fast: fast({ load: 92 }), zone: 'danger' }))
    expect(ring().dataset.zone).toBe('danger')
    expect(within(ring()).getByTestId('context-ring-arc').dataset.tone).toBe('danger')
    expect(screen.queryByTestId(`load-bars-${ID}`)).toBeNull()
  })
  it('the load of a normal zone is green even when the score is high', () => {
    setup(cellView({ fast: fast({ load: 91 }), zone: 'normal' }))
    expect(within(ring()).getByTestId('context-ring-arc').dataset.tone).toBe('ok')
  })
  it('the pool\'s zone unknown draws the ring muted', () => {
    setup(cellView({ zone: 'unknown' }))
    expect(within(ring()).getByTestId('context-ring-arc').dataset.tone).toBe('muted')
  })
  it('a ring on another item reads that item: disk 90 is yellow', () => {
    setup({ ...withIndicator({ shape: 'ring', data: ['disk'] }), fast: fast({ disk: 90 }) })
    expect(within(ring()).getByTestId('context-ring-arc').dataset.tone).toBe('warn')
  })
  it('one bar: a single bar for the first item, no second', () => {
    setup({ ...withIndicator({ shape: 'bar', data: ['cpu'] }), fast: fast({ cpu: 75 }) })
    expect(barOf('left').dataset.pct).toBe('75')
    expect(barOf('left').dataset.tone).toBe('warn')
    expect(within(bars()).queryByTestId('host-bar-right')).toBeNull()
    expect(screen.queryByTestId(`load-ring-${ID}`)).toBeNull()
  })
  it('two bars: the left item and the right item', () => {
    setup({ ...withIndicator({ shape: 'bars', data: ['cpu', 'pressure'] }), fast: fast({ cpu: 75, level: 2, mem: 30 }) })
    expect(barOf('left').dataset.pct).toBe('75')
    expect(Number(barOf('right').dataset.pct)).toBeCloseTo(200 / 3, 5)
    expect(barOf('right').dataset.tone).toBe('warn')
  })
  it('two bars with memory on the right', () => {
    setup({ ...withIndicator({ shape: 'bars', data: ['cpu', 'mem'] }), fast: fast({ cpu: 10, level: 4, mem: 91 }) })
    expect(barOf('right').dataset.pct).toBe('91')
  })
  it('slots with the worker off is a grey empty bar', () => {
    setup({ ...withIndicator({ shape: 'bar', data: ['slots'] }), worker: { enabled: false, slots: { heavy: 2, light: 4 }, running: { heavy: 0, light: 0 } } })
    expect(barOf('left').dataset.tone).toBe('muted')
    expect(within(barOf('left')).queryByTestId('host-bar-fill')).toBeNull()
  })
})

describe('HostCell — a host that is not ok (M17–M19)', () => {
  it('wwd_down: the badge lit, a bold yellow broken link where the indicator was, no ring', () => {
    setup(cellView({ liveness: 'wwd_down' }))
    expect(screen.getByTestId(`host-badge-dim-${ID}`).className).not.toContain('opacity')
    const icon = screen.getByTestId(`state-icon-${ID}`)
    expect(icon.dataset.state).toBe('wwd_down')
    expect(icon.querySelector('svg')!.getAttribute('class')).toContain('text-status-warning')
    expect(screen.queryByTestId(`load-ring-${ID}`)).toBeNull()
  })
  it('host_down: the badge at 40 %, a red power symbol', () => {
    setup(cellView({ liveness: 'host_down' }))
    expect(screen.getByTestId(`host-badge-dim-${ID}`).className).toContain('opacity-40')
    expect(screen.getByTestId(`state-icon-${ID}`).querySelector('svg')!.getAttribute('class')).toContain('text-status-error')
  })
  it('unknown: the badge at 60 %, a grey question mark', () => {
    setup(cellView({ liveness: 'unknown' }))
    expect(screen.getByTestId(`host-badge-dim-${ID}`).className).toContain('opacity-60')
    expect(screen.getByTestId(`state-icon-${ID}`).querySelector('svg')!.getAttribute('class')).toContain('text-text-muted')
  })
  it('an ok host has no state icon', () => {
    setup(cellView())
    expect(screen.queryByTestId(`state-icon-${ID}`)).toBeNull()
    expect(screen.getByTestId(`host-badge-dim-${ID}`).className).not.toContain('opacity')
  })
  it('the cell draws the count it is given (the model gives none to a host that is not ok)', () => {
    setup({ ...cellView({ liveness: 'host_down' }), count: { n: 3, tone: 'neutral' } })
    expect(screen.getByTestId(`count-badge-${ID}`)).toBeTruthy()
  })
  it('the accessible name carries the state word; an ok host also its zone', () => {
    setup(cellView({ liveness: 'wwd_down' }))
    expect(tile().getAttribute('aria-label')).toBe('mlab, wwd is not answering (the machine is up)')
  })
  it('an ok host\'s name: host, state, zone', () => {
    setup(cellView())
    expect(tile().getAttribute('aria-label')).toBe('mlab, OK, Normal')
  })
})

describe('HostCell — the count at the badge\'s corner (M6, M7)', () => {
  it('none when the model has none', () => {
    setup(cellView())
    expect(screen.queryByTestId(`count-badge-${ID}`)).toBeNull()
  })
  it.each([
    ['neutral', 'bg-text-muted'], ['warn', 'bg-status-warning'], ['error', 'bg-status-error'],
  ] as const)('%s count: its colour', (tone, cls) => {
    setup({ ...cellView(), count: { n: 4, tone } })
    const badge = screen.getByTestId(`count-badge-${ID}`)
    expect(badge.textContent).toBe('4')
    expect(badge.className).toContain(cls)
  })
  it('above 99 it reads 99+', () => {
    setup({ ...cellView(), count: { n: 120, tone: 'neutral' } })
    expect(screen.getByTestId(`count-badge-${ID}`).textContent).toBe('99+')
  })
  it('there is no breathing dot any more (U3): the running jobs are in the number', () => {
    setup({ ...cellView(), worker: { enabled: true, slots: { heavy: 2, light: 4 }, running: { heavy: 1, light: 0 } } })
    expect(screen.queryByTestId(`worker-running-${ID}`)).toBeNull()
  })
})

describe('HostCell — the cell', () => {
  it('shows the badge', () => {
    setup(cellView())
    expect(screen.getByTestId(`host-badge-${ID}`)).toBeTruthy()
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
