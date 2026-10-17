import { describe, it, expect } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import { HostCard } from './HostCard'
import { fast, host } from '../../lib/fleet/__fixtures__/builders'
import type { FleetHostView } from '../../lib/fleet/store'
import type { FleetQueue } from '../../lib/fleet/types'

const queue = (n: number, over: Partial<FleetQueue> = {}): FleetQueue => ({
  heavy_used: 1, heavy_total: 2, light_used: 3, light_total: 4,
  holders: [],
  waiters: Array.from({ length: n }, (_, i) => ({ class: 'heavy', place: i + 1, command: 'make', project: 'p', session: '', since: 0 })),
  longest_wait_s: 125, admission_held: false, admission_reason: '', heavy_paused: false, light_paused: false, ...over,
})
const view = (over: Partial<FleetHostView> = {}): FleetHostView => ({ ...host('mlab', { name: 'mlab', fast: fast({ load: 40 }) }), ...over })

function show(v: FleetHostView, p: { waitingPrimary?: boolean; primaryElsewhere?: boolean; placement?: 'above' | 'right' } = {}) {
  render(<HostCard view={v} waitingPrimary={p.waitingPrimary ?? false} primaryElsewhere={p.primaryElsewhere ?? false} placement={p.placement ?? 'above'} anchor={null} />)
  return screen.getByTestId('host-card')
}
const row = (k: string) => screen.getByTestId(`host-card-row-${k}`)
const driver = () => screen.queryAllByTestId('host-card-ring-tag').map((e) => e.closest('[data-testid^="host-card-row-"]')!.getAttribute('data-testid'))


describe('HostCard — content', () => {
  it('normal: name, zone word in the zone colour, five readings with their tones', () => {
    const c = show(view({ fast: fast({ cpu: 12, mem: 75, level: 1, page_rate: 3, disk: 44, disk_free_gb: 120, zone: 'normal' }) }))
    expect(within(c).getByText('mlab')).toBeTruthy()
    expect(within(c).getByTestId('host-card-zone').textContent).toBe('Normal')
    expect(within(c).getByTestId('host-card-zone').className).toContain('text-status-success-text')
    expect(row('cpu').textContent).toContain('12%')
    expect(row('cpu').innerHTML).not.toContain('status-')
    expect(row('mem').textContent).toContain('75%')
    expect(row('mem').innerHTML).toContain('text-status-warning-text')
    expect(row('pressure').textContent).toContain('Normal')
    expect(row('paging').textContent).toContain('3 pages/s')
    expect(row('disk').textContent).toContain('44%')
    expect(row('disk').textContent).toContain('120 GB free')
  })
  it('disk: no free-space note when disk_free_gb is unknown', () => {
    show(view({ fast: fast({ disk_free_gb: 0 }) }))
    expect(row('disk').textContent).not.toContain('free')
  })
  it('warning with a queue and admission held', () => {
    const c = show(view({ fast: fast({ cpu: 95, level: 4, page_rate: 900, zone: 'danger', load: 100 }), slow: { at: 1, top_cpu: [], top_mem: [], queue: queue(2, { admission_held: true }) } }))
    expect(within(c).getByTestId('host-card-zone').textContent).toBe('Overloaded')
    expect(row('cpu').innerHTML).toContain('text-status-error-text')
    expect(row('pressure').textContent).toContain('Critical')
    expect(row('pressure').innerHTML).toContain('text-status-error-text')
    expect(row('paging').innerHTML).toContain('text-status-error-text')
    expect(within(c).getByTestId('host-card-slots').textContent).toBe('Heavy 1/2 · Light 3/4')
    expect(within(c).getByTestId('host-card-queue').textContent).toBe('Queue 2 · longest 2:05')
    expect(within(within(c).getByTestId('host-card-queue')).getByText('2').className).toContain('text-status-warning-text')
    expect(within(c).getByTestId('host-card-held').className).toContain('text-status-error-text')
  })
  it('queue of 0: no warning colour, no longest, no held line', () => {
    const c = show(view({ slow: { at: 1, top_cpu: [], top_mem: [], queue: queue(0, { longest_wait_s: 0 }) } }))
    expect(within(c).getByTestId('host-card-queue').textContent).toBe('Queue 0')
    expect(within(within(c).getByTestId('host-card-queue')).getByText('0').className).not.toContain('warning')
    expect(within(c).queryByTestId('host-card-held')).toBeNull()
  })
  it('a probe host has no queue section', () => {
    const c = show(view({ kind: 'probe', slow: { at: 1, top_cpu: [], top_mem: [], queue: queue(1) } }))
    expect(within(c).queryByTestId('host-card-slots')).toBeNull()
    expect(within(c).queryByTestId('host-card-queue')).toBeNull()
  })
  it('unknown level (0) shows a dash', () => {
    show(view({ fast: fast({ level: 0 }) }))
    expect(row('pressure').textContent).toContain('—')
  })
  it('offline: last successful reading instead of the values', () => {
    const t = Date.UTC(2026, 9, 16, 3, 4, 5)
    const c = show(view({ status: 'offline', fast: null, last_ok_at: t, slow: { at: 1, top_cpu: [], top_mem: [], queue: queue(1) } }))
    expect(within(c).getByTestId('host-card-zone').textContent).toBe('Offline')
    expect(c.textContent).toContain(`Last successful reading: ${new Date(t).toLocaleString()}`)
    expect(within(c).queryByTestId('host-card-row-cpu')).toBeNull()
    expect(within(c).queryByTestId('host-card-queue')).toBeNull()
  })
  it('offline hides the values even when a last reading is still held', () => {
    const c = show(view({ status: 'offline', fast: fast({ cpu: 77 }) }))
    expect(within(c).queryByTestId('host-card-row-cpu')).toBeNull()
  })
  it('no reading yet: the no-data word', () => {
    const c = show(view({ fast: null }))
    expect(within(c).getByTestId('host-card-zone').textContent).toBe('No data')
    expect(within(c).queryByTestId('host-card-row-cpu')).toBeNull()
  })
  it('the notices come last', () => {
    const c = show(view(), { waitingPrimary: true })
    expect(c.lastElementChild!.textContent).toBe('Waiting for the primary')
  })
  it('the primary-elsewhere notice comes last too', () => {
    const d = show(view({ fast: null }), { primaryElsewhere: true })
    expect(d.lastElementChild!.textContent).toBe('The primary is not on this App')
  })
  it('no notice when healthy', () => {
    const c = show(view())
    expect(c.textContent).not.toContain('primary')
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
    show(view({ fast: fast({ ...base, ...over }) }))
    expect(driver()).toEqual([`host-card-row-${key}`])
    expect(row(key).className).toContain('font-semibold')
  })
  it('a tie marks the first', () => {
    show(view({ fast: fast({ ...base, cpu: 75, level: 2 }) }))
    expect(driver()).toEqual(['host-card-row-cpu'])
  })
  it('an offline host marks nothing', () => {
    show(view({ status: 'offline', fast: null }))
    expect(driver()).toEqual([])
  })
})

describe('HostCard — placement', () => {
  it('above: absolute-style anchor above the strip', () => {
    expect(show(view(), { placement: 'above' }).getAttribute('data-placement')).toBe('above')
  })
  it('right: to the right of the cell', () => {
    expect(show(view(), { placement: 'right' }).getAttribute('data-placement')).toBe('right')
  })
  it('is the prototype card: 220px, elevated, no pointer events', () => {
    const c = show(view())
    expect(c.className).toContain('w-[220px]')
    expect(c.className).toContain('bg-surface-elevated')
    expect(c.className).toContain('pointer-events-none')
  })
})
