import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { HostCell, type HostCellProps } from './HostCell'
import { fast, host } from '../../lib/fleet/__fixtures__/builders'
import type { FleetHostView } from '../../lib/fleet/store'
import type { FleetQueue } from '../../lib/fleet/types'

const COLORS = { console: { main: { color: '#336699', alpha: 100 } } }
const queue = (n: number, over: Partial<FleetQueue> = {}): FleetQueue => ({
  heavy_used: 2, heavy_total: 2, light_used: 0, light_total: 4,
  waiters: Array.from({ length: n }, (_, i) => ({ class: 'heavy', place: i + 1, command: 'make', project: 'p', since: 0 })),
  longest_wait_s: 30, admission_held: false, ...over,
})
const view = (over: Partial<FleetHostView> = {}): FleetHostView => ({ ...host('mlab', { name: 'mlab', fast: fast({ load: 40 }) }), ...over })

function setup(v: FleetHostView, over: Partial<HostCellProps> = {}) {
  const props: HostCellProps = {
    view: v, look: { colors: COLORS, icon: 'Desktop' }, variant: 'wide', isActive: false, waitingPrimary: false, primaryElsewhere: false, onOpen: vi.fn(), ...over,
  }
  render(<HostCell {...props} />)
  return props
}
const ring = () => screen.getByTestId('load-ring-mlab')
const arc = () => ring().querySelector('circle[stroke-linecap="round"]')
const block = () => screen.getByTestId('host-block-mlab')
const badge = () => screen.queryByTestId('queue-badge-mlab')

describe('HostCell — the box', () => {
  it('is an 18px rounded-[4px] box in the host colour with a white icon', () => {
    setup(view())
    expect(block().className).toContain('rounded-[4px]')
    expect(block().style.width).toBe('18px')
    expect(block().style.height).toBe('18px')
    expect(block().style.backgroundColor).toBe('rgb(51, 102, 153)')
    expect(screen.getByTestId('host-block-icon-mlab').className).toContain('text-white/90')
    expect(screen.getByTestId('host-block-icon-mlab').className).toContain('group-hover:text-white')
  })
  it('is the TeamCell shape: h-6 rounded-md, hover surface, selected surface', () => {
    const { rerender } = render(<HostCell view={view()} look={{ colors: COLORS }} variant="wide" isActive={false} waitingPrimary={false} primaryElsewhere={false} onOpen={() => {}} />)
    const tile = screen.getByTestId('host-tile-mlab')
    expect(tile.className).toContain('h-6')
    expect(tile.className).toContain('rounded-md')
    expect(tile.className).toContain('hover:bg-surface-hover')
    rerender(<HostCell view={view()} look={{ colors: COLORS }} variant="wide" isActive waitingPrimary={false} primaryElsewhere={false} onOpen={() => {}} />)
    expect(screen.getByTestId('host-tile-mlab').className).toContain('bg-surface-active')
  })
  it('offline: the colour at 35% under a hatch, the icon at 35%', () => {
    setup(view({ status: 'offline', fast: null }))
    expect(block().style.backgroundColor).toContain('35%')
    expect(block().style.backgroundImage).toContain('repeating-linear-gradient')
    expect(screen.getByTestId('host-block-icon-mlab').className).toContain('opacity-35')
  })
  it('online has no hatch and no dimmed icon', () => {
    setup(view())
    expect(block().style.backgroundImage).toBe('')
    expect(screen.getByTestId('host-block-icon-mlab').className).not.toContain('opacity-35')
  })
})

describe('HostCell — the ring', () => {
  it('its arc length follows the composite score', () => {
    setup(view({ fast: fast({ load: 50 }) }))
    const [len, whole] = (arc()!.getAttribute('stroke-dasharray') ?? '').split(' ').map(Number)
    expect(len / whole).toBeCloseTo(0.5, 2)
    expect(ring().style.width).toBe('16px')
  })
  it('a different score gives a different length', () => {
    setup(view({ fast: fast({ load: 80 }) }))
    const [len, whole] = (arc()!.getAttribute('stroke-dasharray') ?? '').split(' ').map(Number)
    expect(len / whole).toBeCloseTo(0.8, 2)
  })
  it('its colour is the zone the daemon sent, not a function of the score', () => {
    // a high score but a normal zone stays neutral; a low score in a danger zone is red
    const { unmount } = render(<HostCell view={view({ fast: fast({ load: 95, zone: 'normal' }) })} look={{}} variant="wide" isActive={false} waitingPrimary={false} primaryElsewhere={false} onOpen={() => {}} />)
    expect(arc()!.getAttribute('class')).toContain('stroke-text-secondary')
    unmount()
    render(<HostCell view={view({ fast: fast({ load: 5, zone: 'danger' }) })} look={{}} variant="wide" isActive={false} waitingPrimary={false} primaryElsewhere={false} onOpen={() => {}} />)
    expect(arc()!.getAttribute('class')).toContain('stroke-status-error')
    expect(ring().dataset.zone).toBe('danger')
  })
  it('warning zone is status-warning', () => {
    setup(view({ fast: fast({ load: 60, zone: 'warning' }) }))
    expect(arc()!.getAttribute('class')).toContain('stroke-status-warning')
  })
  it('offline: a dashed track and no arc', () => {
    setup(view({ status: 'offline', fast: null }))
    expect(arc()).toBeNull()
    expect(ring().querySelector('circle')!.getAttribute('stroke-dasharray')).toBeTruthy()
    expect(ring().dataset.zone).toBe('offline')
  })
  it('no reading yet: dashed track, no arc, the box is not hatched', () => {
    setup(view({ fast: null }))
    expect(arc()).toBeNull()
    expect(block().style.backgroundImage).toBe('')
  })
})

describe('HostCell — the queue badge', () => {
  const slow = (q: FleetQueue) => ({ at: 1, top_cpu: [], top_mem: [], queue: q })
  it('shows the waiter count on a daemon host that has waiters', () => {
    setup(view({ slow: slow(queue(2)) }))
    expect(badge()).toHaveTextContent('2')
  })
  it('is absent at 0 waiters', () => {
    setup(view({ slow: slow(queue(0)) }))
    expect(badge()).toBeNull()
  })
  it('is absent on a probe host even with a queue', () => {
    setup(view({ kind: 'probe', slow: slow(queue(2)) }))
    expect(badge()).toBeNull()
  })
  it('is absent while offline', () => {
    setup(view({ status: 'offline', slow: slow(queue(2)) }))
    expect(badge()).toBeNull()
  })
  it('is neutral grey under 2 minutes and under 3 waiters', () => {
    setup(view({ slow: slow(queue(2, { longest_wait_s: 119 })) }))
    expect(badge()!.className).toContain('bg-text-muted')
  })
  it('turns warning from 2 minutes', () => {
    setup(view({ slow: slow(queue(1, { longest_wait_s: 120 })) }))
    expect(badge()!.className).toContain('bg-status-warning')
  })
  it('turns warning from 3 waiters', () => {
    setup(view({ slow: slow(queue(3)) }))
    expect(badge()!.className).toContain('bg-status-warning')
  })
  it('turns error while admission is held, over everything else', () => {
    setup(view({ slow: slow(queue(1, { admission_held: true })) }))
    expect(badge()!.className).toContain('bg-status-error')
  })
})

describe('HostCell — the tooltip and the click', () => {
  it('the accessible name carries the host, the zone word, the queue and the waiting notice', () => {
    setup(view({ name: 'mlab', fast: fast({ zone: 'warning' }), slow: { at: 1, top_cpu: [], top_mem: [], queue: queue(2, { longest_wait_s: 45 }) } }), { waitingPrimary: true })
    expect(screen.getByRole('button', { name: 'mlab, High, Queue: 2 waiting, longest 45 s, Waiting for the primary' })).toBeTruthy()
  })
  it('the accessible name says Offline / No data / the paused queue', () => {
    const { unmount } = render(<HostCell view={view({ status: 'offline', fast: null })} look={{}} variant="wide" isActive={false} waitingPrimary={false} primaryElsewhere={false} onOpen={() => {}} />)
    expect(screen.getByRole('button', { name: 'mlab, Offline' })).toBeTruthy()
    unmount()
    render(<HostCell view={view({ fast: null })} look={{}} variant="wide" isActive={false} waitingPrimary={false} primaryElsewhere={false} onOpen={() => {}} />)
    expect(screen.getByRole('button', { name: 'mlab, No data' })).toBeTruthy()
  })
  it('an unknown zone still draws a neutral arc', () => {
    setup(view({ fast: { ...fast({ load: 50 }), zone: 'mystery' as never } }))
    expect(arc()!.getAttribute('class')).toContain('stroke-text-secondary')
  })
  it('names the host, the zone word, the five values and the queue', () => {
    setup(view({ name: 'mlab', fast: fast({ cpu: 12, mem: 61, level: 2, page_rate: 3, disk: 44, zone: 'warning', load: 66 }), slow: { at: 1, top_cpu: [], top_mem: [], queue: queue(2, { longest_wait_s: 45 }) } }))
    const tip = screen.getByTestId('host-tile-mlab').getAttribute('title')!
    expect(tip).toContain('mlab')
    expect(tip).toContain('High')
    expect(tip).toContain('CPU 12%')
    expect(tip).toContain('Memory 61%')
    expect(tip).toContain('level 2')
    expect(tip).toContain('3 pages/s')
    expect(tip).toContain('44%')
    expect(tip).toContain('2 waiting')
    expect(tip).toContain('45 s')
  })
  it('says Offline for an offline host and shows no values', () => {
    setup(view({ status: 'offline', fast: null }))
    const tip = screen.getByTestId('host-tile-mlab').getAttribute('title')!
    expect(tip).toContain('Offline')
    expect(tip).not.toContain('CPU')
  })
  it('says "Waiting for the primary" when the primary is waited for', () => {
    setup(view(), { waitingPrimary: true })
    expect(screen.getByTestId('host-tile-mlab').getAttribute('title')).toContain('Waiting for the primary')
  })
  it('says "The primary is not on this App" when only a standby answered', () => {
    setup(view({ fast: null }), { primaryElsewhere: true })
    const tip = screen.getByTestId('host-tile-mlab').getAttribute('title')!
    expect(tip).toContain('The primary is not on this App')
    expect(tip).not.toContain('Waiting for the primary')
  })
  it('a healthy cell mentions neither', () => {
    setup(view())
    const tip = screen.getByTestId('host-tile-mlab').getAttribute('title')!
    expect(tip).not.toContain('primary')
  })
  it('click calls onOpen', () => {
    const p = setup(view())
    fireEvent.click(screen.getByTestId('host-tile-mlab'))
    expect(p.onOpen).toHaveBeenCalledTimes(1)
  })
  it('the narrow variant still has the box and the ring, tighter', () => {
    setup(view(), { variant: 'narrow' })
    expect(block()).toBeTruthy()
    expect(ring()).toBeTruthy()
    expect(screen.getByTestId('host-tile-mlab').dataset.variant).toBe('narrow')
  })
})
