import { describe, it, expect } from 'vitest'
import { classify, backoffMs, busyDelayMs } from './errors'

const body = (error: string) => ({ error })

// One test per row of the plan's error table (WWD-7 design).
describe('classify', () => {
  it('no answer (network) → next source', () => {
    expect(classify(null, null)).toEqual({ code: 'no_answer', action: 'next_source' })
  })
  it('wwd_unavailable 503 → next source', () => {
    expect(classify(503, body('wwd_unavailable'))).toEqual({ code: 'wwd_unavailable', action: 'next_source' })
  })
  it('wwd_bad_response 502 → next source', () => {
    expect(classify(502, body('wwd_bad_response'))).toEqual({ code: 'wwd_bad_response', action: 'next_source' })
  })
  it('busy 429 → the same source again', () => {
    expect(classify(429, body('busy'))).toEqual({ code: 'busy', action: 'retry_same' })
  })
  it('wwd_error 502 (a read) → back off on the same source', () => {
    expect(classify(502, body('wwd_error'))).toEqual({ code: 'wwd_error', action: 'backoff' })
  })
  it('host_unreachable 502 (a read) → back off', () => {
    expect(classify(502, body('host_unreachable'))).toEqual({ code: 'host_unreachable', action: 'backoff' })
  })
  it('cursor_ahead 409 → drop the origin and read from 0', () => {
    expect(classify(409, body('cursor_ahead'))).toEqual({ code: 'cursor_ahead', action: 'reset_origin' })
  })
  it('not_found 404 → nothing to do', () => {
    expect(classify(404, body('not_found'))).toEqual({ code: 'not_found', action: 'none' })
  })
  it('host_not_local 501 → keep the gauges, no chart', () => {
    expect(classify(501, body('host_not_local'))).toEqual({ code: 'host_not_local', action: 'no_chart' })
  })
  it('wwd_rejected 4xx (a change) → the value goes back', () => {
    expect(classify(422, body('wwd_rejected'), 'change')).toEqual({ code: 'wwd_rejected', action: 'revert' })
  })
  it('wwd_error / wwd_unavailable (a change) → the value goes back with a retry', () => {
    expect(classify(502, body('wwd_error'), 'change')).toEqual({ code: 'wwd_error', action: 'revert_retry' })
    expect(classify(503, body('wwd_unavailable'), 'change')).toEqual({ code: 'wwd_unavailable', action: 'revert_retry' })
  })
  it('forbidden 403 → the section turns read only', () => {
    expect(classify(403, body('forbidden'))).toEqual({ code: 'forbidden', action: 'read_only' })
  })
  it('bad_request 400 → logged', () => {
    expect(classify(400, body('bad_request'))).toEqual({ code: 'bad_request', action: 'log' })
  })

  it('a body without a code falls back to the status', () => {
    expect(classify(503, null).code).toBe('wwd_unavailable')
    expect(classify(429, {}).code).toBe('busy')
    expect(classify(403, 'text').code).toBe('forbidden')
  })
  it('an unknown code is logged, never thrown', () => {
    expect(classify(418, body('teapot'))).toEqual({ code: 'unknown', action: 'log' })
  })
})

describe('timers', () => {
  it('backoff doubles from 2 s and stops at 30 s', () => {
    expect([0, 1, 2, 3, 4, 5, 9].map(backoffMs)).toEqual([2000, 4000, 8000, 16000, 30000, 30000, 30000])
  })
  it('busy retries after 1–2 s', () => {
    expect(busyDelayMs(0)).toBe(1000)
    expect(busyDelayMs(0.999)).toBeLessThanOrEqual(2000)
  })
})
