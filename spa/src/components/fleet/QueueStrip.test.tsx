import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, screen, fireEvent, within } from '@testing-library/react'
import { QueueStrip } from './QueueStrip'
import { usePoolStore } from '../../lib/pool/store'
import { useJobsStore } from '../../lib/pool/jobs-store'
import { useLayoutStore } from '../../stores/useLayoutStore'
import { openWatchArea } from '../../features/workspace/lib/open-watch-area'
import type { PoolJob } from '../../lib/pool/jobs-types'

vi.mock('../../features/workspace/lib/open-watch-area', () => ({ openWatchArea: vi.fn() }))

const NOW = new Date('2026-10-20T14:00:00Z').getTime()
const job = (id: string, origin: string, over: Partial<PoolJob> = {}): PoolJob => ({
  id, origin, state: 'queued', task: 'go-all', args: [], class: 'dev', labels: [], locks: [], manual: '', manual_seq: 0, claim: null, result: null,
  created_at: NOW - 40_000, repo: { name: 'purdex', root: '', remote: '' }, submitter: { session: '', ref: '', team: '', host: origin }, supersede_key: '', cacheable: true, interruptible: false, queue_timeout_s: 0, adhoc: false, local: false, timeout_s: 0, ...over,
}) as unknown as PoolJob
const hostRow = (alias: string) => ({ alias, host_id: alias, worker: { enabled: true, slots: { heavy: 2, light: 4 } }, labels: [], facts: null, reachable: true }) as never

function seed(jobs: PoolJob[], answered = true) {
  const byOrigin: Record<string, { jobs: Record<string, PoolJob>; cursor: string; epoch: string; via: string }> = {}
  for (const j of jobs) (byOrigin[j.origin] ??= { jobs: {}, cursor: '', epoch: '', via: '' }).jobs[j.id] = j
  useJobsStore.setState({ byOrigin } as never)
  usePoolStore.setState({ hosts: [hostRow('mlab'), hostRow('air26')], sourceHostId: answered ? 'cfg-a' : null, oldDaemon: false } as never)
}
const segs = () => screen.getAllByTestId(/^queue-seg-/).map((e) => e.getAttribute('data-kind'))

beforeEach(() => {
  vi.useFakeTimers()
  vi.setSystemTime(NOW)
  vi.mocked(openWatchArea).mockClear()
  useLayoutStore.setState({ queueStripSegments: 10 })
  usePoolStore.setState({ start: vi.fn(), stop: vi.fn() } as never)
})

describe('QueueStrip — the segments (M1–M4)', () => {
  it('3 queued: 3 lit (heavy purple, light teal) and 7 grey', () => {
    seed([job('a', 'mlab'), job('b', 'mlab', { slot: 'light' } as never), job('c', 'air26')])
    render(<QueueStrip />)
    expect(segs()).toEqual(['heavy', 'light', 'heavy', 'empty', 'empty', 'empty', 'empty', 'empty', 'empty', 'empty'])
    expect(screen.getByTestId('queue-seg-0').className).toContain('bg-queue-heavy')
    expect(screen.getByTestId('queue-seg-1').className).toContain('bg-queue-light')
    expect(screen.getByTestId('queue-seg-3').className).toContain('bg-border-default')
    expect(screen.queryByTestId('queue-overflow')).toBeNull()
  })
  it('14 queued: 10 lit and +4 on the last segment', () => {
    seed(Array.from({ length: 14 }, (_, i) => job(`j${i}`, 'mlab')))
    render(<QueueStrip />)
    expect(segs().every((k) => k === 'heavy')).toBe(true)
    expect(within(screen.getByTestId('queue-seg-9')).getByTestId('queue-overflow').textContent).toBe('+4')
  })
  it('a claimed job and a hold are not on the strip', () => {
    seed([job('a', 'mlab'), job('b', 'mlab', { state: 'running', claim: { worker: 'air26', attempt: 1, since: 0 } } as never), job('c', 'mlab', { manual: 'hold' } as never)])
    render(<QueueStrip />)
    expect(segs().filter((k) => k !== 'empty')).toHaveLength(1)
  })
  it('nothing queued: one grey line, still there (M4)', () => {
    seed([])
    render(<QueueStrip />)
    expect(screen.getByTestId('queue-strip')).toBeTruthy()
    expect(segs().every((k) => k === 'empty')).toBe(true)
    expect(segs()).toHaveLength(10)
  })
  it('the accessible name counts the waiting jobs', () => {
    seed([job('a', 'mlab'), job('b', 'mlab')])
    render(<QueueStrip />)
    expect(screen.getByTestId('queue-strip').getAttribute('aria-label')).toBe('Resource pool: 2 waiting')
  })
})

describe('QueueStrip — a pool nobody can read (M25)', () => {
  it('every source failed while the jobs store still holds 14 waiting jobs: all grey, no +N', () => {
    seed(Array.from({ length: 14 }, (_, i) => job(`j${i}`, 'mlab')), false)
    // the jobs page would keep these (hosts empty, not answered): make sure the strip is not that page
    usePoolStore.setState({ hosts: [], sourceHostId: null } as never)
    render(<QueueStrip />)
    expect(segs().every((k) => k === 'empty')).toBe(true)
    expect(screen.queryByTestId('queue-overflow')).toBeNull()
    expect(screen.getByTestId('queue-strip').getAttribute('aria-label')).toBe('Resource pool: 0 waiting')
    fireEvent.mouseEnter(screen.getByTestId('queue-strip'))
    expect(screen.getByTestId('queue-tooltip').textContent).toContain('Cannot read the resource pool')
  })
})

describe('QueueStrip — the hover list (M5)', () => {
  it('header, the rows (number, kind, job, source, wait), the hint', () => {
    seed([job('a', 'mlab', { args: ['./...'] }), job('b', 'air26', { slot: 'light', task: 'lint', created_at: NOW - 125_000 } as never)])
    render(<QueueStrip />)
    fireEvent.mouseEnter(screen.getByTestId('queue-strip'))
    const tip = screen.getByTestId('queue-tooltip')
    expect(tip.textContent).toContain('Resource pool: 2 waiting (heavy 1 · light 1)')
    const rows = within(tip).getAllByTestId('queue-row')
    expect(rows).toHaveLength(2)
    // the display order puts the job that waited longest first
    const mlab = rows.find((r) => r.textContent!.includes('mlab'))!
    const air = rows.find((r) => r.textContent!.includes('air26'))!
    expect(rows[0]).toBe(air)
    expect(air.textContent).toContain('1 purdex lint · air26 · 2:05')
    expect(mlab.textContent).toContain('2 purdex go-all ./... · mlab · 0:40')
    expect(within(mlab).getByTestId('queue-row-dot').className).toContain('bg-queue-heavy')
    expect(within(air).getByTestId('queue-row-dot').className).toContain('bg-queue-light')
    expect(tip.textContent).toContain('Click to open the job queue')
  })
  it('over 10 jobs: the first 10 and "… n more"', () => {
    seed(Array.from({ length: 14 }, (_, i) => job(`j${i}`, 'mlab')))
    render(<QueueStrip />)
    fireEvent.mouseEnter(screen.getByTestId('queue-strip'))
    const tip = screen.getByTestId('queue-tooltip')
    expect(within(tip).getAllByTestId('queue-row')).toHaveLength(10)
    expect(tip.textContent).toContain('… 4 more')
  })
  it('nothing waiting says so', () => {
    seed([])
    render(<QueueStrip />)
    fireEvent.mouseEnter(screen.getByTestId('queue-strip'))
    expect(screen.getByTestId('queue-tooltip').textContent).toContain('No job is waiting in the resource pool')
  })
  it('the card closes on leave', () => {
    seed([])
    render(<QueueStrip />)
    fireEvent.mouseEnter(screen.getByTestId('queue-strip'))
    fireEvent.mouseLeave(screen.getByTestId('queue-strip'))
    expect(screen.queryByTestId('queue-tooltip')).toBeNull()
  })
})

describe('QueueStrip — keyboard and screen readers', () => {
  it('focus opens the list and the button is described by it; blur closes it', () => {
    seed([job('a', 'mlab')])
    render(<QueueStrip />)
    const strip = screen.getByTestId('queue-strip')
    expect(strip.getAttribute('aria-describedby')).toBeNull()
    fireEvent.focus(strip)
    const tip = screen.getByTestId('queue-tooltip')
    expect(strip.getAttribute('aria-describedby')).toBe(tip.id)
    expect(tip.id).not.toBe('')
    fireEvent.blur(strip)
    expect(strip.getAttribute('aria-describedby')).toBeNull()
  })
  it('keeps a visible focus ring (the outline is off)', () => {
    seed([])
    render(<QueueStrip />)
    expect(screen.getByTestId('queue-strip').className).toContain('focus-visible:ring-1')
  })
  it('the unreadable pool is described too', () => {
    seed([], false)
    usePoolStore.setState({ hosts: [], sourceHostId: null } as never)
    render(<QueueStrip />)
    fireEvent.focus(screen.getByTestId('queue-strip'))
    expect(screen.getByTestId('queue-strip').getAttribute('aria-describedby')).toBe(screen.getByTestId('queue-tooltip').id)
  })
})

describe('QueueStrip — the segments setting (M2)', () => {
  it.each([6, 8, 12])('%s segments, and the overflow counts what does not fit', (nseg) => {
    useLayoutStore.setState({ queueStripSegments: nseg as never })
    seed(Array.from({ length: 14 }, (_, i) => job(`j${i}`, 'mlab')))
    render(<QueueStrip />)
    expect(segs()).toHaveLength(nseg)
    expect(screen.getByTestId('queue-overflow').textContent).toBe(`+${14 - nseg}`)
  })
})

describe('QueueStrip — click', () => {
  it('opens the job queue of the watcher area', () => {
    seed([])
    render(<QueueStrip />)
    fireEvent.click(screen.getByTestId('queue-strip'))
    expect(openWatchArea).toHaveBeenCalledWith({ tab: 'jobs' })
  })
})
