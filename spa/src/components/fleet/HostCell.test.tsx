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
    setup({ ...cellView({ liveness: 'host_down' }), count: { n: 3, tone: 'neutral', queued: 3, running: 0 } })
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
    setup({ ...cellView(), count: { n: 4, tone, queued: 4, running: 0 } })
    const badge = screen.getByTestId(`count-badge-${ID}`)
    expect(badge.textContent).toBe('4')
    expect(badge.className).toContain(cls)
  })
  it('above 99 it reads 99+', () => {
    setup({ ...cellView(), count: { n: 120, tone: 'neutral', queued: 120, running: 0 } })
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
  it.each(['wide', 'narrow'] as const)('%s: the tile shows a ring for keyboard focus only (focus-visible), no outline on a click', (variant) => {
    setup(cellView(), { variant })
    expect(tile().className).toContain('focus-visible:ring-1')
    expect(tile().className).toContain('outline-none')
  })
  it('the tile is described by its open card, and by nothing while closed', () => {
    setup(cellView())
    expect(tile().getAttribute('aria-describedby')).toBeNull()
    fireEvent.focus(tile())
    const id = screen.getByTestId('host-card').id
    expect(id).not.toBe('')
    expect(tile().getAttribute('aria-describedby')).toBe(id)
    fireEvent.blur(tile())
    expect(tile().getAttribute('aria-describedby')).toBeNull()
  })
  it('two tiles never share a card id', () => {
    render(<><HostCell view={cellView()} variant="wide" isActive={false} onOpen={vi.fn()} /><HostCell view={cellView({ hostId: '0123456789abcdef', name: 'air26' })} variant="wide" isActive={false} onOpen={vi.fn()} /></>)
    fireEvent.focus(screen.getByTestId(`host-tile-${ID}`))
    const a = screen.getByTestId('host-card').id
    fireEvent.blur(screen.getByTestId(`host-tile-${ID}`))
    fireEvent.focus(screen.getByTestId('host-tile-0123456789abcdef'))
    expect(screen.getByTestId('host-card').id).not.toBe(a)
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

describe('HostCell — the narrow block (M14, M16)', () => {
  const narrow = (v: CellView) => setup(v, { variant: 'narrow' })
  const barsOf = () => [0, 1, 2, 3].map((i) => within(screen.getByTestId(`narrow-block-${ID}`)).getByTestId(`host-bar-${i}`))
  it('no host icon and no ring: four bars in order CPU, memory, pressure, disk, each in its band', () => {
    narrow(cellView({ fast: fast({ cpu: 20, mem: 75, level: 2, disk: 96 }) }))
    expect(screen.queryByTestId(`host-badge-${ID}`)).toBeNull()
    expect(screen.queryByTestId(`load-ring-${ID}`)).toBeNull()
    expect(screen.queryByTestId(`load-bars-${ID}`)).toBeNull()
    const bars = barsOf()
    expect(bars.map((b) => b.dataset.tone)).toEqual(['ok', 'warn', 'warn', 'danger'])
    expect(Number(bars[0].dataset.pct)).toBe(20)
  })
  it('the block sits on the tint of the host colour; a host without one on the neutral grey', () => {
    narrow(cellView())
    const tinted = screen.getByTestId(`narrow-block-${ID}`)
    expect(tinted.dataset.tinted).toBe('true')
    expect(tinted.style.background).not.toBe('')
    expect(tinted.className).not.toContain('bg-surface-hover')
  })
  it('no colour: bg-surface-hover', () => {
    narrow(cellView({ look: { ...cellView().look, colors: undefined, color: undefined } as CellView['look'] }))
    const plain = screen.getByTestId(`narrow-block-${ID}`)
    expect(plain.dataset.tinted).toBe('false')
    expect(plain.className).toContain('bg-surface-hover')
  })
  it('the count is on the block\'s top right and carries its colour', () => {
    narrow({ ...cellView(), count: { n: 4, tone: 'error', queued: 4, running: 0 } })
    const badge = within(screen.getByTestId(`narrow-block-${ID}`)).getByTestId(`count-badge-${ID}`)
    expect(badge.textContent).toBe('4')
    expect(badge.className).toContain('bg-status-error')
  })
  it.each([
    ['wwd_down', ''],
    ['host_down', 'opacity-40'],
    ['unknown', 'opacity-60'],
  ] as const)('%s: the state icon in the middle, no bars, dimmed %s (the model gives no count)', (liveness, dim) => {
    narrow(cellView({ liveness }))
    const icon = screen.getByTestId(`state-icon-${ID}`)
    expect(icon.dataset.state).toBe(liveness)
    expect(screen.queryByTestId('host-bars')).toBeNull()
    const dimmed = screen.getByTestId(`narrow-block-${ID}`)
    if (dim) expect(dimmed.className).toContain(dim)
    else expect(dimmed.className).not.toContain('opacity')
  })
  it('the dimming covers the tinted block itself, not only what is inside', () => {
    narrow(cellView({ liveness: 'host_down' }))
    const block = screen.getByTestId(`narrow-block-${ID}`)
    expect(block.dataset.tinted).toBe('true')
    expect(block.className).toContain('opacity-40')
    expect(within(block).getByTestId(`state-icon-${ID}`).className).not.toContain('opacity')
  })
  it('hover opens the card to the right; a click opens the host', () => {
    const p = narrow(cellView())
    fireEvent.mouseEnter(tile())
    expect(screen.getByTestId('host-card').getAttribute('data-placement')).toBe('right')
    fireEvent.click(tile())
    expect(p.onOpen).toHaveBeenCalledTimes(1)
  })
})
