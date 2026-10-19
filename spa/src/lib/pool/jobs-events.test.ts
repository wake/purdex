// spa/src/lib/pool/jobs-events.test.ts — the `jobs.changed` frame: `{host, job_id, state}` as a JSON string; anything else is dropped, and a good one makes the driver read that job.
import { describe, it, expect, vi, beforeEach } from 'vitest'

const jobChanged = vi.fn(async () => {})
vi.mock('./jobs-driver-live', () => ({ jobsDriver: { jobChanged: (...a: unknown[]) => jobChanged(...(a as [])) } }))

const { parseJobsChanged, handleJobsEvent, isJobsEvent } = await import('./jobs-events')

const frame = (over: Record<string, unknown> = {}) => JSON.stringify({ host: 'mserv', job_id: 'j1', state: 'running', ...over })

beforeEach(() => { jobChanged.mockClear() })

describe('parseJobsChanged', () => {
  it('reads the host, the job id and the state', () => {
    expect(parseJobsChanged(frame())).toEqual({ host: 'mserv', jobId: 'j1', state: 'running' })
  })
  it('takes an object as well as its JSON text', () => {
    expect(parseJobsChanged({ host: 'a', job_id: 'b', state: 'queued' })).toEqual({ host: 'a', jobId: 'b', state: 'queued' })
  })
  it('a state the App does not know is still a notice', () => {
    expect(parseJobsChanged(frame({ state: 'future' }))).toMatchObject({ state: 'future' })
  })
  it.each([
    ['not JSON', 'nope'],
    ['not an object', '[1]'],
    ['null', 'null'],
    ['no host', frame({ host: undefined })],
    ['empty host', frame({ host: '' })],
    ['no job id', frame({ job_id: undefined })],
    ['empty job id', frame({ job_id: '' })],
    ['a number for the id', frame({ job_id: 5 })],
    ['no state', frame({ state: undefined })],
  ])('drops a frame that is %s', (_name, value) => {
    expect(parseJobsChanged(value)).toBeNull()
  })
})

describe('handleJobsEvent', () => {
  it('a good frame makes the driver read that job of that host', () => {
    handleJobsEvent('h1', frame())
    expect(jobChanged).toHaveBeenCalledWith('mserv', 'j1')
  })
  it('a bad frame reads nothing', () => {
    handleJobsEvent('h1', 'nope')
    expect(jobChanged).not.toHaveBeenCalled()
  })
  it('only jobs.changed is a jobs event', () => {
    expect(isJobsEvent('jobs.changed')).toBe(true)
    expect(isJobsEvent('pool.changed')).toBe(false)
    expect(isJobsEvent('fleet.changed')).toBe(false)
  })
})
