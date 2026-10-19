// 7a-4a: the host tab's 「可接工作」 row — judgements (worker, what the host takes and why not, sessions room, owner, exceptions), not readings.
import { describe, it, expect, afterEach } from 'vitest'
import { render, screen, cleanup, within } from '@testing-library/react'
import { AvailRow } from './AvailRow'
import type { PoolCellExtra } from '../../lib/pool/cell-model'

afterEach(() => cleanup())

const live = (over: Partial<PoolCellExtra> = {}): PoolCellExtra => ({
  state: 'live', zone: 'normal', load: null,
  worker: { enabled: true, slots: { heavy: 2, light: 4 }, running: { heavy: 1, light: 0 } },
  headroom: { heavy: 1, light: 4 }, reasons: null, sessionsRoom: 3, owner: { active: false, idle_s: 120, reason: 'hid' }, psi: null, since: null, ...over,
})

describe('the 可接工作 row', () => {
  it('worker on: running over slots; takes; sessions room; owner away', () => {
    render(<AvailRow pool={live()} exceptions={[]} />)
    const row = screen.getByTestId('avail-row')
    expect(within(row).getByTestId('avail-worker').textContent).toContain('On · heavy 1/2 · light 0/4')
    expect(within(row).getByTestId('avail-headroom').textContent).toContain('heavy 1 · light 4')
    expect(within(row).getByTestId('avail-sessions').textContent).toContain('room for 3 more')
    expect(within(row).getByTestId('avail-owner').textContent).toContain('Away (idle 120 s)')
  })

  it('worker off says so', () => {
    render(<AvailRow pool={live({ worker: { enabled: false, slots: { heavy: 2, light: 4 }, running: { heavy: 0, light: 0 } }, headroom: { heavy: 0, light: 0 }, reasons: { heavy: ['worker_off'], light: ['worker_off'] } })} exceptions={[]} />)
    expect(screen.getByTestId('avail-worker').textContent).toContain('Off')
    expect(screen.getByTestId('avail-reason').textContent).toContain('Worker is off')
  })

  it('a class at 0 gives its reason; both the same → one line for both', () => {
    render(<AvailRow pool={live({ headroom: { heavy: 0, light: 0 }, reasons: { heavy: ['zone_warning', 'slots_full'], light: ['zone_warning', 'slots_full'] } })} exceptions={[]} />)
    const lines = screen.getAllByTestId('avail-reason')
    expect(lines).toHaveLength(1)
    expect(lines[0].textContent).toContain('heavy · light')
    expect(lines[0].textContent).toContain('Load is high · All slots are in use')
  })

  it('heavy and light with different reasons: two lines', () => {
    render(<AvailRow pool={live({ headroom: { heavy: 0, light: 0 }, reasons: { heavy: ['gate_full'], light: ['owner_active'] } })} exceptions={[]} />)
    expect(screen.getAllByTestId('avail-reason')).toHaveLength(2)
  })

  it('a daemon that sends no reasons: the plain 0, no reason made up', () => {
    render(<AvailRow pool={live({ headroom: { heavy: 0, light: 0 }, reasons: null })} exceptions={[]} />)
    expect(screen.getByTestId('avail-headroom').textContent).toContain('heavy 0 · light 0')
    expect(screen.queryByTestId('avail-reason')).toBeNull()
  })

  it('owner at the machine; no owner block for a host without owner rules', () => {
    const { rerender } = render(<AvailRow pool={live({ owner: { active: true, idle_s: 7, reason: 'hid' } })} exceptions={[]} />)
    expect(screen.getByTestId('avail-owner').textContent).toContain('At this machine (7 s idle)')
    rerender(<AvailRow pool={live({ owner: null })} exceptions={[]} />)
    expect(screen.queryByTestId('avail-owner')).toBeNull()
  })

  it('exceptions in force: the name, and until when or no end', () => {
    const until = new Date(2026, 9, 18, 5, 30).getTime()
    render(<AvailRow pool={live()} exceptions={[{ name: 'app-dev-package', since: 1, until }, { name: 'dev-server', since: 1, until: null }]} />)
    const ex = screen.getByTestId('avail-exceptions')
    expect(within(ex).getByTestId('avail-exception-app-dev-package').textContent).toMatch(/app-dev-package.*until/)
    expect(within(ex).getByTestId('avail-exception-dev-server').textContent).toContain('no end')
  })

  it('no exceptions: no exceptions block', () => {
    render(<AvailRow pool={live()} exceptions={[]} />)
    expect(screen.queryByTestId('avail-exceptions')).toBeNull()
  })

  it('wwd silent: one line saying so, no invented worker or headroom', () => {
    render(<AvailRow pool={live({ state: 'fallback', worker: null, headroom: null, sessionsRoom: null, owner: null, since: null })} exceptions={[]} />)
    expect(screen.getByTestId('avail-row').textContent).toContain('wwd is not answering')
    expect(screen.queryByTestId('avail-worker')).toBeNull()
  })

  it('offline: nothing', () => {
    const { container } = render(<AvailRow pool={live({ state: 'offline', worker: null, headroom: null })} exceptions={[]} />)
    expect(container.textContent).toBe('')
  })
})
