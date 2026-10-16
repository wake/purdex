import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, screen, fireEvent, within } from '@testing-library/react'
import { FleetCells } from './FleetCells'
import { useFleetConfigStore } from '../../lib/fleet/config-store'
import { useFleetStore } from '../../lib/fleet/store'
import { fleetHistory } from '../../lib/fleet/history'
import { useHostStore } from '../../stores/useHostStore'
import { fast, host, primaryAnswer, standbyAnswer } from '../../lib/fleet/__fixtures__/builders'

const A = 'cfg-a'
const B = 'cfg-b'
const st = () => useFleetStore.getState()
const hostCfg = (id: string, daemonId: string) => ({ id, name: ({ 'cfg-a': 'air26', 'cfg-b': 'mlab' } as Record<string, string>)[id] ?? id, ip: '100.64.0.2', port: 7860, token: 't', order: 0, daemonId, colors: { console: { main: { color: '#336699', alpha: 100 } } } })

function answer(hostId: string, a: ReturnType<typeof primaryAnswer> | ReturnType<typeof standbyAnswer>) {
  const gen = st().begin(hostId)
  st().setSupport(hostId, gen, 'yes')
  st().applyFetched(hostId, gen, a)
}

const FOUR = [
  host('d-a', { name: 'air26', fast: fast({ load: 30 }) }),
  host('d-b', { name: 'mlab', fast: fast({ load: 92, zone: 'danger' }), slow: { at: 1, top_cpu: [], top_mem: [], queue: { heavy_used: 2, heavy_total: 2, light_used: 0, light_total: 4, waiters: [{ class: 'heavy', place: 1, command: 'make', project: 'p', since: 0 }], longest_wait_s: 1, admission_held: false } } }),
  host('p1', { kind: 'probe', name: 'air19', status: 'offline', fast: null }),
  host('p2', { kind: 'probe', name: 'mserv', fast: fast({ load: 10 }) }),
]

beforeEach(() => {
  st().reset()
  useFleetConfigStore.getState().reset()
  fleetHistory.clear()
  useHostStore.setState({ hosts: { [A]: hostCfg(A, 'd-a'), [B]: hostCfg(B, 'd-b') }, hostOrder: [A, B], activeHostId: A, runtime: {} } as never)
})

const props = () => ({ onOpenHost: vi.fn(), onOpenHosts: vi.fn() })

describe('FleetCells — the four states (storybook-style)', () => {
  it('normal, overloaded, offline and queued, side by side', () => {
    answer(A, primaryAnswer(1, FOUR))
    render(<FleetCells variant="wide" {...props()} />)
    expect(screen.getByTestId('load-ring-d-a').dataset.zone).toBe('normal')
    expect(within(screen.getByTestId('load-ring-d-a')).getByTestId('context-ring-arc').dataset.tone).toBe('ok')
    expect(screen.getByTestId('load-ring-d-b').dataset.zone).toBe('danger')
    expect(within(screen.getByTestId('load-ring-d-b')).getByTestId('context-ring-arc').dataset.tone).toBe('danger')
    expect(screen.getByTestId('load-ring-p1').dataset.zone).toBe('offline')
    expect(within(screen.getByTestId('load-ring-p1')).queryByTestId('context-ring-arc')).toBeNull()
    expect(screen.getByTestId('queue-badge-d-b')).toHaveTextContent('1')
    expect(screen.queryByTestId('queue-badge-d-a')).toBeNull()
  })
})

describe('FleetCells — the row', () => {
  it('is laid out like the member beads: wraps with the sidebar width, gap-0.5, no separators, daemon hosts first then the probes', () => {
    answer(A, primaryAnswer(1, FOUR))
    render(<FleetCells variant="wide" {...props()} />)
    const row = screen.getByTestId('fleet-cells')
    const cls = row.className.split(' ')
    for (const c of ['flex', 'flex-wrap', 'items-center', 'content-start', 'gap-0.5']) expect(cls).toContain(c)
    expect(cls).not.toContain('flex-nowrap')
    expect(cls).not.toContain('flex-col')
    expect(within(row).getAllByRole('button').map((b) => b.getAttribute('aria-label')?.split(', ')[0])).toEqual(['air26', 'mlab', 'air19', 'mserv'])
    expect(within(row).queryAllByTestId('cell-sep')).toHaveLength(0)
  })
  it('a daemon host\'s badge wears the App\'s host colours; a probe host\'s, the look it sent', () => {
    const probe = host('p3', { kind: 'probe', name: 'mserv', icon: 'Cpu', colors: { console: { main: { color: '#22c55e', alpha: 100 } } }, fast: fast({ load: 10 }) })
    answer(A, primaryAnswer(1, [...FOUR, probe]))
    render(<FleetCells variant="wide" {...props()} />)
    expect(screen.getByTestId('host-badge-d-a').style.getPropertyValue('--hb-main')).toBe('rgba(51, 102, 153, 1)')
    expect(screen.getByTestId('host-badge-p3').style.getPropertyValue('--hb-main')).toBe('rgba(34, 197, 94, 1)')
    expect(screen.getByTestId('host-badge-p1').dataset.hasColor).toBe('false')
  })
  it('the narrow variant stacks them in a column without separators', () => {
    answer(A, primaryAnswer(1, FOUR))
    render(<FleetCells variant="narrow" {...props()} />)
    const col = screen.getByTestId('fleet-cells')
    expect(col.className).toContain('flex-col')
    expect(within(col).getAllByRole('button')).toHaveLength(4)
    expect(within(col).queryAllByTestId('cell-sep')).toHaveLength(0)
  })
  it('renders nothing before any fleet is known', () => {
    render(<FleetCells variant="wide" {...props()} />)
    expect(screen.queryByTestId('fleet-cells')).toBeNull()
  })
})

describe('FleetCells — clicks', () => {
  it('a daemon host opens that host on the Hosts page', () => {
    answer(A, primaryAnswer(1, FOUR))
    const p = props()
    render(<FleetCells variant="wide" {...p} />)
    fireEvent.click(screen.getByTestId('host-tile-d-b'))
    expect(p.onOpenHost).toHaveBeenCalledWith(B)
    expect(p.onOpenHosts).not.toHaveBeenCalled()
  })
  it('the probe click opens the primary\'s overview: with B as the primary it is onOpenHost(B)', () => {
    answer(B, primaryAnswer(1, FOUR))
    const p = props()
    render(<FleetCells variant="wide" {...p} />)
    fireEvent.click(screen.getByTestId('host-tile-p1'))
    expect(p.onOpenHost).toHaveBeenCalledWith(B)
    expect(useFleetConfigStore.getState().focusProbe).toBe('p1')
  })
  it('a probe host opens the Hosts page on the primary\'s overview and asks the probe section to highlight its row', () => {
    answer(A, primaryAnswer(1, FOUR))
    const p = props()
    render(<FleetCells variant="wide" {...p} />)
    fireEvent.click(screen.getByTestId('host-tile-p2'))
    expect(p.onOpenHost).toHaveBeenCalledWith(A)
    expect(useFleetConfigStore.getState().focusProbe).toBe('p2')
    expect(p.onOpenHosts).not.toHaveBeenCalled()
  })
})

describe('FleetCells — the two waiting tooltips', () => {
  it('a primary that went quiet: the last values stay and say "Waiting for the primary"', () => {
    answer(A, primaryAnswer(1, FOUR))
    st().checkStale(Date.now() + 60_000)
    render(<FleetCells variant="wide" {...props()} />)
    expect(screen.getByTestId('host-tile-d-a').getAttribute('title')).toContain('Waiting for the primary')
  })
  it('only a standby answered: the App hosts show without values and say the primary is not on this App', () => {
    answer(A, standbyAnswer(1, 'air26'))
    render(<FleetCells variant="wide" {...props()} />)
    const tip = screen.getByTestId('host-tile-cfg-a').getAttribute('title')!
    expect(tip).toContain('The primary is not on this App')
    expect(screen.getByTestId('load-ring-cfg-a').dataset.zone).toBe('nodata')
    expect(within(screen.getByTestId('fleet-cells')).getAllByRole('button')).toHaveLength(2)
  })
})
