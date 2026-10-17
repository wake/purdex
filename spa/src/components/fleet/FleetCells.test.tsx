import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, screen, fireEvent, within, act } from '@testing-library/react'
import { FleetCells } from './FleetCells'
import { useUISettingsStore } from '../../stores/useUISettingsStore'
import { useFleetConfigStore } from '../../lib/fleet/config-store'
import { openWatchArea } from '../../features/workspace/lib/open-watch-area'
import { useFleetStore } from '../../lib/fleet/store'
import { fleetHistory } from '../../lib/fleet/history'
import { useHostStore } from '../../stores/useHostStore'
import { useLayoutStore } from '../../stores/useLayoutStore'
import { fast, host, primaryAnswer, standbyAnswer } from '../../lib/fleet/__fixtures__/builders'

vi.mock('../../features/workspace/lib/open-watch-area', () => ({ openWatchArea: vi.fn() }))

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
  host('d-b', { name: 'mlab', fast: fast({ load: 92, zone: 'danger' }), slow: { at: 1, top_cpu: [], top_mem: [], queue: { heavy_used: 2, heavy_total: 2, light_used: 0, light_total: 4, holders: [], waiters: [{ class: 'heavy', place: 1, command: 'make', project: 'p', session: '', since: 0 }], longest_wait_s: 1, admission_held: false, admission_reason: '', heavy_paused: false, light_paused: false } } }),
  host('p1', { kind: 'probe', name: 'air19', status: 'offline', fast: null }),
  host('p2', { kind: 'probe', name: 'mserv', fast: fast({ load: 10 }) }),
]

beforeEach(() => {
  useUISettingsStore.setState(useUISettingsStore.getInitialState())
  st().reset()
  useFleetConfigStore.getState().reset()
  fleetHistory.clear()
  useHostStore.setState({ hosts: { [A]: hostCfg(A, 'd-a'), [B]: hostCfg(B, 'd-b') }, hostOrder: [A, B], activeHostId: A, runtime: {} } as never)
})

const props = () => ({})

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

describe('FleetCells — the indicator setting (主機格指示)', () => {
  it('switching it redraws every cell, wide and narrow, with no remount needed', () => {
    answer(A, primaryAnswer(1, FOUR))
    const { rerender } = render(<FleetCells variant="wide" />)
    expect(screen.getAllByTestId(/^load-ring-/)).toHaveLength(4)
    act(() => useUISettingsStore.getState().setFleetCellIndicator('bars-pressure'))
    expect(screen.queryAllByTestId(/^load-ring-/)).toHaveLength(0)
    expect(screen.getAllByTestId(/^load-bars-/)).toHaveLength(4)
    rerender(<FleetCells variant="narrow" />)
    expect(screen.getAllByTestId(/^load-bars-/)).toHaveLength(4)
    act(() => useUISettingsStore.getState().setFleetCellIndicator('ring'))
    expect(screen.getAllByTestId(/^load-ring-/)).toHaveLength(4)
  })
})

describe('FleetCells — the row', () => {
  // The gap between hosts narrows (journal v5 U9, §3.6): gap-0 from the beads' gap-0.5.
  it('is laid out like the member beads: wraps with the sidebar width, no gap between cells, no separators, daemon hosts first then the probes', () => {
    answer(A, primaryAnswer(1, FOUR))
    render(<FleetCells variant="wide" {...props()} />)
    const row = screen.getByTestId('fleet-cells')
    const cls = row.className.split(' ')
    for (const c of ['flex', 'flex-wrap', 'items-center', 'content-start', 'gap-0']) expect(cls).toContain(c)
    for (const c of ['gap-0.5', 'gap-1', 'gap-px']) expect(cls).not.toContain(c)
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
  // jsdom lays nothing out, so the sum is taken from the classes: a cell is 3 px + badge + gap-1 + ml-0.5 + indicator + 3 px.
  // At gap-px the fifth cell would wrap at the default width (5 × 44 + 4 = 224 > 223), which is why the gap is 0, not 1 px.
  it('five cells still fit one line at the default sidebar width', () => {
    const FIVE = [...FOUR, host('p3', { kind: 'probe', name: 'p3', fast: fast({ load: 5 }) })]
    answer(A, primaryAnswer(1, FIVE))
    render(<FleetCells variant="wide" {...props()} />)
    const spacing = (el: Element, prefix: string) => {
      for (const c of el.className.split(' ')) {
        if (c === `${prefix}-px`) return 1
        const arb = c.match(new RegExp(`^${prefix}-\\[(\\d+)px\\]$`))
        if (arb) return Number(arb[1])
        const step = c.match(new RegExp(`^${prefix}-(\\d+(?:\\.\\d+)?)$`))
        if (step) return Number(step[1]) * 4
      }
      return 0
    }
    const row = screen.getByTestId('fleet-cells')
    const cells = within(row).getAllByRole('button')
    expect(cells).toHaveLength(5)
    const cell = cells[0]
    const ring = within(cell).getByTestId('load-ring-d-a')
    const box = parseFloat(within(cell).getByTestId('host-badge-d-a').style.width)
    const ringBox = parseFloat(within(ring).getByTestId('context-ring').style.width)
    const cellW = 2 * spacing(cell, 'px') + box + spacing(cell, 'gap') + spacing(ring, 'ml') + ringBox
    expect(cellW).toBe(44)
    const bar = useLayoutStore.getInitialState().activityBarWideSize
    const room = bar - 1 /* the bar's border-r */ - 2 * spacing(row, 'px')
    expect(5 * cellW + 4 * spacing(row, 'gap')).toBeLessThanOrEqual(room)
  })
  it('the narrow variant stacks them in a column without separators, no gap between cells', () => {
    answer(A, primaryAnswer(1, FOUR))
    render(<FleetCells variant="narrow" {...props()} />)
    const col = screen.getByTestId('fleet-cells')
    expect(col.className).toContain('flex-col')
    expect(col.className.split(' ')).toContain('gap-0')
    for (const c of ['gap-0.5', 'gap-1', 'gap-px']) expect(col.className.split(' ')).not.toContain(c)
    expect(within(col).getAllByRole('button')).toHaveLength(4)
    expect(within(col).queryAllByTestId('cell-sep')).toHaveLength(0)
  })
  it('renders nothing before any fleet is known', () => {
    render(<FleetCells variant="wide" {...props()} />)
    expect(screen.queryByTestId('fleet-cells')).toBeNull()
  })
})

describe('FleetCells — clicks', () => {
  beforeEach(() => vi.mocked(openWatchArea).mockClear())

  it('a daemon host opens its tab in the watcher area, by the fleet host id', () => {
    answer(A, primaryAnswer(1, FOUR))
    render(<FleetCells variant="wide" {...props()} />)
    fireEvent.click(screen.getByTestId('host-tile-d-b'))
    expect(openWatchArea).toHaveBeenCalledWith({ host: 'd-b' })
  })
  it('a probe host opens its tab too, and no longer asks the probe section to highlight a row', () => {
    answer(A, primaryAnswer(1, FOUR))
    render(<FleetCells variant="wide" {...props()} />)
    fireEvent.click(screen.getByTestId('host-tile-p2'))
    expect(openWatchArea).toHaveBeenCalledWith({ host: 'p2' })
  })
})

describe('FleetCells — the two waiting tooltips', () => {
  it('a primary that went quiet: the last values stay and say "Waiting for the primary"', () => {
    answer(A, primaryAnswer(1, FOUR))
    st().checkStale(Date.now() + 60_000)
    render(<FleetCells variant="wide" {...props()} />)
    fireEvent.mouseEnter(screen.getByTestId('host-tile-d-a'))
    expect(screen.getByTestId('host-card').textContent).toContain('Waiting for the primary')
  })
  it('only a standby answered: the App hosts show without values and say the primary is not on this App', () => {
    answer(A, standbyAnswer(1, 'air26'))
    render(<FleetCells variant="wide" {...props()} />)
    fireEvent.mouseEnter(screen.getByTestId('host-tile-cfg-a'))
    expect(screen.getByTestId('host-card').textContent).toContain('The primary is not on this App')
    expect(screen.getByTestId('load-ring-cfg-a').dataset.zone).toBe('nodata')
    expect(within(screen.getByTestId('fleet-cells')).getAllByRole('button')).toHaveLength(2)
  })
})
