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
const driver = () => screen.queryAllByTestId('host-card-ring-tag').map((e) => e.closest('[data-testid^="host-card-row-"]')!.getAttribute('data-testid'))

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
    expect(driver()).toEqual([])
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

describe('HostCard — the ring-driver mark', () => {
  const base = { cpu: 10, mem: 10, level: 0, page_rate: 0, disk: 10 }
  it.each([
    ['cpu', { cpu: 60 }],
    ['mem', { mem: 60 }],
    ['pressure', { level: 2 }],
    ['paging', { page_rate: 90 }],
    ['disk', { disk: 95 }],
  ])('%s can be the driver, and only that row is marked', (key, over) => {
    show(cellView({ fast: fast({ ...base, ...over }) }))
    expect(driver()).toEqual([`host-card-row-${key}`])
    expect(row(key).className).toContain('font-semibold')
  })
  it('the daemon\'s own driver wins over the one worked out', () => {
    show(cellView({ fast: fast({ ...base, cpu: 60 }), load: { score: 90, driver: 'disk' } }))
    expect(driver()).toEqual(['host-card-row-disk'])
  })
  it('a tie marks the first', () => {
    show(cellView({ fast: fast({ ...base, cpu: 75, level: 2 }) }))
    expect(driver()).toEqual(['host-card-row-cpu'])
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
