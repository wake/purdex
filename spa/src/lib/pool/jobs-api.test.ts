// spa/src/lib/pool/jobs-api.test.ts — the reads of `/api/pool/jobs*` from one daemon: the page, one job, a log. A refused read carries its classification (one test per row of the error table that a jobs read can meet).
import { describe, it, expect, vi, beforeEach } from 'vitest'
import listCursor from './__fixtures__/jobs_list_cursor.json'
import detailJson from './__fixtures__/job_detail.json'
import aheadJson from './__fixtures__/cursor_ahead.json'

const hostFetch = vi.fn()
let offline = false
vi.mock('../host-api', () => ({ hostFetch: (...a: unknown[]) => (offline ? Promise.reject(new TypeError('failed')) : hostFetch(...a)) }))

import { fetchJobsPage, fetchJob, fetchJobLog } from './jobs-api'
import { PoolReadError } from './api'

const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status })
beforeEach(() => { hostFetch.mockReset(); offline = false })
const errOf = async (p: Promise<unknown>) => (await p.catch((e) => e)) as PoolReadError

describe('fetchJobsPage', () => {
  it('reads the daemon\'s own list, with after and limit', async () => {
    hostFetch.mockResolvedValue(json(200, listCursor))
    const page = await fetchJobsPage('d1', { after: '0', limit: 1000 })
    expect(hostFetch).toHaveBeenCalledWith('d1', '/api/pool/jobs?after=0&limit=1000')
    expect(page.jobs.map((j) => j.id)).toEqual(['j_01JC'])
    expect(page.cursor).toBe('42')
  })

  it('a list of another host is asked through ?host=, encoded, in a fixed order', async () => {
    hostFetch.mockResolvedValue(json(200, listCursor))
    await fetchJobsPage('d1', { host: 'air 19', state: 'queued', session: 's1', after: '7', limit: 50 })
    expect(hostFetch).toHaveBeenCalledWith('d1', '/api/pool/jobs?host=air+19&state=queued&session=s1&after=7&limit=50')
  })

  it('no query at all is the bare path', async () => {
    hostFetch.mockResolvedValue(json(200, listCursor))
    await fetchJobsPage('d1', {})
    expect(hostFetch).toHaveBeenCalledWith('d1', '/api/pool/jobs')
  })

  it.each([
    ['no answer (network)', null, null, 'no_answer', 'next_source'],
    ['wwd_unavailable 503', 503, { error: 'wwd_unavailable' }, 'wwd_unavailable', 'next_source'],
    ['wwd_bad_response 502', 502, { error: 'wwd_bad_response' }, 'wwd_bad_response', 'next_source'],
    ['busy 429', 429, { error: 'busy' }, 'busy', 'retry_same'],
    ['wwd_error 502', 502, { error: 'wwd_error' }, 'wwd_error', 'backoff'],
    ['host_unreachable 502', 502, { error: 'host_unreachable' }, 'host_unreachable', 'backoff'],
    ['cursor_ahead 409 (its body is wwd\'s, with no "error")', 409, aheadJson, 'cursor_ahead', 'reset_origin'],
    ['bad_request 400', 400, { error: 'bad_request' }, 'bad_request', 'log'],
  ])('%s', async (_n, status, body, code, action) => {
    if (status === null) offline = true
    else hostFetch.mockResolvedValue(json(status, body))
    const err = await errOf(fetchJobsPage('d1', {}))
    expect(err).toBeInstanceOf(PoolReadError)
    expect(err.poolError).toEqual({ code, action })
  })

  it('an answer that parses as nothing is outside the contract', async () => {
    hostFetch.mockResolvedValue(json(200, { jobs: 3 }))
    expect((await errOf(fetchJobsPage('d1', {}))).poolError.code).toBe('wwd_bad_response')
  })
})

describe('fetchJob', () => {
  it('reads the detail, naming the host when it is another one', async () => {
    hostFetch.mockImplementation(async () => json(200, detailJson)) // a Response body is read once: each call gets its own
    const d = await fetchJob('d1', 'j_01JC', 'air19')
    expect(hostFetch).toHaveBeenCalledWith('d1', '/api/pool/jobs/j_01JC?host=air19')
    expect(d.job.id).toBe('j_01JC')
    await fetchJob('d1', 'j_01JC')
    expect(hostFetch).toHaveBeenLastCalledWith('d1', '/api/pool/jobs/j_01JC')
  })

  it('a job that is gone is not_found, which changes nothing else', async () => {
    hostFetch.mockResolvedValue(json(404, { error: 'not_found' }))
    expect((await errOf(fetchJob('d1', 'j_x'))).poolError).toEqual({ code: 'not_found', action: 'none' })
  })

  it('a detail that parses as nothing is outside the contract', async () => {
    hostFetch.mockResolvedValue(json(200, { job: 1 }))
    expect((await errOf(fetchJob('d1', 'j_x'))).poolError.code).toBe('wwd_bad_response')
  })
})

describe('fetchJobLog', () => {
  it('reads the log as text', async () => {
    hostFetch.mockResolvedValue(new Response('line 1\nline 2\n', { status: 200 }))
    expect(await fetchJobLog('d1', 'j_01JC', 'air19')).toBe('line 1\nline 2\n')
    expect(hostFetch).toHaveBeenCalledWith('d1', '/api/pool/jobs/j_01JC/log?host=air19')
  })

  it('no log yet is not_found; a busy daemon is busy', async () => {
    hostFetch.mockResolvedValue(json(404, { error: 'not_found' }))
    expect((await errOf(fetchJobLog('d1', 'j_01JC'))).poolError.code).toBe('not_found')
    hostFetch.mockResolvedValue(json(429, { error: 'busy' }))
    expect((await errOf(fetchJobLog('d1', 'j_01JC'))).poolError).toEqual({ code: 'busy', action: 'retry_same' })
  })
})
