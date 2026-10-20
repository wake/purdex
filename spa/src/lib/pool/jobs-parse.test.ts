// spa/src/lib/pool/jobs-parse.test.ts — the trust boundary of the jobs wire. The JSON samples in __fixtures__/ are byte copies of internal/wwd/wire/testdata (the fixtures-sync guard keeps them so). A malformed job is dropped
// whole, never half-read; a page or detail whose envelope is wrong is null; a state, class or position the App does not know stays text.
import { describe, it, expect } from 'vitest'
import { parseJob, parseJobDetail, parseJobsPage, parseCursorAhead } from './jobs-parse'
import listCursor from './__fixtures__/jobs_list_cursor.json'
import listEmpty from './__fixtures__/jobs_list_response.json'
import doneJson from './__fixtures__/job_done.json'
import detailJson from './__fixtures__/job_detail.json'
import aheadJson from './__fixtures__/cursor_ahead.json'

describe('a queued job', () => {
  it('reads every field, with times as ms', () => {
    const page = parseJobsPage(listCursor)!
    expect(page.cursor).toBe('42')
    expect(page.epoch).toBe('0123456789abcdef0123456789abcdef')
    const j = page.jobs[0]
    expect(j).toMatchObject({
      id: 'j_01JC', origin: 'mlab', task: 'spa-related', args: ['src/a.ts'], class: 'merge', state: 'queued', labels: ['os:darwin', 'repo:purdex'], locks: [],
      cacheable: true, timeout_s: 1800, interruptible: false, queue_timeout_s: 0, adhoc: false, local: false, manual: '', manual_seq: 0, claim: null, result: null,
    })
    expect(j.created_at).toBe(Date.parse('2026-10-18T05:00:00+08:00'))
    expect(j.repo).toEqual({ name: 'purdex', root: 'abc123', remote: 'git@github.com:wake/purdex.git' })
    expect(j.snapshot.rel_cwd).toBe('spa')
    expect(j.submitter).toEqual({ session: 's1', ref: '_abc123', team: 'wwd', host: 'mlab' })
  })
})

describe('a finished job', () => {
  it('reads the claim and the result: summary, times, who superseded it', () => {
    const j = parseJob(doneJson)!
    expect(j.state).toBe('superseded')
    expect(j.claim).toEqual({ worker: 'air26', attempt: 1, since: Date.parse('2026-10-18T05:00:10+08:00') })
    expect(j.result).toMatchObject({ exit: 0, host: 'air26', cache_hit: false, reason: '', superseded_by: 'j_01JE', log: 'j_01JD/log' })
    expect(j.result?.summary).toEqual({ passed: 10, failed: 1, skipped: 2, failures: [{ name: 't', file: 'a.test.ts', message: 'boom' }], tail: ['x'] })
    expect(j.result?.started_at).toBe(Date.parse('2026-10-18T05:00:11+08:00'))
    expect(j.result?.finished_at).toBe(Date.parse('2026-10-18T05:02:00+08:00'))
  })

  it('a null exit stays null (cancelled, expired): it is not 0', () => {
    const j = parseJob({ ...doneJson, result: { ...doneJson.result, exit: null, summary: null } })!
    expect(j.result?.exit).toBeNull()
    expect(j.result?.summary).toBeNull()
  })
})

describe('what is not known stays text', () => {
  it('an unknown state, class or position is kept as it came', () => {
    const j = parseJob({ ...doneJson, state: 'paused', class: 'nightly', manual: 'sideways' })!
    expect([j.state, j.class, j.manual]).toEqual(['paused', 'nightly', 'sideways'])
  })
  it('null lists (a Go nil slice) read as empty', () => {
    const j = parseJob({ ...doneJson, args: null, labels: null, locks: null })!
    expect([j.args, j.labels, j.locks]).toEqual([[], [], []])
  })
  it('an empty submitter session (a job from before #3477) is kept empty', () => {
    const j = parseJob({ ...doneJson, submitter: { ...doneJson.submitter, session: '' } })!
    expect(j.submitter.session).toBe('')
  })
})

describe('a malformed job is dropped whole', () => {
  it.each([
    ['no id', { ...doneJson, id: '' }],
    ['no origin', { ...doneJson, origin: 3 }],
    ['no state', { ...doneJson, state: undefined }],
    ['no repo', { ...doneJson, repo: null }],
    ['created_at that is no time', { ...doneJson, created_at: 'yesterday' }],
    ['a claim that is not an object', { ...doneJson, claim: 'air26' }],
    ['a result whose summary is malformed', { ...doneJson, result: { ...doneJson.result, summary: { passed: 'ten' } } }],
  ])('%s', (_n, job) => {
    expect(parseJob(job)).toBeNull()
  })

  it('in a page, the bad one goes and the rest stay; a page whose jobs are all bad is unreadable, not empty', () => {
    const page = parseJobsPage({ jobs: [{ ...doneJson, id: '' }, listCursor.jobs[0]], cursor: '1', epoch: 'e' })!
    expect(page.jobs.map((j) => j.id)).toEqual(['j_01JC'])
    expect(parseJobsPage({ jobs: [{ ...doneJson, id: '' }], cursor: '1', epoch: 'e' })).toBeNull()
  })
})

describe('the envelopes', () => {
  it('an empty page is empty, and keeps its cursor and epoch (which may be empty)', () => {
    expect(parseJobsPage(listEmpty)).toEqual({ jobs: [], cursor: '0', epoch: '' })
  })
  it.each([null, 3, 'x', [], {}, { jobs: 'no', cursor: '1', epoch: 'e' }, { jobs: [], cursor: 1, epoch: 'e' }, { jobs: [], cursor: '1' }])('%j is not a page', (v) => {
    expect(parseJobsPage(v)).toBeNull()
  })
  it('a null jobs list (a Go nil slice) is an empty page', () => {
    expect(parseJobsPage({ jobs: null, cursor: '3', epoch: 'e' })).toEqual({ jobs: [], cursor: '3', epoch: 'e' })
  })

  it('the detail: the job and its log, null while no log is stored', () => {
    const d = parseJobDetail(detailJson)!
    expect(d.job.id).toBe('j_01JC')
    expect(d.log === null || typeof d.log === 'string').toBe(true)
    expect(parseJobDetail({ job: doneJson, log: 'out' })?.log).toBe('out')
    expect(parseJobDetail({ job: doneJson, log: null })?.log).toBeNull()
    expect(parseJobDetail({ job: { ...doneJson, id: '' }, log: null })).toBeNull()
    expect(parseJobDetail({ log: null })).toBeNull()
  })

  it('cursor_ahead: the head and the epoch the daemon has now', () => {
    expect(parseCursorAhead(aheadJson)).toEqual({ head: '17', epoch: '0123456789abcdef0123456789abcdef' })
    expect(parseCursorAhead({ reason: 'other' })).toBeNull()
    expect(parseCursorAhead(null)).toBeNull()
  })
})

describe('the slot of a job', () => {
  const slotOf = (slot: unknown) => parseJob({ ...doneJson, slot })?.slot
  it('reads heavy and light', () => {
    expect(slotOf('heavy')).toBe('heavy')
    expect(slotOf('light')).toBe('light')
  })
  it('leaves the field out when absent or not a known value', () => {
    expect(parseJob(doneJson)).not.toHaveProperty('slot')
    for (const bad of ['medium', 3, null, '']) expect(parseJob({ ...doneJson, slot: bad })).not.toHaveProperty('slot')
  })
})
