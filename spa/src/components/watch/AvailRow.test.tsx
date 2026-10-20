// 7a-4a: the host tab's 「可接工作」 row — judgements (worker, what the host takes and why not, sessions room, owner, exceptions), not readings.
import { describe, it, expect, afterEach } from 'vitest'
import { render, screen, cleanup, within } from '@testing-library/react'
import { AvailRow } from './AvailRow'
import type { PoolCellExtra } from '../../lib/pool/fleet-bridge'

afterEach(() => cleanup())

const AT = Date.UTC(2026, 9, 18, 5, 0)
const live = (over: Partial<PoolCellExtra> = {}): PoolCellExtra => ({
  state: 'live', zone: 'normal', load: null,
  worker: { enabled: true, slots: { heavy: 2, light: 4 }, running: { heavy: 1, light: 0 } },
  headroom: { heavy: 1, light: 4 }, reasons: null, sessionsRoom: 3, owner: { active: false, idle_s: 120, reason: 'hid' }, psi: null, since: null, reason: null, ...over,
})

describe('the 可接工作 row', () => {
  it('worker on: running over slots; takes; sessions room; owner away', () => {
    render(<AvailRow pool={live()} exceptions={[]} at={AT} />)
    const row = screen.getByTestId('avail-row')
    expect(within(row).getByTestId('avail-worker').textContent).toContain('On · heavy 1/2 · light 0/4')
    expect(within(row).getByTestId('avail-headroom').textContent).toContain('heavy 1 · light 4')
    expect(within(row).getByTestId('avail-sessions').textContent).toContain('room for 3 more')
    expect(within(row).getByTestId('avail-owner').textContent).toContain('Away (idle 120 s)')
  })

  it('worker off says so', () => {
    render(<AvailRow pool={live({ worker: { enabled: false, slots: { heavy: 2, light: 4 }, running: { heavy: 0, light: 0 } }, headroom: { heavy: 0, light: 0 }, reasons: { heavy: ['worker_off'], light: ['worker_off'] } })} exceptions={[]} at={AT} />)
    expect(screen.getByTestId('avail-worker').textContent).toContain('Off')
    expect(screen.getByTestId('avail-reason').textContent).toContain('Worker is off')
  })

  it('a class at 0 gives its reason; both the same → one line for both', () => {
    render(<AvailRow pool={live({ headroom: { heavy: 0, light: 0 }, reasons: { heavy: ['zone_warning', 'slots_full'], light: ['zone_warning', 'slots_full'] } })} exceptions={[]} at={AT} />)
    const lines = screen.getAllByTestId('avail-reason')
    expect(lines).toHaveLength(1)
    expect(lines[0].textContent).toContain('heavy · light')
    expect(lines[0].textContent).toContain('Load is high · All slots are in use')
  })

  it('heavy and light with different reasons: two lines', () => {
    render(<AvailRow pool={live({ headroom: { heavy: 0, light: 0 }, reasons: { heavy: ['gate_full'], light: ['owner_active'] } })} exceptions={[]} at={AT} />)
    expect(screen.getAllByTestId('avail-reason')).toHaveLength(2)
  })

  it('a daemon that sends no reasons: the plain 0, no reason made up', () => {
    render(<AvailRow pool={live({ headroom: { heavy: 0, light: 0 }, reasons: null })} exceptions={[]} at={AT} />)
    expect(screen.getByTestId('avail-headroom').textContent).toContain('heavy 0 · light 0')
    expect(screen.queryByTestId('avail-reason')).toBeNull()
  })

  it('owner at the machine; no owner block for a host without owner rules', () => {
    const { rerender } = render(<AvailRow pool={live({ owner: { active: true, idle_s: 7, reason: 'hid' } })} exceptions={[]} at={AT} />)
    expect(screen.getByTestId('avail-owner').textContent).toContain('At this machine (7 s idle)')
    rerender(<AvailRow pool={live({ owner: null })} exceptions={[]} at={AT} />)
    expect(screen.queryByTestId('avail-owner')).toBeNull()
  })

  it('exceptions in force: the name, and until when or no end', () => {
    const until = AT + 1800_000
    render(<AvailRow pool={live()} exceptions={[{ name: 'app-dev-package', since: 1, until }, { name: 'dev-server', since: 1, until: null }]} at={AT} />)
    const ex = screen.getByTestId('avail-exceptions')
    expect(within(ex).getByTestId('avail-exception-app-dev-package').textContent).toMatch(/app-dev-package.*until/)
    expect(within(ex).getByTestId('avail-exception-dev-server').textContent).toContain('no end')
  })

  it('an exception whose end is already past (by the facts\' own time) is not in force', () => {
    render(<AvailRow pool={live()} exceptions={[{ name: 'old-one', since: AT - 7200_000, until: AT - 60_000 }, { name: 'on-now', since: AT - 60_000, until: AT + 3600_000 }]} at={AT} />)
    expect(screen.queryByTestId('avail-exception-old-one')).toBeNull()
    expect(screen.getByTestId('avail-exception-on-now')).toBeTruthy()
  })

  it('an exception that has not begun yet is not in force, with or without an end; one that begins exactly now is', () => {
    render(<AvailRow pool={live()} exceptions={[
      { name: 'later', since: AT + 60_000, until: AT + 3600_000 },
      { name: 'later-open', since: AT + 60_000, until: null },
      { name: 'right-now', since: AT, until: null },
    ]} at={AT} />)
    expect(screen.queryByTestId('avail-exception-later')).toBeNull()
    expect(screen.queryByTestId('avail-exception-later-open')).toBeNull()
    expect(screen.getByTestId('avail-exception-right-now')).toBeTruthy()
  })

  it('all exceptions over: no exceptions block', () => {
    render(<AvailRow pool={live()} exceptions={[{ name: 'old-one', since: 1, until: AT - 1 }]} at={AT} />)
    expect(screen.queryByTestId('avail-exceptions')).toBeNull()
  })

  it('no exceptions: no exceptions block', () => {
    render(<AvailRow pool={live()} exceptions={[]} at={AT} />)
    expect(screen.queryByTestId('avail-exceptions')).toBeNull()
  })

  it('wwd unreachable: one line saying so, no invented worker or headroom', () => {
    render(<AvailRow pool={live({ state: 'fallback', reason: 'unreachable', worker: null, headroom: null, sessionsRoom: null, owner: null, since: null })} exceptions={[]} at={AT} />)
    expect(screen.getByTestId('avail-row').textContent).toContain('wwd is unreachable')
    expect(screen.queryByTestId('avail-worker')).toBeNull()
  })

  it('wwd answering with numbers that stopped: its own line, not "unreachable"', () => {
    render(<AvailRow pool={live({ state: 'fallback', reason: 'stale', worker: null, headroom: null, sessionsRoom: null, owner: null, since: null })} exceptions={[]} at={AT} />)
    const line = screen.getByTestId('avail-row').textContent!
    expect(line).toContain('wwd answers, but its numbers stopped')
    expect(line).not.toContain('unreachable')
  })

  it('a fallback line carries since when the pool last read the host', () => {
    render(<AvailRow pool={live({ state: 'fallback', reason: 'unreachable', worker: null, headroom: null, sessionsRoom: null, owner: null, since: AT - 60_000 })} exceptions={[]} at={AT} />)
    expect(screen.getByTestId('avail-row').textContent).toMatch(/wwd is unreachable \(since .+\)/)
  })

  it('offline: nothing', () => {
    const { container } = render(<AvailRow pool={live({ state: 'offline', worker: null, headroom: null })} exceptions={[]} at={AT} />)
    expect(container.textContent).toBe('')
  })
})
