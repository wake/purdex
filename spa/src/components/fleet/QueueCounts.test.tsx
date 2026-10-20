import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { QueueCounts } from './QueueCounts'
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
  usePoolStore.setState({ hosts: answered ? [hostRow('mlab'), hostRow('air26')] : [], sourceHostId: answered ? 'cfg-a' : null, oldDaemon: false } as never)
}
const block = (kind: 'heavy' | 'light') => screen.getByTestId(`queue-count-${kind}`)
const light = (id: string, origin: string) => job(id, origin, { slot: 'light' } as never)

beforeEach(() => {
  vi.useFakeTimers()
  vi.setSystemTime(NOW)
  vi.mocked(openWatchArea).mockClear()
  useLayoutStore.setState({ queueStripSegments: 10 })
  usePoolStore.setState({ start: vi.fn(), stop: vi.fn() } as never)
})

describe('QueueCounts — the two blocks (M15)', () => {
  it('3 heavy, 0 light: a purple 3 and a grey 0', () => {
    seed([job('a', 'mlab'), job('b', 'mlab'), job('c', 'air26')])
    render(<QueueCounts />)
    expect(block('heavy').textContent).toBe('3')
    expect(block('heavy').className).toContain('bg-queue-heavy')
    expect(block('light').textContent).toBe('0')
    expect(block('light').className).toContain('bg-surface-hover')
    expect(block('light').className).not.toContain('bg-queue-light')
  })
  it('light waiting jobs fill the teal block', () => {
    seed([light('a', 'mlab'), light('b', 'air26'), job('c', 'mlab')])
    render(<QueueCounts />)
    expect(block('light').textContent).toBe('2')
    expect(block('light').className).toContain('bg-queue-light')
    expect(block('heavy').textContent).toBe('1')
  })
  it('nothing waits: both blocks are there, grey, 0 and 0', () => {
    seed([])
    render(<QueueCounts />)
    expect(block('heavy').textContent).toBe('0')
    expect(block('light').textContent).toBe('0')
    expect(block('heavy').className).toContain('bg-surface-hover')
  })
  it('the segment setting does not change the numbers; a claimed job and a hold are not counted', () => {
    useLayoutStore.setState({ queueStripSegments: 6 })
    seed([...Array.from({ length: 14 }, (_, i) => job(`j${i}`, 'mlab')), job('r', 'mlab', { state: 'running', claim: { worker: 'air26', attempt: 1, since: 0 } } as never), job('h', 'mlab', { manual: 'hold' } as never)])
    render(<QueueCounts />)
    expect(block('heavy').textContent).toBe('14')
  })
  it('more than 99 reads 99+', () => {
    seed(Array.from({ length: 120 }, (_, i) => job(`j${i}`, 'mlab')))
    render(<QueueCounts />)
    expect(block('heavy').textContent).toBe('99+')
  })
  it('the accessible name counts the waiting jobs', () => {
    seed([job('a', 'mlab'), light('b', 'mlab')])
    render(<QueueCounts />)
    expect(screen.getByTestId('queue-counts').getAttribute('aria-label')).toBe('Resource pool: 2 waiting')
  })
})

describe('QueueCounts — a pool nobody can read (M25)', () => {
  it('every source failed while the jobs store still holds waiting jobs: two grey 0s', () => {
    seed(Array.from({ length: 14 }, (_, i) => job(`j${i}`, 'mlab')), false)
    render(<QueueCounts />)
    expect(block('heavy').textContent).toBe('0')
    expect(block('light').textContent).toBe('0')
    expect(block('heavy').className).toContain('bg-surface-hover')
  })
})

describe('QueueCounts — hover and click', () => {
  it('hover lists the waiting jobs to the right and the tile is described by it; leaving closes it', () => {
    seed([job('a', 'mlab')])
    render(<QueueCounts />)
    const tile = screen.getByTestId('queue-counts')
    expect(tile.getAttribute('aria-describedby')).toBeNull()
    fireEvent.mouseEnter(tile)
    const tip = screen.getByRole('tooltip')
    expect(tip.getAttribute('data-placement')).toBe('right')
    expect(tile.getAttribute('aria-describedby')).toBe(tip.id)
    fireEvent.mouseLeave(tile)
    expect(screen.queryByRole('tooltip')).toBeNull()
  })
  it('a click opens the job queue', () => {
    seed([job('a', 'mlab')])
    render(<QueueCounts />)
    fireEvent.click(screen.getByTestId('queue-counts'))
    expect(openWatchArea).toHaveBeenCalledWith({ tab: 'jobs' })
  })
})
