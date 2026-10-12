import { describe, it, expect, vi, beforeEach } from 'vitest'
import { ApprovalApiError } from './approval-api'
import { fetchPaused, parsePausedView, postPauseRelease } from './paused-api'
import { parseRosterEvent } from './roster'
import { pausedLightText } from './paused-text'
import * as approvalApi from './approval-api'

vi.mock('./approval-api', async (orig) => ({ ...(await orig<typeof import('./approval-api')>()), send: vi.fn() }))
vi.mock('./unattended-api', () => ({ descriptorFor: vi.fn(async () => ({ kind: 'app', label: 'Purdex.app' })) }))
const send = vi.mocked(approvalApi.send)

const row = (over: Record<string, unknown> = {}) => ({ session_id: 'S1', ref: '_aaaaaa', title: 'solo', role: 'solo', causes: ['quota'], since: 5, lead_ref: '', bounced: 0, ...over })

beforeEach(() => send.mockReset())

describe('parsePausedView', () => {
  it('keeps good rows', () => {
    const out = parsePausedView({ sessions: [row(), row({ session_id: 'S2', role: 'lead', causes: ['quota', 'lead'], bounced: 2 })] })
    expect(out.map((p) => p.session_id)).toEqual(['S1', 'S2'])
    expect(out[1].causes).toEqual(['quota', 'lead'])
  })
  it('drops rows with a missing or ill-typed field', () => {
    const bad = [row({ session_id: undefined }), row({ ref: 3 }), row({ role: 'boss' }), row({ since: 'x' }), row({ bounced: null }), row({ causes: [] }), row({ causes: 'quota' }), 'junk', null]
    expect(parsePausedView({ sessions: [...bad, row({ session_id: 'ok' })] }).map((p) => p.session_id)).toEqual(['ok'])
  })
  it('drops an unknown cause from a row, and the row when none is left', () => {
    const out = parsePausedView({ sessions: [row({ session_id: 'A', causes: ['quota', 'future'] }), row({ session_id: 'B', causes: ['future'] })] })
    expect(out).toHaveLength(1)
    expect(out[0].causes).toEqual(['quota'])
  })
  it('an envelope that is not {sessions: []} is bad_response', () => {
    expect(() => parsePausedView({})).toThrow(ApprovalApiError)
    expect(parsePausedView({ sessions: [] })).toEqual([])
  })
})

describe('roster paused', () => {
  const lead = (extra: Record<string, unknown>) => ({ session_id: 'L', ref: '_l', address: 'h/l', live: true, ...extra })
  const parse = (extra: Record<string, unknown>) => {
    const ev = parseRosterEvent({ op: 'snapshot', teams: [{ id: 't', host_id: 'h', created_at: 1, lead: lead(extra), members: [] }] })
    if (typeof ev === 'string') throw new Error(ev)
    return ev.teams[0].lead
  }
  it('null / missing -> absent', () => {
    expect(parse({ paused: null }).paused).toBeUndefined()
    expect(parse({}).paused).toBeUndefined()
  })
  it('an object is kept, unknown causes dropped', () => {
    expect(parse({ paused: { causes: ['quota', 'x'], since: 9 } }).paused).toEqual({ causes: ['quota'], since: 9 })
  })
  it('a malformed value becomes absent and does not drop the frame', () => {
    expect(parse({ paused: { causes: [], since: 9 } }).paused).toBeUndefined()
    expect(parse({ paused: { causes: ['quota'], since: 'x' } }).paused).toBeUndefined()
    expect(parse({ paused: 'yes' }).paused).toBeUndefined()
  })
})

describe('fetchPaused / postPauseRelease', () => {
  it('fetchPaused GETs the list and parses it', async () => {
    send.mockResolvedValueOnce({ sessions: [row()] })
    expect(await fetchPaused('h')).toHaveLength(1)
    expect(send.mock.calls[0][1]).toBe('/api/team/paused')
  })
  it('200 -> released{n}, body carries the client claim', async () => {
    send.mockResolvedValueOnce({ released: 2 })
    expect(await postPauseRelease('h', 'S/1')).toEqual({ kind: 'released', n: 2 })
    expect(send.mock.calls[0][1]).toBe('/api/team/paused/S%2F1/release')
    expect(JSON.parse(String(send.mock.calls[0][2].body))).toEqual({ client: { kind: 'app', label: 'Purdex.app' } })
  })
  it('404 not_paused -> not_paused', async () => {
    send.mockRejectedValueOnce(new ApprovalApiError(404, 'not_paused'))
    expect(await postPauseRelease('h', 'S')).toEqual({ kind: 'not_paused' })
  })
  it('400 -> bad_claim with detail', async () => {
    send.mockRejectedValueOnce(new ApprovalApiError(400, 'bad_request', 'client.label is empty'))
    expect(await postPauseRelease('h', 'S')).toEqual({ kind: 'bad_claim', detail: 'client.label is empty' })
  })
  it('network and other failures -> error', async () => {
    send.mockRejectedValueOnce(new ApprovalApiError(0, 'network', 'down'))
    expect(await postPauseRelease('h', 'S')).toEqual({ kind: 'error', message: 'down' })
    send.mockRejectedValueOnce(new ApprovalApiError(500, 'http_500'))
    expect((await postPauseRelease('h', 'S')).kind).toBe('error')
  })
})

describe('pausedLightText', () => {
  const t = (k: string, p?: Record<string, string | number>) => (p ? `${k}:${JSON.stringify(p)}` : k)
  it('quota', () => expect(pausedLightText(t, ['quota'], 0)).toBe('paused.light.quota'))
  it('lead only', () => expect(pausedLightText(t, ['lead'], 0)).toBe('paused.light.lead'))
  it('quota + lead reads as quota', () => expect(pausedLightText(t, ['lead', 'quota'], 0)).toBe('paused.light.quota'))
  it('bounced 0 adds nothing, N adds the suffix', () => {
    expect(pausedLightText(t, ['quota'], 0)).not.toContain('bounced')
    expect(pausedLightText(t, ['quota'], 3)).toBe('paused.light.quota' + 'paused.light.bounced:{"n":3}')
  })
})
