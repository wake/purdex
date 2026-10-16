import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, within, act } from '@testing-library/react'
import { HostCell, type HostCellProps } from './HostCell'
import { fast, host } from '../../lib/fleet/__fixtures__/builders'
import type { FleetHostView } from '../../lib/fleet/store'
import type { FleetQueue } from '../../lib/fleet/types'
import { useUISettingsStore } from '../../stores/useUISettingsStore'

const COLORS = { console: { main: { color: '#336699', alpha: 100 } } }
const queue = (n: number, over: Partial<FleetQueue> = {}): FleetQueue => ({
  heavy_used: 2, heavy_total: 2, light_used: 0, light_total: 4,
  holders: [],
  waiters: Array.from({ length: n }, (_, i) => ({ class: 'heavy', place: i + 1, command: 'make', project: 'p', session: '', since: 0 })),
  longest_wait_s: 30, admission_held: false, admission_reason: '', heavy_paused: false, light_paused: false, ...over,
})
const view = (over: Partial<FleetHostView> = {}): FleetHostView => ({ ...host('mlab', { name: 'mlab', fast: fast({ load: 40 }) }), ...over })

beforeEach(() => {
  useUISettingsStore.setState(useUISettingsStore.getInitialState())
})

function setup(v: FleetHostView, over: Partial<HostCellProps> = {}) {
  const props: HostCellProps = {
    view: v, look: { colors: COLORS, icon: 'Desktop' }, variant: 'wide', isActive: false, waitingPrimary: false, primaryElsewhere: false, onOpen: vi.fn(), ...over,
  }
  render(<HostCell {...props} />)
  return props
}
const tile = () => screen.getByTestId('host-tile-mlab')
const ring = () => screen.getByTestId('load-ring-mlab')
const arc = () => within(ring()).queryByTestId('context-ring-arc')
const hostBadge = () => screen.getByTestId('host-badge-mlab')
const badge = () => screen.queryByTestId('queue-badge-mlab')

describe('HostCell — the cell', () => {
  it('is a member bead\'s box: h-6 rounded-md gap-1, 3px each side, hover surface, selected surface', () => {
    const { rerender } = render(<HostCell view={view()} look={{ colors: COLORS }} variant="wide" isActive={false} waitingPrimary={false} primaryElsewhere={false} onOpen={() => {}} />)
    for (const c of ['group', 'h-6', 'rounded-md', 'gap-1', 'px-[3px]', 'hover:bg-surface-hover']) expect(tile().className.split(' ')).toContain(c)
    rerender(<HostCell view={view()} look={{ colors: COLORS }} variant="wide" isActive waitingPrimary={false} primaryElsewhere={false} onOpen={() => {}} />)
    expect(tile().className).toContain('bg-surface-active')
  })
})

describe('HostCell — the host badge (the App\'s HostBadge, as beside a member)', () => {
  it('is a HostBadge: the host colour\'s light layer behind, the icon in its middle layer (main on hover)', () => {
    setup(view())
    const b = hostBadge()
    expect(b.hasAttribute('data-host-badge')).toBe(true)
    expect(b.style.background).toBe('rgba(51, 102, 153, 0.22)')
    expect(b.style.getPropertyValue('--hb-main')).toBe('rgba(51, 102, 153, 1)')
    expect(b.style.getPropertyValue('--hb-middle')).toBe('rgba(51, 102, 153, 0.6)')
    expect(b.style.color).toBe('var(--hb-icon, var(--hb-middle))')
  })
  it('takes the sidebar badge settings (size 16, corner 4 by default)', () => {
    setup(view())
    expect(hostBadge().style.width).toBe('16px')
    expect(hostBadge().style.height).toBe('16px')
    expect(hostBadge().style.borderRadius).toBe('4px')
  })
  it('follows a changed sidebar badge size, and so does the ring', () => {
    setup(view())
    act(() => useUISettingsStore.setState({ hostBadgeSidebarBox: 20 } as never))
    expect(hostBadge().style.width).toBe('20px')
    expect(within(ring()).getByTestId('context-ring').style.width).toBe('20px')
  })
  it('a neutral line colour setting greys the icon, like the member badges', () => {
    useUISettingsStore.setState({ hostBadgeSidebarLineColor: 'neutral' } as never)
    setup(view())
    expect(hostBadge().style.color).toBe('var(--text-muted)')
  })
  it('resolves the colours as the badge beside an agent member does (terminal, falling back to console)', () => {
    setup(view(), { look: { colors: { ...COLORS, terminal: { main: { color: '#22c55e', alpha: 100 } } } } })
    expect(hostBadge().style.getPropertyValue('--hb-main')).toBe('rgba(34, 197, 94, 1)')
  })
  it('a host with no colour: the neutral badge (no background, muted icon)', () => {
    setup(view(), { look: {} })
    expect(hostBadge().dataset.hasColor).toBe('false')
    expect(hostBadge().style.background).toBe('')
  })
  it('an unknown icon name (from the wire) never shows as text', () => {
    setup(view(), { look: { colors: COLORS, icon: 'desktop' } })
    expect(hostBadge().textContent).toBe('')
  })
  it('offline: the badge dimmed, no hatch', () => {
    setup(view({ status: 'offline', fast: null }))
    expect(screen.getByTestId('host-badge-dim-mlab').className).toContain('opacity-40')
    expect(tile().innerHTML).not.toContain('repeating-linear-gradient')
    expect(hostBadge().style.background).toBe('rgba(51, 102, 153, 0.22)')
  })
  it('online (with or without a reading): not dimmed', () => {
    setup(view({ fast: null }))
    expect(screen.getByTestId('host-badge-dim-mlab').className).not.toContain('opacity-40')
  })
})

describe('HostCell — the ring (the App\'s ContextRing, as the member status)', () => {
  it('is a ContextRing the badge\'s size, like the team cell\'s', () => {
    setup(view())
    const r = within(ring()).getByTestId('context-ring')
    expect(r.style.width).toBe('16px')
    expect(r.querySelector('circle')!.getAttribute('stroke')).toBe('var(--border-default)')
    expect(r.querySelector('circle')!.getAttribute('stroke-width')).toBe('2.5')
  })
  it('its arc length follows the composite score', () => {
    setup(view({ fast: fast({ load: 50 }) }))
    expect(arc()!.dataset.shown).toBe('50')
    const [len, whole] = (arc()!.getAttribute('stroke-dasharray') ?? '').split(' ').map(Number)
    expect(len / whole).toBeCloseTo(0.5, 2)
  })
  it('a different score gives a different length', () => {
    setup(view({ fast: fast({ load: 80 }) }))
    const [len, whole] = (arc()!.getAttribute('stroke-dasharray') ?? '').split(' ').map(Number)
    expect(len / whole).toBeCloseTo(0.8, 2)
  })
  it('normal is the ok tone (green, like a healthy member ring)', () => {
    setup(view({ fast: fast({ load: 30, zone: 'normal' }) }))
    expect(arc()!.dataset.tone).toBe('ok')
    expect(arc()!.getAttribute('class')).toBe('stroke-status-success')
  })
  it('warning is the warn tone', () => {
    setup(view({ fast: fast({ load: 60, zone: 'warning' }) }))
    expect(arc()!.getAttribute('class')).toBe('stroke-status-warning')
  })
  it('its colour is the zone the daemon sent, not a function of the score', () => {
    // a high score in a normal zone stays green; a low score in a danger zone is red
    const { unmount } = render(<HostCell view={view({ fast: fast({ load: 95, zone: 'normal' }) })} look={{}} variant="wide" isActive={false} waitingPrimary={false} primaryElsewhere={false} onOpen={() => {}} />)
    expect(arc()!.getAttribute('class')).toBe('stroke-status-success')
    unmount()
    render(<HostCell view={view({ fast: fast({ load: 5, zone: 'danger' }) })} look={{}} variant="wide" isActive={false} waitingPrimary={false} primaryElsewhere={false} onOpen={() => {}} />)
    expect(arc()!.getAttribute('class')).toBe('stroke-status-error')
    expect(ring().dataset.zone).toBe('danger')
  })
  it('the middle is empty: a host has no model', () => {
    setup(view())
    const svg = within(ring()).getByTestId('context-ring').querySelector('svg')!
    expect(svg.querySelectorAll('g, path, text')).toHaveLength(0)
  })
  it('offline: the track only, no arc', () => {
    setup(view({ status: 'offline', fast: null }))
    expect(arc()).toBeNull()
    expect(within(ring()).getByTestId('context-ring').querySelectorAll('circle')).toHaveLength(1)
    expect(ring().dataset.zone).toBe('offline')
  })
  it('no reading yet: the track only, no arc', () => {
    setup(view({ fast: null }))
    expect(arc()).toBeNull()
    expect(ring().dataset.zone).toBe('nodata')
  })
  it('an unknown zone (the parser drops one; defensive) still draws the score\'s arc', () => {
    setup(view({ fast: { ...fast({ load: 50 }), zone: 'mystery' as never } }))
    expect(arc()!.dataset.shown).toBe('50')
  })
})

describe('HostCell — the queue badge', () => {
  const slow = (q: FleetQueue) => ({ at: 1, top_cpu: [], top_mem: [], queue: q })
  it('shows the waiter count on a daemon host that has waiters', () => {
    setup(view({ slow: slow(queue(2)) }))
    expect(badge()).toHaveTextContent('2')
  })
  it('sits on the host badge\'s corner (its positioned parent holds the host badge)', () => {
    setup(view({ slow: slow(queue(2)) }))
    const anchor = badge()!.parentElement!
    expect(anchor.className).toContain('relative')
    expect(anchor.contains(hostBadge())).toBe(true)
    expect(anchor.contains(ring())).toBe(false)
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
  it('names the host, the zone word, the five values and the queue', () => {
    setup(view({ name: 'mlab', fast: fast({ cpu: 12, mem: 61, level: 2, page_rate: 3, disk: 44, zone: 'warning', load: 66 }), slow: { at: 1, top_cpu: [], top_mem: [], queue: queue(2, { longest_wait_s: 45 }) } }))
    const tip = tile().getAttribute('title')!
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
    const tip = tile().getAttribute('title')!
    expect(tip).toContain('Offline')
    expect(tip).not.toContain('CPU')
  })
  it('says "Waiting for the primary" when the primary is waited for', () => {
    setup(view(), { waitingPrimary: true })
    expect(tile().getAttribute('title')).toContain('Waiting for the primary')
  })
  it('says "The primary is not on this App" when only a standby answered', () => {
    setup(view({ fast: null }), { primaryElsewhere: true })
    const tip = tile().getAttribute('title')!
    expect(tip).toContain('The primary is not on this App')
    expect(tip).not.toContain('Waiting for the primary')
  })
  it('a healthy cell mentions neither', () => {
    setup(view())
    expect(tile().getAttribute('title')).not.toContain('primary')
  })
  it('click calls onOpen', () => {
    const p = setup(view())
    fireEvent.click(tile())
    expect(p.onOpen).toHaveBeenCalledTimes(1)
  })
  it('the narrow variant has the same badge and ring, tighter', () => {
    setup(view(), { variant: 'narrow' })
    expect(hostBadge().style.width).toBe('16px')
    expect(within(ring()).getByTestId('context-ring')).toBeTruthy()
    expect(tile().dataset.variant).toBe('narrow')
    expect(tile().className.split(' ')).toContain('gap-0.5')
  })
})
