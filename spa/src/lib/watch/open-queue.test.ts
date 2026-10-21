import { describe, it, expect, vi, beforeEach } from 'vitest'

vi.mock('../../features/workspace/lib/open-watch-area', () => ({ openWatchArea: vi.fn() }))
import { openWatchArea } from '../../features/workspace/lib/open-watch-area'
import { clearJobsViewMemory, readJobsMode, readSelectedJob, writeJobsMode } from '../pool/jobs-view-memory'
import { queueOpener, tabQueueOpener } from './open-queue'
import type { QueuedJobMark } from './queued-sessions'

const mark = (over: Partial<QueuedJobMark> = {}): QueuedJobMark => ({ jobId: 'j1', origin: 'mlab', line: 'purdex test', state: 'running', place: 0, since: 1, worker: 'mlab', ...over })

describe('queueOpener', () => {
  beforeEach(() => { vi.mocked(openWatchArea).mockClear(); clearJobsViewMemory() })

  it('opens the job queue on the live view with the first job selected', () => {
    writeJobsMode('history')
    queueOpener([mark({ jobId: 'j9', origin: 'mserv' }), mark()])!()
    expect(openWatchArea).toHaveBeenCalledWith({ tab: 'jobs' })
    expect(readJobsMode()).toBe('live')
    expect(readSelectedJob()).toBe('mserv/j9')
  })

  it('has nothing to open without jobs', () => {
    expect(queueOpener(undefined)).toBeUndefined()
    expect(queueOpener([])).toBeUndefined()
  })
})

describe('tabQueueOpener: only while the hourglass is drawn', () => {
  const base = { backgroundWaiters: [mark()], tabIndicatorStyle: 'badge' as const, agentStatus: 'running' as const, isAwaitingApproval: false }
  it('lit: the opener', () => { expect(tabQueueOpener(base)).toBeTypeOf('function') })
  it('lights off (icon): none, even when waiting for approval', () => {
    expect(tabQueueOpener({ ...base, tabIndicatorStyle: 'icon' })).toBeUndefined()
    expect(tabQueueOpener({ ...base, tabIndicatorStyle: 'icon', isAwaitingApproval: true })).toBeUndefined()
  })
  it('no light at all (no status): none; an awaiting request is a light', () => {
    expect(tabQueueOpener({ ...base, agentStatus: undefined })).toBeUndefined()
    expect(tabQueueOpener({ ...base, agentStatus: undefined, isAwaitingApproval: true })).toBeTypeOf('function')
  })
  it.each(['dot', 'iconDot'] as const)('%s: the opener', (tabIndicatorStyle) => { expect(tabQueueOpener({ ...base, tabIndicatorStyle })).toBeTypeOf('function') })
})
