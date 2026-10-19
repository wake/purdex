// spa/src/lib/pool/job-detail.test.ts — what a job's log area says: the text, the button for a log over 1 MiB, or why there is none.
import { describe, it, expect } from 'vitest'
import { logNote, LOG_UPLOAD_GRACE_MS } from './job-detail'
import type { PoolJob, JobResult } from './jobs-types'

const NOW = 1_800_000_000_000
const result = (over: Partial<JobResult> = {}): JobResult => ({
  exit: 0, summary: null, log: 'j/log', host: 'mlab', started_at: NOW - 120_000, finished_at: NOW - 60_000 * 30, cache_hit: false, reason: '', superseded_by: null, ...over,
})
const job = (state: string, r: JobResult | null): PoolJob => ({
  id: 'j', origin: 'mlab', repo: { name: 'purdex', root: '', remote: '' }, snapshot: { commit: 'c', tree: '', base: '', rel_cwd: '' }, task: 'test', args: [], class: 'dev', labels: [], locks: [], cacheable: false,
  timeout_s: 0, supersede_key: '', interruptible: false, queue_timeout_s: 0, adhoc: false, local: false, submitter: { session: '', ref: '', team: '', host: '' }, created_at: NOW - 1000, state,
  claim: null, result: r, manual: '', manual_seq: 0,
})

describe('logNote', () => {
  it('a job that has not ended says the log comes at the end', () => {
    expect(logNote(job('running', null), null, NOW)).toBe('running')
    expect(logNote(job('queued', null), null, NOW)).toBe('running')
    expect(logNote(job('claimed', null), null, NOW)).toBe('running')
  })

  it('an ended job with the log in its detail shows the text', () => {
    expect(logNote(job('succeeded', result()), 'line 1', NOW)).toBe('text')
  })

  it('an ended job whose log is stored but not in the detail (over 1 MiB) is loaded on demand', () => {
    expect(logNote(job('failed', result({ log: 'j/log' })), null, NOW)).toBe('on_demand')
  })

  it('no stored log: within a minute of the end it is still uploading, later it is past the keeping time', () => {
    const at = (ago: number) => job('failed', result({ log: '', finished_at: NOW - ago }))
    expect(logNote(at(LOG_UPLOAD_GRACE_MS - 1), null, NOW)).toBe('uploading')
    expect(logNote(at(LOG_UPLOAD_GRACE_MS), null, NOW)).toBe('expired')
  })

  it('a job that never ran (superseded in the queue) has no log and says nothing about one', () => {
    expect(logNote(job('superseded', null), null, NOW)).toBe('none')
    expect(logNote(job('expired', result({ log: '', started_at: null, finished_at: null })), null, NOW)).toBe('none')
  })
})
