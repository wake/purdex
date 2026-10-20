import { describe, it, expect, vi, beforeEach } from 'vitest'

vi.mock('../../features/workspace/lib/open-watch-area', () => ({ openWatchArea: vi.fn() }))
import { openWatchArea } from '../../features/workspace/lib/open-watch-area'
import { clearJobsViewMemory, readJobsMode, readSelectedJob, writeJobsMode } from '../pool/jobs-view-memory'
import { queueOpener } from './open-queue'
import type { QueuedJobMark } from './queued-sessions'

const mark = (over: Partial<QueuedJobMark> = {}): QueuedJobMark => ({ jobId: 'j1', origin: 'mlab', line: 'purdex test', state: 'running', place: 0, since: 1, worker: 'mlab', ...over })
const waiter = { class: 'heavy' as const, place: 1, command: 'go test', project: 'p', session: 's', since: 1 }

describe('queueOpener', () => {
  beforeEach(() => { vi.mocked(openWatchArea).mockClear(); clearJobsViewMemory() })

  it('opens the job queue on the live view with the first job selected', () => {
    writeJobsMode('history')
    queueOpener([waiter, mark({ jobId: 'j9', origin: 'mserv' }), mark()])!()
    expect(openWatchArea).toHaveBeenCalledWith({ tab: 'jobs' })
    expect(readJobsMode()).toBe('live')
    expect(readSelectedJob()).toBe('mserv/j9')
  })

  it('has nothing to open without waiters or with gate waiters only', () => {
    expect(queueOpener(undefined)).toBeUndefined()
    expect(queueOpener([])).toBeUndefined()
    expect(queueOpener([waiter])).toBeUndefined()
  })
})
