import { describe, it, expect } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import { HostCard } from './HostCard'
import { cellView, fast, FACTS } from './cell-view.fixture'
import type { CellView } from '../../lib/pool/cell-model'

function show(v: CellView, placement: 'above' | 'right' = 'above') {
  render(<HostCard view={v} placement={placement} anchor={null} />)
  return screen.getByTestId('host-card')
}
const row = (k: string) => screen.getByTestId(`host-card-row-${k}`)

describe('HostCard — an ok host', () => {
  it('name, zone word in the zone colour, five readings with their tones', () => {
    const c = show(cellView({ fast: fast({ cpu: 12, mem: 75, level: 1, page_rate: 3, disk: 44, disk_free_gb: 120 }) }))
    expect(within(c).getByText('mlab')).toBeTruthy()
    expect(within(c).getByTestId('host-card-zone').textContent).toBe('Normal')
    expect(within(c).getByTestId('host-card-zone').className).toContain('text-status-success-text')
    expect(row('cpu').textContent).toContain('12%')
    expect(row('cpu').innerHTML).not.toContain('status-')
    expect(row('mem').innerHTML).toContain('text-status-warning-text')
    expect(row('pressure').textContent).toContain('Normal')
    expect(row('paging').textContent).toContain('3 pages/s')
    expect(row('disk').textContent).toContain('44%')
    expect(row('disk').textContent).toContain('120 GB free')
  })
  it('the pool\'s zone word wins over the reading\'s, including unknown', () => {
    const c = show(cellView({ zone: 'unknown' }))
    expect(within(c).getByTestId('host-card-zone').textContent).toBe('Unknown')
  })
  it('unknown level (0) shows a dash', () => {
    show(cellView({ fast: fast({ level: 0 }) }))
    expect(row('pressure').textContent).toContain('—')
  })
  it('no queue section and no primary notice: the sidebar does not read the fleet', () => {
    const c = show(cellView())
    expect(within(c).queryByTestId('host-card-slots')).toBeNull()
    expect(within(c).queryByTestId('host-card-queue')).toBeNull()
    expect(c.textContent).not.toContain('primary')
  })
  it('the pool\'s rows: worker, headroom with the reason at 0, owner, sessions room, PSI', () => {
    const c = show(cellView({
      worker: { enabled: true, slots: { heavy: 2, light: 4 }, running: { heavy: 1, light: 0 } },
      headroom: { heavy: 0, light: 3 }, reasons: { heavy: ['memory'], light: [] },
      owner: { active: true, idle_s: 5 } as never, sessionsRoom: 7, psi: { some10: 1.5, full10: 0.25 },
    }))
    expect(within(c).getByTestId('host-card-pool-worker').textContent).toContain('1')
    expect(within(c).getByTestId('host-card-pool-headroom')).toBeTruthy()
    expect(within(c).getAllByTestId('host-card-pool-reason').length).toBeGreaterThan(0)
    expect(within(c).getByTestId('host-card-pool-owner')).toBeTruthy()
    expect(within(c).getByTestId('host-card-pool-sessions').textContent).toContain('7')
    expect(row('pressure').textContent).toContain('1.5')
  })
})

describe('HostCard — a host that is not ok (M21)', () => {
  it.each([
    ['wwd_down', "wwd is not answering (the machine is up)"],
    ['host_down', 'Host is offline'],
    ['unknown', 'State unknown'],
  ] as const)('%s: its state word in the headline, no readings, no pool rows', (liveness, word) => {
    const c = show(cellView({ liveness }))
    expect(within(c).getByTestId('host-card-zone').textContent).toBe(word)
    expect(within(c).queryByTestId('host-card-row-cpu')).toBeNull()
    expect(within(c).queryByTestId('host-card-pool-worker')).toBeNull()
    expect(within(c).queryByTestId('host-card-ring-tag')).toBeNull()
  })
  it('says why, when the last reading was and when it was last checked', () => {
    const c = show(cellView({ liveness: 'host_down', lastFactsAt: FACTS.at - 60_000 }))
    expect(within(c).getAllByTestId('host-card-liveness').length).toBeGreaterThanOrEqual(2)
    expect(c.textContent).toContain('Neither wwd nor port 22 answers')
  })
  it('a host whose facts stopped says so', () => {
    const c = show(cellView({ liveness: 'unknown', lastFactsAt: FACTS.at }))
    expect(within(c).getAllByTestId('host-card-liveness').length).toBeGreaterThan(0)
  })
})

describe('HostCard — the whole pool cannot be read (M25)', () => {
  it('says why and the last reading of this host, not the per-host explanation', () => {
    const c = show(cellView({ liveness: 'unknown', lastFactsAt: FACTS.at - 60_000, poolFailure: { code: 'wwd_unavailable', since: 1 } }))
    const lines = within(c).getAllByTestId('host-card-pool-outage').map((e) => e.textContent)
    expect(lines[0]).toBe('Cannot read the resource pool (Cannot read the pool: no wwd is running)')
    expect(lines[1]).toContain('Last reading')
    expect(within(c).queryByTestId('host-card-liveness')).toBeNull()
  })
  it('a failure with a word of its own, and none at all', () => {
    expect(within(show(cellView({ liveness: 'unknown', poolFailure: { code: 'wwd_bad_response', since: 1 } }))).getAllByTestId('host-card-pool-outage')[0].textContent).toContain('does not match the format')
  })
  it('no reading yet says so', () => {
    const c = show(cellView({ liveness: 'unknown', poolFailure: { code: null, since: null } }))
    expect(within(c).getAllByTestId('host-card-pool-outage')[1].textContent).toBe('No reading yet')
  })
})

describe('HostCard — the readings in M11 order', () => {
  it('load (with its cause), CPU, system load, memory (with GB), pressure, paging, swap, disk', () => {
    show(cellView({ fast: fast({ load: 62, load1: 4, ncpu: 8, mem: 40 }), load: { score: 62, driver: 'cpu' } }))
    const ids = screen.getAllByTestId(/^host-card-row-/).map((e) => e.getAttribute('data-testid'))
    expect(ids).toEqual(['load', 'cpu', 'sysload', 'mem', 'pressure', 'paging', 'swap', 'disk'].map((k) => `host-card-row-${k}`))
    expect(row('load').textContent).toContain('62%')
    expect(row('load').textContent).toContain('CPU')
    expect(row('sysload').textContent).toContain('4.0 / 8')
    expect(row('mem').textContent).toContain('6.0 GB')
    expect(row('swap').textContent).toContain('0.5 GB')
  })
  it('system load 70 warns, 100 is danger; no swap figure is a dash', () => {
    show(cellView({ fast: fast({ load1: 6, ncpu: 8 }), detail: { version: '1', memAvailGb: 1, swapUsedGb: null, local: false, checkedAt: null } }))
    expect(row('sysload').innerHTML).toContain('text-status-warning-text')
    expect(row('swap').textContent).toContain('—')
  })
})

describe('HostCard — the mark of what the indicator uses (M11)', () => {
  const marked = () => screen.queryAllByTestId('host-card-ring-tag').map((e) => e.parentElement!.closest('[data-testid^="host-card-"]')!.getAttribute('data-testid'))
  const look = (shape: 'ring' | 'bar' | 'bars', data: string[]) => ({ ...cellView().look, indicator: { shape, data } }) as CellView['look']
  it('a ring marks its item 環', () => {
    show(cellView({ look: look('ring', ['load']) }))
    expect(marked()).toEqual(['host-card-row-load'])
    expect(screen.getByTestId('host-card-ring-tag').textContent).toBe('Ring')
    expect(row('load').className).toContain('font-semibold')
    expect(row('cpu').className).not.toContain('font-semibold')
  })
  it('two bars mark both items 柱', () => {
    show(cellView({ look: look('bars', ['cpu', 'disk']) }))
    expect(marked()).toEqual(['host-card-row-cpu', 'host-card-row-disk'])
    expect(screen.getAllByTestId('host-card-ring-tag').map((e) => e.textContent)).toEqual(['Bar', 'Bar'])
  })
  it('slots marks the worker row', () => {
    show(cellView({ look: look('bar', ['slots']), worker: { enabled: true, slots: { heavy: 1, light: 1 }, running: { heavy: 0, light: 0 } } }))
    expect(marked()).toEqual(['host-card-pool-worker'])
  })
})

describe('HostCard — the title and the lower rows (M11)', () => {
  it('the title carries the host badge', () => {
    const c = show(cellView())
    expect(within(c).getByTestId('host-card-badge')).toBeTruthy()
  })
  it('jobs: what it sent and waits, and what runs on it', () => {
    show(cellView({ count: { n: 3, tone: 'neutral', queued: 2, running: 1 } }))
    expect(screen.getByTestId('host-card-pool-jobs').textContent).toContain('2')
    expect(screen.getByTestId('host-card-pool-jobs').textContent).toContain('1')
  })
  it('jobs with no count read 0 and 0', () => {
    show(cellView({ count: null }))
    expect(screen.getByTestId('host-card-pool-jobs').textContent).toBe('JobsWaiting 0 · running here 0')
  })
  it('wwd: version and the reading time', () => {
    show(cellView())
    expect(screen.getByTestId('host-card-pool-wwd').textContent).toContain('0.9.1')
  })
  it('link: ok with the last check; the local host says it is the local wwd', () => {
    show(cellView())
    expect(screen.getByTestId('host-card-pool-link').textContent).toContain('OK')
  })
  it('the local host', () => {
    show(cellView({ detail: { version: '1', memAvailGb: 1, swapUsedGb: 0, local: true, checkedAt: null } }))
    expect(screen.getByTestId('host-card-pool-link').textContent).toContain('local wwd')
  })
})

describe('HostCard — placement', () => {
  it('above and right', () => {
    expect(show(cellView(), 'above').getAttribute('data-placement')).toBe('above')
  })
  it('right: to the right of the cell', () => {
    expect(show(cellView(), 'right').getAttribute('data-placement')).toBe('right')
  })
  it('is the prototype card: 220px, elevated, no pointer events', () => {
    const c = show(cellView())
    expect(c.className).toContain('w-[220px]')
    expect(c.className).toContain('bg-surface-elevated')
    expect(c.className).toContain('pointer-events-none')
  })
})
