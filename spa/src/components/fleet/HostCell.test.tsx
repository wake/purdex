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
const bars = () => screen.getByTestId('load-bars-mlab')
const barOf = (side: 'left' | 'right') => within(bars()).getByTestId(`host-bar-${side}`)
const fillOf = (side: 'left' | 'right') => within(barOf(side)).queryByTestId('host-bar-fill')

// 「主機格指示」: the setting picks what sits after the badge — the ring, or two bars (CPU + memory pressure / memory used).
describe('HostCell — the indicator setting', () => {
  it('default: the ring, no bars', () => {
    setup(view())
    expect(ring()).toBeTruthy()
    expect(screen.queryByTestId('load-bars-mlab')).toBeNull()
  })
  it('bars-pressure: two bars in the ring\'s place and the ring\'s size; left CPU, right the pressure level', () => {
    useUISettingsStore.setState({ fleetCellIndicator: 'bars-pressure' })
    setup(view({ fast: fast({ cpu: 75, level: 2, mem: 30, zone: 'warning' }) }))
    expect(screen.queryByTestId('load-ring-mlab')).toBeNull()
    expect(within(bars()).queryByTestId('context-ring')).toBeNull()
    expect(within(bars()).getByTestId('host-bars').style.width).toBe('16px')
    expect(bars().dataset.zone).toBe('warning')
    expect(barOf('left').dataset.pct).toBe('75')
    expect(barOf('left').dataset.tone).toBe('warn')
    expect(Number(barOf('right').dataset.pct)).toBeCloseTo(200 / 3, 5)
    expect(barOf('right').dataset.tone).toBe('warn')
  })
  it('bars-pressure: an unknown level (0) falls back to memory used %', () => {
    useUISettingsStore.setState({ fleetCellIndicator: 'bars-pressure' })
    setup(view({ fast: fast({ cpu: 10, level: 0, mem: 91 }) }))
    expect(barOf('right').dataset.pct).toBe('91')
    expect(barOf('right').dataset.tone).toBe('danger')
  })
  it('bars-memory: the right bar is memory used %, whatever the level', () => {
    useUISettingsStore.setState({ fleetCellIndicator: 'bars-memory' })
    setup(view({ fast: fast({ cpu: 30, level: 1, mem: 82 }) }))
    expect(barOf('left').dataset.pct).toBe('30')
    expect(barOf('left').dataset.tone).toBe('ok')
    expect(barOf('right').dataset.pct).toBe('82')
    expect(barOf('right').dataset.tone).toBe('warn')
  })
  it('bars, offline: grey tracks, no fill; the badge still dimmed', () => {
    useUISettingsStore.setState({ fleetCellIndicator: 'bars-memory' })
    setup(view({ status: 'offline', fast: null }))
    expect(fillOf('left')).toBeNull()
    expect(fillOf('right')).toBeNull()
    expect(bars().dataset.zone).toBe('offline')
    expect(screen.getByTestId('host-badge-dim-mlab').className).toContain('opacity-40')
  })
  it('bars, no reading yet: tracks only', () => {
    useUISettingsStore.setState({ fleetCellIndicator: 'bars-pressure' })
    setup(view({ fast: null }))
    expect(fillOf('left')).toBeNull()
    expect(fillOf('right')).toBeNull()
  })
  it('bars keep the queue count on the badge\'s corner', () => {
    useUISettingsStore.setState({ fleetCellIndicator: 'bars-pressure' })
    setup(view({ slow: { at: 1, top_cpu: [], top_mem: [], queue: queue(2) } }))
    expect(badge()).toHaveTextContent('2')
    expect(badge()!.parentElement!.contains(hostBadge())).toBe(true)
  })
  it('the narrow variant follows the same setting', () => {
    useUISettingsStore.setState({ fleetCellIndicator: 'bars-memory' })
    setup(view(), { variant: 'narrow' })
    expect(bars()).toBeTruthy()
    expect(screen.queryByTestId('load-ring-mlab')).toBeNull()
  })
  it('a corrupt stored value draws the ring', () => {
    useUISettingsStore.setState({ fleetCellIndicator: 'pie' as never })
    setup(view())
    expect(ring()).toBeTruthy()
  })
})

describe('HostCell — the cell', () => {
  it('is a member bead\'s box: h-6 rounded-md gap-1, 3px each side, hover surface, selected surface', () => {
    const { rerender } = render(<HostCell view={view()} look={{ colors: COLORS }} variant="wide" isActive={false} waitingPrimary={false} primaryElsewhere={false} onOpen={() => {}} />)
    for (const c of ['group', 'h-6', 'rounded-md', 'gap-1', 'px-[3px]', 'hover:bg-surface-hover']) expect(tile().className.split(' ')).toContain(c)
    rerender(<HostCell view={view()} look={{ colors: COLORS }} variant="wide" isActive waitingPrimary={false} primaryElsewhere={false} onOpen={() => {}} />)
    expect(tile().className).toContain('bg-surface-active')
  })

  it('draws no click / focus outline (user 10-17)', () => {
    render(<HostCell view={view()} look={{ colors: COLORS }} variant="wide" isActive={false} waitingPrimary={false} primaryElsewhere={false} onOpen={() => {}} />)
    expect(tile().className.split(' ')).toContain('outline-none')
  })
})

describe('HostCell — the host badge (the App\'s HostBadge, as beside a member)', () => {
  it('is a HostBadge: the host colour\'s light layer behind, the icon always in its main layer', () => {
    setup(view())
    const b = hostBadge()
    expect(b.hasAttribute('data-host-badge')).toBe(true)
    expect(b.style.background).toBe('rgba(51, 102, 153, 0.22)')
    expect(b.style.getPropertyValue('--hb-main')).toBe('rgba(51, 102, 153, 1)')
    expect(b.style.getPropertyValue('--hb-middle')).toBe('rgba(51, 102, 153, 0.6)')
    expect(b.style.color).toBe('var(--hb-main)')
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
    expect(hostBadge().style.color).toBe('var(--text-secondary)')
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
  it('has no native title (the hover card is the only tooltip), keeps the aria-label', () => {
    setup(view())
    expect(tile().hasAttribute('title')).toBe(false)
    expect(tile().getAttribute('aria-label')).toBe('mlab, Normal')
  })
  it('no card until the pointer enters; enter shows it, leave hides it', () => {
    setup(view())
    expect(screen.queryByTestId('host-card')).toBeNull()
    fireEvent.mouseEnter(tile())
    expect(screen.getByTestId('host-card')).toBeTruthy()
    fireEvent.mouseLeave(tile())
    expect(screen.queryByTestId('host-card')).toBeNull()
  })
  it('closes the card on a scroll of any ancestor, and on a window resize', () => {
    setup(view())
    fireEvent.mouseEnter(tile())
    expect(screen.getByTestId('host-card')).toBeTruthy()
    fireEvent.scroll(document.body)
    expect(screen.queryByTestId('host-card')).toBeNull()
    fireEvent.mouseEnter(tile())
    expect(screen.getByTestId('host-card')).toBeTruthy()
    fireEvent(window, new Event('resize'))
    expect(screen.queryByTestId('host-card')).toBeNull()
  })
  it('scroll / resize listeners are gone once the card is closed', () => {
    const add = vi.spyOn(window, 'addEventListener')
    const rm = vi.spyOn(window, 'removeEventListener')
    setup(view())
    fireEvent.mouseEnter(tile())
    expect(add.mock.calls.some((c) => c[0] === 'scroll')).toBe(true)
    fireEvent.mouseLeave(tile())
    expect(rm.mock.calls.some((c) => c[0] === 'scroll')).toBe(true)
    expect(rm.mock.calls.some((c) => c[0] === 'resize')).toBe(true)
    add.mockRestore(); rm.mockRestore()
  })
  it('one card at a time: focus on cell A then hover on cell B shows only B\'s card', () => {
    const mk = (id: string) => ({ ...host(id, { name: id, fast: fast({ load: 40 }) }) }) as FleetHostView
    render(<>
      {[mk('a'), mk('b')].map((v) => (
        <HostCell key={v.id} view={v} look={{ colors: COLORS, icon: 'Desktop' }} variant="wide" isActive={false} waitingPrimary={false} primaryElsewhere={false} onOpen={vi.fn()} />
      ))}
    </>)
    fireEvent.focus(screen.getByTestId('host-tile-a'))
    expect(screen.getAllByTestId('host-card')).toHaveLength(1)
    fireEvent.mouseEnter(screen.getByTestId('host-tile-b'))
    const cards = screen.getAllByTestId('host-card')
    expect(cards).toHaveLength(1)
    expect(within(cards[0]).getByText('b')).toBeTruthy()
  })
  it('keyboard focus shows the card, blur hides it', () => {
    setup(view())
    fireEvent.focus(tile())
    expect(screen.getByTestId('host-card')).toBeTruthy()
    fireEvent.blur(tile())
    expect(screen.queryByTestId('host-card')).toBeNull()
  })
  it('a click hides the card and opens', () => {
    const p = setup(view())
    fireEvent.mouseEnter(tile())
    fireEvent.click(tile())
    expect(screen.queryByTestId('host-card')).toBeNull()
    expect(p.onOpen).toHaveBeenCalledTimes(1)
  })
  it('the card carries this cell\'s host, readings and the waiting-primary notice', () => {
    setup(view({ name: 'mlab', fast: fast({ cpu: 12, zone: 'warning' }), slow: { at: 1, top_cpu: [], top_mem: [], queue: queue(2, { longest_wait_s: 45 }) } }), { waitingPrimary: true })
    fireEvent.mouseEnter(tile())
    const c = screen.getByTestId('host-card')
    expect(c.textContent).toContain('mlab')
    expect(c.textContent).toContain('12%')
    expect(c.textContent).toContain('Waiting for the primary')
    expect(c.textContent).toContain('Queue 2')
  })
  it('wide: the card sits above the strip; narrow: to the right of the cell', () => {
    const { unmount } = render(<HostCell view={view()} look={{}} variant="wide" isActive={false} waitingPrimary={false} primaryElsewhere={false} onOpen={() => {}} />)
    fireEvent.mouseEnter(tile())
    expect(screen.getByTestId('host-card').getAttribute('data-placement')).toBe('above')
    unmount()
    render(<HostCell view={view()} look={{}} variant="narrow" isActive={false} waitingPrimary={false} primaryElsewhere={false} onOpen={() => {}} />)
    fireEvent.mouseEnter(tile())
    expect(screen.getByTestId('host-card').getAttribute('data-placement')).toBe('right')
  })
  it('the icon is always bright: a coloured host uses its main layer, a plain one text-secondary; offline keeps the dimming', () => {
    setup(view())
    expect(hostBadge().style.color).toBe('var(--hb-main)')
  })
  it('a plain (uncoloured) host: the brighter neutral, not muted', () => {
    setup(view(), { look: {} })
    expect(hostBadge().style.color).toBe('var(--text-secondary)')
  })
  it('an offline host stays dimmed on top of the bright icon', () => {
    setup(view({ status: 'offline', fast: null }))
    expect(screen.getByTestId('host-badge-dim-mlab').className).toContain('opacity-40')
    expect(hostBadge().style.color).toBe('var(--hb-main)')
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
