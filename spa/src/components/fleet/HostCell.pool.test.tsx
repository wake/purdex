import { describe, it, expect, vi, beforeEach } from 'vitest'
import { cleanup, render, screen, within } from '@testing-library/react'
import { HostCell } from './HostCell'
import { HostCard } from './HostCard'
import { poolCellModel } from '../../lib/pool/fleet-bridge'
import { poolHostView } from '../../lib/pool/host-view'
import { parseFacts, parseHostsAnswer } from '../../lib/pool/parse'
import darwinJson from '../../lib/pool/__fixtures__/facts_darwin.json'
import linuxJson from '../../lib/pool/__fixtures__/facts_linux.json'
import hostsJson from '../../lib/pool/__fixtures__/pool_hosts_response.json'
import type { PoolHost } from '../../lib/pool/types'
import { fast, host } from '../../lib/fleet/__fixtures__/builders'
import { useUISettingsStore } from '../../stores/useUISettingsStore'

const COLORS = { console: { main: { color: '#336699', alpha: 100 } } }
const darwin = parseFacts(darwinJson)!
const linux = parseFacts(linuxJson)!
const NOW = darwin.at + 5000
const base = parseHostsAnswer(hostsJson)![0]
const poolHost = (over: Partial<PoolHost> = {}): PoolHost => ({ ...base, alias: 'mlab', host_id: 'h-mlab', facts: darwin, reachable: true, ...over })
const fleetHost = (over: Record<string, unknown> = {}) => ({ ...host('mlab', { name: 'mlab', fast: fast({ load: 33 }) }), alias: 'mlab', ...over })
const factsWith = (over: Record<string, unknown>) => ({ ...darwin, ...over }) as typeof darwin
const workerOf = (enabled: boolean, running: { heavy: number; light: number }) => ({ ...darwin.worker, enabled, running })

function cell(ph: PoolHost | null, opts: { fleet?: ReturnType<typeof fleetHost>; poolLastOk?: number | null; fleetStale?: boolean } = {}) {
  const f = opts.fleet ?? fleetHost()
  return poolCellModel(f, poolHostView({ alias: 'mlab', pool: ph, fleet: f, fleetStale: opts.fleetStale ?? false, poolLastOk: opts.poolLastOk ?? null }, NOW), ph)
}
function show(m: ReturnType<typeof cell>) {
  render(<HostCell view={m.view} pool={m.pool} look={{ colors: COLORS, icon: 'Desktop' }} variant="wide" isActive={false} waitingPrimary={false} primaryElsewhere={false} onOpen={vi.fn()} />)
}
const dot = () => screen.queryByTestId('worker-running-mlab')
const ring = () => screen.getByTestId('load-ring-mlab')
const count = () => screen.queryByTestId('queue-badge-mlab')

beforeEach(() => { useUISettingsStore.setState(useUISettingsStore.getInitialState()) })

describe('HostCell on the pool — states', () => {
  it('live: the ring takes the daemon\'s score and the zone colour; the badge is not dimmed', () => {
    show(cell(poolHost({ load: { score: 91, driver: 'disk' } })))
    expect(ring().dataset.zone).toBe('normal')
    expect(screen.getByTestId('host-badge-dim-mlab').className).not.toContain('opacity-40')
  })

  it('live with the zone unknown: the ring keeps its length (the score) and is grey (design: 環照長度畫，但用灰色)', () => {
    show(cell(poolHost({ load: { score: 20, driver: 'cpu' }, facts: factsWith({ derived: { ...darwin.derived, zone: 'unknown' } }) })))
    expect(ring().dataset.zone).toBe('unknown')
    const arc = within(ring()).getByTestId('context-ring-arc')
    expect(arc.dataset.tone).toBe('muted')
    expect(arc.dataset.shown).toBe('20')
  })
  it('a zone colour is forced when the zone is known: warning paints the arc warn even at a low score', () => {
    show(cell(poolHost({ load: { score: 20, driver: 'cpu' }, facts: factsWith({ derived: { ...darwin.derived, zone: 'warning' } }) })))
    expect(within(ring()).getByTestId('context-ring-arc').dataset.tone).toBe('warn')
  })

  it('fallback: the fleet readings, not dimmed, no running dot', () => {
    show(cell(null))
    expect(ring().dataset.zone).toBe('normal')
    expect(screen.getByTestId('host-badge-dim-mlab').className).not.toContain('opacity-40')
    expect(dot()).toBeNull()
  })

  // 8a-3b (user: 要能看見斷線): one mark at the badge's bottom left while the pool does not read the host but the fleet does
  const linkMark = () => screen.queryByTestId('wwd-link-mlab')
  const tile = () => screen.getByTestId('host-tile-mlab')

  it('fallback, wwd unreachable: the link-break mark on the badge, the reason in the aria name, the badge not dimmed', () => {
    show(cell(null))
    expect(linkMark()).toBeTruthy()
    expect(linkMark()!.getAttribute('class')).toContain('text-status-warning')
    expect(linkMark()!.getAttribute('class')).toContain('-bottom-px')
    expect(linkMark()!.getAttribute('class')).toContain('-left-px')
    expect(tile().getAttribute('aria-label')).toContain('wwd is unreachable')
    expect(screen.getByTestId('host-badge-dim-mlab').className).not.toContain('opacity-40')
  })

  it('fallback, wwd answering with old facts: the same mark, the other reason in the aria name', () => {
    show(cell(poolHost({ facts: factsWith({ at: NOW - 60_000 }) })))
    expect(linkMark()).toBeTruthy()
    expect(tile().getAttribute('aria-label')).toContain('wwd answers, but its numbers stopped')
  })

  it('no mark while the pool reads the host, when the host is offline, or when the pool is not the source', () => {
    show(cell(poolHost()))
    expect(linkMark()).toBeNull()
    expect(tile().getAttribute('aria-label')).not.toContain('wwd')
    cleanup()
    show(cell(null, { fleet: fleetHost({ status: 'offline', fast: null }) }))
    expect(linkMark()).toBeNull()
    cleanup()
    const f = fleetHost()
    render(<HostCell view={f} pool={null} look={{ colors: COLORS, icon: 'Desktop' }} variant="wide" isActive={false} waitingPrimary={false} primaryElsewhere={false} onOpen={vi.fn()} />)
    expect(linkMark()).toBeNull()
  })

  // 88's review: the ring around the mark is the cell's own background (selected, hover, or the bar's), read from one variable on the tile
  it('the ring of the mark reads the cell background variable; the tile sets it per state (bar / hover / selected)', () => {
    show(cell(null))
    expect(linkMark()!.getAttribute('style')).toContain('var(--cell-bg)')
    expect(tile().className).toContain('[--cell-bg:var(--surface-tertiary)]')
    expect(tile().className).toContain('hover:[--cell-bg:var(--surface-hover)]')
    cleanup()
    const m = cell(null)
    render(<HostCell view={m.view} pool={m.pool} look={{ colors: COLORS, icon: 'Desktop' }} variant="wide" isActive waitingPrimary={false} primaryElsewhere={false} onOpen={vi.fn()} />)
    expect(tile().className).toContain('[--cell-bg:var(--surface-active)]')
    expect(tile().className).not.toContain('hover:[--cell-bg')
  })

  it('the mark does not take the bottom right (the running dot) or the top right (the queue count)', () => {
    show(cell(null))
    expect(linkMark()!.getAttribute('class')).not.toContain('-right-px')
    expect(linkMark()!.getAttribute('class')).not.toContain('-top-')
  })

  it('offline: the badge at 40 %, the ring track only', () => {
    show(cell(null, { fleet: fleetHost({ status: 'offline', fast: null }) }))
    expect(ring().dataset.zone).toBe('offline')
    expect(screen.getByTestId('host-badge-dim-mlab').className).toContain('opacity-40')
    expect(within(ring()).queryByTestId('context-ring-arc')).toBeNull()
  })
})

describe('HostCell on the pool — the running dot', () => {
  it('shows while heavy or light work runs here', () => {
    show(cell(poolHost({ facts: factsWith({ worker: workerOf(true, { heavy: 0, light: 1 }) }) })))
    expect(dot()).toBeTruthy()
    expect(dot()!.className).toContain('animate-breathe')
  })
  it('heavy running counts too', () => {
    show(cell(poolHost({ facts: factsWith({ worker: workerOf(true, { heavy: 1, light: 0 }) }) })))
    expect(dot()).toBeTruthy()
  })
  it('is absent when nothing runs', () => {
    show(cell(poolHost({ facts: factsWith({ worker: workerOf(true, { heavy: 0, light: 0 }) }) })))
    expect(dot()).toBeNull()
  })
  it('is absent on an offline host even if the last facts ran something', () => {
    show(cell(poolHost({ reachable: false, facts: factsWith({ worker: workerOf(true, { heavy: 1, light: 0 }) }) }), { fleet: fleetHost({ status: 'offline', fast: null }) }))
    expect(dot()).toBeNull()
  })
})

// The count at the badge's top right is left as it is today (the user, WWD-7 Q2: "to be reworked as a whole later"): facts' queued
// jobs add no number to it. On the pool a live host has no gate queue, so today's count does not show either.
describe('HostCell on the pool — the top-right count is left alone', () => {
  it('queued jobs in the facts draw no number', () => {
    show(cell(poolHost({ facts: factsWith({ queue: { queued: 4 } }) })))
    expect(count()).toBeNull()
  })
  it('no queue in the facts, none either', () => {
    show(cell(poolHost()))
    expect(count()).toBeNull()
  })
  it('a fallback host keeps today\'s gate-queue count', () => {
    const waiters = Array.from({ length: 2 }, (_, i) => ({ class: 'heavy', place: i + 1, command: 'make', project: 'p', session: '', since: 0 }))
    const slow = { at: 1, top_cpu: [], top_mem: [], queue: { heavy_used: 2, heavy_total: 2, light_used: 0, light_total: 4, holders: [], waiters, longest_wait_s: 30, admission_held: false, admission_reason: '', heavy_paused: false, light_paused: false } }
    show(cell(null, { fleet: fleetHost({ slow }) }))
    expect(count()).toHaveTextContent('2')
  })
})

describe('HostCard on the pool — the lower part', () => {
  const card = (m: ReturnType<typeof cell>) => {
    render(<HostCard view={m.view} pool={m.pool} waitingPrimary={false} primaryElsewhere={false} placement="above" anchor={null} />)
    return screen.getByTestId('host-card')
  }
  const rowText = (id: string) => screen.getByTestId(`host-card-pool-${id}`).textContent

  it('live: worker, headroom, sessions room', () => {
    card(cell(poolHost()))
    expect(rowText('worker')).toContain('On')
    expect(rowText('worker')).toContain('0/1')
    expect(rowText('headroom')).toContain('heavy 1')
    expect(rowText('headroom')).toContain('light 2')
    expect(rowText('sessions')).toContain('2')
  })

  it('the owner row only when facts name an owner', () => {
    card(cell(poolHost()))
    expect(rowText('owner')).toContain('42')
  })
  it('no owner row when facts carry none (Linux)', () => {
    card(cell(poolHost({ facts: linux })))
    expect(screen.queryByTestId('host-card-pool-owner')).toBeNull()
  })

  it('heavy at 0 gives its reason: the gate is full (air26 today)', () => {
    card(cell(poolHost({ facts: factsWith({ derived: { ...darwin.derived, headroom: { heavy: 0, light: 2 } } }), headroom_reasons: { heavy: ['gate_full'], light: [] } })))
    expect(rowText('headroom')).toContain("The gate's slots are held")
  })
  it('worker off: the reason, and the worker row says off', () => {
    card(cell(poolHost({ facts: factsWith({ worker: workerOf(false, { heavy: 0, light: 0 }), derived: { ...darwin.derived, headroom: { heavy: 0, light: 0 } } }), headroom_reasons: { heavy: ['worker_off'], light: ['worker_off'] } })))
    expect(rowText('worker')).toContain('Off')
    expect(rowText('headroom')).toContain('Worker is off')
  })
  it('owner active is a reason too', () => {
    card(cell(poolHost({ facts: factsWith({ derived: { ...darwin.derived, headroom: { heavy: 0, light: 2 } } }), headroom_reasons: { heavy: ['owner_active'], light: [] } })))
    expect(rowText('headroom')).toContain('The owner is at this machine')
  })
  it('an unknown reason code is shown as it is', () => {
    card(cell(poolHost({ facts: factsWith({ derived: { ...darwin.derived, headroom: { heavy: 0, light: 2 } } }), headroom_reasons: { heavy: ['from_the_future'], light: [] } })))
    expect(rowText('headroom')).toContain('from_the_future')
  })
  it('both classes at 0 for different reasons: each shows its own', () => {
    card(cell(poolHost({ facts: factsWith({ derived: { ...darwin.derived, headroom: { heavy: 0, light: 0 } } }), headroom_reasons: { heavy: ['slots_full'], light: ['owner_active'] } })))
    const reasons = screen.getAllByTestId('host-card-pool-reason').map((e) => e.textContent)
    expect(reasons).toEqual(['heavy: All slots are in use', 'light: The owner is at this machine'])
  })
  it('both classes at 0 for the same reason: shown once, saying it holds for both', () => {
    card(cell(poolHost({ facts: factsWith({ derived: { ...darwin.derived, headroom: { heavy: 0, light: 0 } } }), headroom_reasons: { heavy: ['worker_off'], light: ['worker_off'] } })))
    expect(screen.getAllByTestId('host-card-pool-reason').map((e) => e.textContent)).toEqual(['heavy · light: Worker is off'])
  })
  it('only light at 0: its reason shows, labelled light', () => {
    card(cell(poolHost({ facts: factsWith({ derived: { ...darwin.derived, headroom: { heavy: 1, light: 0 } } }), headroom_reasons: { heavy: [], light: ['slots_full'] } })))
    expect(screen.getAllByTestId('host-card-pool-reason').map((e) => e.textContent)).toEqual(['light: All slots are in use'])
  })
  it('only heavy at 0: its reason shows, labelled heavy', () => {
    card(cell(poolHost({ facts: factsWith({ derived: { ...darwin.derived, headroom: { heavy: 0, light: 2 } } }), headroom_reasons: { heavy: ['gate_full'], light: [] } })))
    expect(screen.getAllByTestId('host-card-pool-reason').map((e) => e.textContent)).toEqual(["heavy: The gate's slots are held"])
  })
  it('reasons sent but a class at 0 with an empty list is a contradiction the daemon should not make: shown as unknown, labelled', () => {
    card(cell(poolHost({ facts: factsWith({ derived: { ...darwin.derived, headroom: { heavy: 0, light: 2 } } }), headroom_reasons: { heavy: [], light: [] } })))
    expect(screen.getAllByTestId('host-card-pool-reason').map((e) => e.textContent)).toEqual(['heavy: Reason unknown'])
  })
  it('the same for light, and for both', () => {
    card(cell(poolHost({ facts: factsWith({ derived: { ...darwin.derived, headroom: { heavy: 0, light: 0 } } }), headroom_reasons: { heavy: [], light: [] } })))
    expect(screen.getAllByTestId('host-card-pool-reason').map((e) => e.textContent)).toEqual(['heavy · light: Reason unknown'])
  })
  it('a zero without reasons from the daemon is shown as 0, no reason invented', () => {
    card(cell(poolHost({ facts: factsWith({ derived: { ...darwin.derived, headroom: { heavy: 0, light: 2 } } }) })))
    expect(rowText('headroom')).toContain('heavy 0')
    expect(screen.getByTestId('host-card-pool-headroom').querySelector('[data-testid="host-card-pool-reason"]')).toBeNull()
  })

  it('the ring tag sits on the daemon\'s driver', () => {
    card(cell(poolHost({ load: { score: 91, driver: 'disk' } })))
    const tags = screen.getAllByTestId('host-card-ring-tag').map((e) => e.closest('[data-testid^="host-card-row-"]')!.getAttribute('data-testid'))
    expect(tags).toEqual(['host-card-row-disk'])
  })

  it('Linux: the pressure row carries PSI some10 / full10', () => {
    card(cell(poolHost({ facts: linux })))
    expect(screen.getByTestId('host-card-row-pressure').textContent).toContain('0.5')
  })

  it('the zone unknown is worded and muted', () => {
    card(cell(poolHost({ facts: linux })))
    expect(screen.getByTestId('host-card-zone').textContent).toBe('Unknown')
    expect(screen.getByTestId('host-card-zone').className).toContain('text-text-muted')
  })

  it('fallback: says the pool does not answer, with the time; worker and headroom unknown; the fleet readings stay', () => {
    card(cell(null, { poolLastOk: NOW - 60_000 }))
    expect(screen.getByTestId('host-card-pool-fallback').textContent).toContain('wwd is unreachable')
    expect(rowText('worker')).toContain('Unknown')
    expect(rowText('headroom')).toContain('Unknown')
    expect(screen.getByTestId('host-card-row-cpu')).toBeTruthy()
  })

  it('fallback with old facts from an answering wwd: the card says the numbers stopped, not that wwd is unreachable', () => {
    card(cell(poolHost({ facts: factsWith({ at: NOW - 60_000 }) }), { poolLastOk: NOW - 60_000 }))
    const line = screen.getByTestId('host-card-pool-fallback').textContent!
    expect(line).toContain('wwd answers, but its numbers stopped')
    expect(line).not.toContain('unreachable')
  })

  it('offline: the newer last-ok and no pool rows', () => {
    card(cell(null, { fleet: fleetHost({ status: 'offline', fast: null, last_ok_at: 1000 }), poolLastOk: 9000 }))
    expect(screen.queryByTestId('host-card-pool-worker')).toBeNull()
    expect(screen.getByTestId('host-card').textContent).toContain(new Date(9000).toLocaleString())
  })

  it('without a pool (capability absent) the card is today\'s: no pool rows', () => {
    render(<HostCard view={fleetHost()} pool={null} waitingPrimary={false} primaryElsewhere={false} placement="above" anchor={null} />)
    expect(screen.queryByTestId('host-card-pool-worker')).toBeNull()
  })
})
