import { describe, it, expect, vi, beforeEach } from 'vitest'
import { parseObservations, fetchObservations, parseUndeclared, fetchUndeclared } from './observations-api'

vi.mock('../host-api', () => ({ hostFetch: vi.fn() }))
import { hostFetch } from '../host-api'

// The Go shape: internal/wwd/wire/observation.go (a record) inside wwd's answer, which the daemon passes on untouched (internal/module/pool/observations.go).
const REC = { id: 'o_1_2', host: 'air26', session: 's1', via: 'session', command: 'go test', signature: 'go test', started_at: '2026-10-20T01:00:00Z', ended_at: null, cpu_seconds: 61.5, cpu_peak: 100, mem_peak_mb: 300, procs_peak: 3 }
const DONE = { ...REC, id: 'o_3_4', via: 'codex', started_at: '2026-10-20T02:00:00Z', ended_at: '2026-10-20T02:10:00Z' }
const body = (over: Record<string, unknown> = {}) => ({ host: 'air26', since: '2026-10-19T00:00:00Z', observations: [REC, DONE], truncated: false, next_until: null, ...over })

describe('parseObservations', () => {
  it('reads a running and a finished record, times as ms', () => {
    const r = parseObservations(body())!
    expect(r.known).toBe(true)
    expect(r.truncated).toBe(false)
    expect(r.nextUntil).toBeNull()
    expect(r.observations[0]).toEqual({ id: 'o_1_2', host: 'air26', session: 's1', via: 'session', command: 'go test', signature: 'go test', startedAt: Date.parse('2026-10-20T01:00:00Z'), endedAt: null, cpuSeconds: 61.5, cpuPeak: 100, memPeakMb: 300, procsPeak: 3 })
    expect(r.observations[1].endedAt).toBe(Date.parse('2026-10-20T02:10:00Z'))
    expect(r.observations[1].via).toBe('codex')
  })
  it('keeps wwd\'s order (the oldest first)', () => {
    expect(parseObservations(body({ observations: [DONE, REC] }))!.observations.map((o) => o.id)).toEqual(['o_3_4', 'o_1_2'])
  })
  it('next_until comes back as the exact string, a zone and a fraction of a second included', () => {
    const next = '2026-10-20T01:00:00.123456789+08:00'
    const r = parseObservations(body({ truncated: true, next_until: next }))!
    expect(r.truncated).toBe(true)
    expect(r.nextUntil).toBe(next)
  })
  it('a next_until that is not a time is no cursor', () => {
    expect(parseObservations(body({ truncated: true, next_until: 'later' }))!.nextUntil).toBeNull()
    expect(parseObservations(body({ truncated: true, next_until: 7 }))!.nextUntil).toBeNull()
  })
  it('known: false (an older wwd) is an answer with nothing in it', () => {
    expect(parseObservations({ host: 'air19', known: false, observations: [] })).toEqual({ host: 'air19', known: false, observations: [], truncated: false, nextUntil: null })
  })
  it('observations: null is an empty list', () => {
    expect(parseObservations(body({ observations: null }))!.observations).toEqual([])
  })
  it('a bad record is dropped and the others stay', () => {
    const r = parseObservations(body({ observations: [{ ...REC, id: '' }, { ...REC, id: 'a', signature: '' }, { ...REC, id: 'b', via: 3 }, { ...REC, id: 'c', started_at: 'x' }, { ...REC, id: 'd', ended_at: 'soon' }, DONE] }))!
    expect(r.observations.map((o) => o.id)).toEqual(['o_3_4'])
  })
  it('a via the App does not know stays as the word; a missing figure is 0; a missing command is empty', () => {
    const r = parseObservations(body({ observations: [{ ...REC, via: 'newtool', cpu_seconds: undefined, mem_peak_mb: 'big', command: undefined }] }))!
    expect(r.observations[0]).toMatchObject({ via: 'newtool', cpuSeconds: 0, memPeakMb: 0, command: '' })
  })
  it('a record that ended before it began is dropped (the Go contract refuses it too)', () => {
    const r = parseObservations(body({ observations: [{ ...REC, id: 'x', ended_at: '2026-10-20T00:59:59Z' }, { ...REC, id: 'y', ended_at: '2026-10-20T01:00:00Z' }] }))!
    expect(r.observations.map((o) => o.id)).toEqual(['y'])
  })
  it('a record that names another host than the answer is dropped; an empty host stays', () => {
    const r = parseObservations(body({ observations: [{ ...REC, id: 'a', host: 'mlab' }, { ...REC, id: 'b', host: '' }, { ...REC, id: 'c', host: 'air26' }] }))!
    expect(r.observations.map((o) => o.id)).toEqual(['b', 'c'])
  })
  it('a record twice in one answer is one', () => {
    expect(parseObservations(body({ observations: [REC, REC] }))!.observations).toHaveLength(1)
  })
  it.each([null, 3, 'x', [], { host: 'a' }, { observations: 'none' }, { observations: [] }, { host: 3, observations: [] }, { host: '', observations: [] }])('a wrong envelope is null: %j', (v) => {
    expect(parseObservations(v)).toBeNull()
  })
})

describe('fetchObservations', () => {
  beforeEach(() => vi.mocked(hostFetch).mockReset())
  const ok = (b: unknown) => vi.mocked(hostFetch).mockResolvedValue(new Response(JSON.stringify(b), { status: 200 }))
  it('asks the source daemon for that host, since as given', async () => {
    ok(body())
    const r = await fetchObservations('cfg-a', 'air26', { since: '2026-10-19T00:00:00Z' })
    expect(hostFetch).toHaveBeenCalledWith('cfg-a', '/api/pool/hosts/air26/observations?since=2026-10-19T00%3A00%3A00Z')
    expect(r.observations).toHaveLength(2)
  })
  it('the next page sends until exactly as wwd gave it, not re-formatted', async () => {
    ok(body())
    const next = '2026-10-20T01:00:00.123456789+08:00'
    await fetchObservations('cfg-a', 'air26', { since: '2026-10-19T00:00:00Z', until: next })
    expect(hostFetch).toHaveBeenCalledWith('cfg-a', `/api/pool/hosts/air26/observations?since=2026-10-19T00%3A00%3A00Z&until=${encodeURIComponent(next)}`)
  })
  it('an alias is path-encoded', async () => {
    ok(body({ host: 'a b/c', observations: [] }))
    await fetchObservations('cfg-a', 'a b/c', { since: '2026-10-19T00:00:00Z' })
    expect(vi.mocked(hostFetch).mock.calls[0][1]).toContain('/api/pool/hosts/a%20b%2Fc/observations')
  })
  it('an answer about another host than the one asked for is unreadable (wwd_bad_response)', async () => {
    ok(body({ host: 'mlab' }))
    await expect(fetchObservations('cfg-a', 'air26', { since: 'x' })).rejects.toMatchObject({ poolError: { code: 'wwd_bad_response' } })
  })
  it('an answer that parses as nothing is unreadable (wwd_bad_response)', async () => {
    ok({ nope: true })
    await expect(fetchObservations('cfg-a', 'air26', { since: 'x' })).rejects.toMatchObject({ poolError: { code: 'wwd_bad_response' } })
  })
  it('a 403 (a device token may not read them) is the forbidden class', async () => {
    vi.mocked(hostFetch).mockResolvedValue(new Response('{}', { status: 403 }))
    await expect(fetchObservations('cfg-a', 'air26', { since: 'x' })).rejects.toMatchObject({ status: 403, poolError: { code: 'forbidden' } })
  })
})

// The Go shape: internal/wwd/wire/undeclared.go (UndeclaredResponse), passed on untouched by the daemon; an older wwd's answer is the daemon's own `{ known: false }` (internal/module/pool/observations.go).
const GROUP = { signature: 'go test', count: 3, cpu_seconds: 120.5, max_mem_mb: 800, last_at: '2026-10-20T03:00:00Z', last_session: 's1', last_via: 'session', last_command: 'go test ./...' }
const CODEX = { ...GROUP, signature: 'codex exec', count: 1, cpu_seconds: 10, max_mem_mb: 50, last_session: 'orphan', last_via: 'codex', last_command: 'codex exec x' }
const ubody = (over: Record<string, unknown> = {}) => ({ host: 'air26', since: '2026-10-13T00:00:00Z', groups: [GROUP, CODEX], truncated: false, ...over })

describe('parseUndeclared', () => {
  it('reads the groups, times as ms', () => {
    const r = parseUndeclared(ubody())!
    expect(r.known).toBe(true)
    expect(r.truncated).toBe(false)
    expect(r.groups[0]).toEqual({ signature: 'go test', count: 3, cpuSeconds: 120.5, maxMemMb: 800, lastAt: Date.parse('2026-10-20T03:00:00Z'), lastSession: 's1', lastVia: 'session', lastCommand: 'go test ./...' })
    expect(r.groups[1].lastVia).toBe('codex')
  })
  it('groups: null is an empty list; known: false (an older wwd, the daemon\'s own answer) is an answer with nothing in it, whatever else it holds', () => {
    expect(parseUndeclared(ubody({ groups: null }))!.groups).toEqual([])
    const old = parseUndeclared({ host: 'air26', known: false, groups: [] })!
    expect([old.known, old.groups]).toEqual([false, []])
    expect(parseUndeclared({ host: 'air26', known: false })!.groups).toEqual([])
  })
  it('fails closed: one bad group (no signature, a count of 0, a time that is no time, a via that is not session or codex, a figure that is not a finite number of 0 or more) makes the page nothing (codex attack)', () => {
    for (const bad of [{ ...GROUP, signature: '' }, { ...GROUP, count: 0 }, { ...GROUP, last_at: 'nope' }, { ...GROUP, last_via: 'newkind' }, { ...GROUP, cpu_seconds: -1 }, { ...GROUP, max_mem_mb: 'x' }, { ...GROUP, cpu_seconds: undefined }]) {
      expect(parseUndeclared(ubody({ groups: [CODEX, bad] }))).toBeNull()
    }
  })
  it('a page of good groups is a page', () => {
    expect(parseUndeclared(ubody({ groups: [GROUP, CODEX] }))!.groups).toHaveLength(2)
  })
  it('truncated comes through; an answer without a host is nothing', () => {
    expect(parseUndeclared(ubody({ truncated: true }))!.truncated).toBe(true)
    expect(parseUndeclared({ groups: [] })).toBeNull()
  })
})

describe('fetchUndeclared', () => {
  beforeEach(() => vi.mocked(hostFetch).mockReset())
  const ok = (b: unknown) => vi.mocked(hostFetch).mockResolvedValue(new Response(JSON.stringify(b), { status: 200 }))
  it('asks the source daemon for that host, since as given', async () => {
    ok(ubody())
    const r = await fetchUndeclared('cfg-a', 'air26', '2026-10-13T00:00:00Z')
    expect(hostFetch).toHaveBeenCalledWith('cfg-a', '/api/pool/hosts/air26/undeclared?since=2026-10-13T00%3A00%3A00Z')
    expect(r.groups).toHaveLength(2)
  })
  it('an answer about another host than the one asked for is unreadable (wwd_bad_response)', async () => {
    ok(ubody({ host: 'mlab' }))
    await expect(fetchUndeclared('cfg-a', 'air26', 'x')).rejects.toMatchObject({ poolError: { code: 'wwd_bad_response' } })
  })
  it('a 403 is the forbidden class', async () => {
    vi.mocked(hostFetch).mockResolvedValue(new Response('{}', { status: 403 }))
    await expect(fetchUndeclared('cfg-a', 'air26', 'x')).rejects.toMatchObject({ status: 403, poolError: { code: 'forbidden' } })
  })
})
