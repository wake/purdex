// The wake settings document: each contract fixture reads as the plan §6 says, a body of the wrong shape is `app_mismatch`, only a throwing fetch is `no_answer`, and an unknown field survives `withPath`.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import get from './__fixtures__/watch_wake_settings_get.json'
import getStale from './__fixtures__/watch_wake_settings_get_stale.json'
import getFull from './__fixtures__/watch_wake_settings_get_full.json'
import getReadonly from './__fixtures__/watch_wake_settings_get_readonly.json'
import put200 from './__fixtures__/watch_wake_settings_put_200.json'
import put400 from './__fixtures__/watch_wake_settings_put_400.json'
import put400Missing from './__fixtures__/watch_wake_settings_put_400_missing_expect.json'
import put403 from './__fixtures__/watch_wake_settings_put_403.json'
import put409 from './__fixtures__/watch_wake_settings_put_409.json'
import put503 from './__fixtures__/watch_wake_settings_put_503.json'
import { fieldIdOfPath, hasAppMismatch } from './wake-settings-model'
import { useHostStore } from '../../stores/useHostStore'

// The page's `fetch` is what the host helper ends in: stubbing it also shows that nothing is sent for a host this device lacks.
const hostFetch = vi.fn()

import { fetchWakeSettings, parseWakeSettingsResponse, putWakeSettings, readPath, withPath } from './wake-settings'

const reply = (status: number, body: unknown) => new Response(typeof body === 'string' ? body : JSON.stringify(body), { status })
beforeEach(() => {
  hostFetch.mockReset()
  vi.stubGlobal('fetch', hostFetch)
  useHostStore.setState({ hosts: { A: { id: 'A', name: 'A', ip: '127.0.0.1', port: 7860, token: 't' } } as never, hostOrder: ['A'], runtime: {} as never })
})

describe('parseWakeSettingsResponse', () => {
  it('reads the GET fixture: every field has its type', () => {
    const d = parseWakeSettingsResponse(get)!
    expect(d.etag).toBe(get.etag)
    expect(d.revision).toBe(1)
    expect(d.source).toBe('mlab')
    expect(d.sourceId).toBe(get.source_id)
    expect(d.isSource).toBe(true)
    expect(d.stale).toBe(false)
    expect(d.readonly).toBe(false)
    expect(d.fetchedAt).toBe(get.fetched_at)
    expect(d.writable).toBe(true)
    expect(Object.keys(d.settings.rules as object)).toEqual(['patrol', 'disk_low', 'wwd_alert', 'unattended_changed', 'user_back'])
    expect(d.knownKinds.find((k) => k.kind === 'orphans')).toEqual({ kind: 'orphans', levels: ['digest', 'push'], label: '孤兒行程' })
    expect(d.suppressed).toEqual([])
    expect(d.patrolStatus).toEqual({ lastOkAt: null, lastOkHost: null, lastFailAt: null, lastFailCode: null, lastFailDetail: null })
  })

  it('a document never read is not writable and names nobody', () => {
    const d = parseWakeSettingsResponse(getStale)!
    expect(d.fetchedAt).toBeNull()
    expect(d.stale).toBe(true)
    expect(d.sourceId).toBe('')
    expect(d.writable).toBe(false)
  })

  it('reads the full fixture: a blocked record and a failed patrol', () => {
    const d = parseWakeSettingsResponse(getFull)!
    expect(d.suppressed.map((r) => r.why)).toEqual(['level_off', 'kind_off', 'patrol_failed'])
    expect(d.patrolStatus.lastFailCode).toBe('targets_unreachable')
    expect(d.patrolStatus.lastOkHost).toBe('mlab')
    expect(d.writable).toBe(true)
  })

  it('a readonly document is not writable', () => {
    expect(parseWakeSettingsResponse(getReadonly)!.writable).toBe(false)
  })

  it('a document of another version is not writable (copied from the GET fixture, version 2)', () => {
    const d = parseWakeSettingsResponse({ ...get, settings: { ...get.settings, version: 2 } })!
    expect(d.writable).toBe(false)
  })

  it('reads the PUT 200 fixture with the same parser: a multi-line prompt, times and kinds_off', () => {
    const d = parseWakeSettingsResponse(put200)!
    expect(d.revision).toBe(2)
    const rules = d.settings.rules as Record<string, Record<string, unknown>>
    expect(String(rules.patrol.prompt)).toContain('\n')
    expect((rules.patrol.times as string[]).length).toBeGreaterThan(0)
    expect((rules.wwd_alert.kinds_off as string[]).length).toBeGreaterThan(0)
  })

  it('a field a type would drop is kept in `settings`', () => {
    const d = parseWakeSettingsResponse({ ...get, settings: { ...get.settings, future: { a: 1 } } })!
    expect(d.settings.future).toEqual({ a: 1 })
  })
})

describe('readPath / withPath', () => {
  it('reads a nested path and gives undefined for a missing one', () => {
    expect(readPath(get.settings, 'rules.patrol.max_gap_hours')).toBe(24)
    expect(readPath(get.settings, 'rules.nope.x')).toBeUndefined()
  })

  it('changes only that path: the rest is deep-equal, and an unknown field stays', () => {
    const doc = { ...get.settings, future: { a: 1 }, rules: { ...get.settings.rules, patrol: { ...get.settings.rules.patrol, extra: [1] } } }
    const next = withPath(doc, 'rules.patrol.max_gap_hours', 48)
    expect(readPath(next, 'rules.patrol.max_gap_hours')).toBe(48)
    expect(next.future).toEqual({ a: 1 })
    expect(readPath(next, 'rules.patrol.extra')).toEqual([1])
    expect(withPath(next, 'rules.patrol.max_gap_hours', 24)).toEqual(doc)
    expect(readPath(doc, 'rules.patrol.max_gap_hours')).toBe(24) // the original is untouched
  })
})

describe('responses', () => {
  it('a host this device does not have is never asked: nothing is sent, so the request cannot fall back to another daemon', async () => {
    expect(await fetchWakeSettings('gone')).toEqual({ kind: 'no_answer' })
    expect(await putWakeSettings('gone', get.settings, 'x')).toEqual({ kind: 'no_answer' })
    expect(hostFetch).not.toHaveBeenCalled()
  })

  it('GET: 200 reads, a throwing fetch is no_answer, other statuses are server_error', async () => {
    hostFetch.mockResolvedValueOnce(reply(200, get))
    expect((await fetchWakeSettings('A')).kind).toBe('ok')
    expect(String(hostFetch.mock.calls[0][0])).toMatch(/127\.0\.0\.1:7860\/api\/watch\/wake-settings$/)
    hostFetch.mockRejectedValueOnce(new Error('down'))
    expect(await fetchWakeSettings('A')).toEqual({ kind: 'no_answer' })
    for (const s of [500, 502, 404]) {
      hostFetch.mockResolvedValueOnce(reply(s, 'oops'))
      expect(await fetchWakeSettings('A')).toEqual({ kind: 'server_error', status: s })
    }
  })

  it('PUT sends the whole settings with the etag', async () => {
    hostFetch.mockResolvedValueOnce(reply(200, put200))
    expect((await putWakeSettings('A', get.settings, 'abc')).kind).toBe('ok')
    const [url, init] = hostFetch.mock.calls[0] as [string, RequestInit]
    expect(url).toMatch(/\/api\/watch\/wake-settings$/)
    expect(init.method).toBe('PUT')
    expect(JSON.parse(init.body as string)).toEqual({ settings: get.settings, expect_etag: 'abc' })
  })

  it('400 invalid fixture: three fields; the unknown_field makes the whole thing a format mismatch; each path finds its field', async () => {
    hostFetch.mockResolvedValueOnce(reply(400, put400))
    const r = await putWakeSettings('A', get.settings, 'x')
    expect(r.kind).toBe('invalid')
    if (r.kind !== 'invalid') return
    expect(r.fields.map((f) => [f.path, f.code])).toEqual([['rules.patrol.enabled', 'unknown_field'], ['rules.patrol.max_gap_hours', 'out_of_range'], ['rules.patrol.times[0]', 'bad_time']])
    expect(r.fields[2].message).toContain('HH:MM')
    expect(hasAppMismatch(r.fields)).toBe(true)
    expect(r.fields.map((f) => fieldIdOfPath(f.path))).toEqual([null, 'patrol.max_gap_hours', 'patrol.times'])
  })

  it('400 missing_expect, 403, 409 and 503 fixtures', async () => {
    hostFetch.mockResolvedValueOnce(reply(400, put400Missing))
    expect(await putWakeSettings('A', get.settings, 'x')).toEqual({ kind: 'app_mismatch' })
    hostFetch.mockResolvedValueOnce(reply(403, put403))
    expect(await putWakeSettings('A', get.settings, 'x')).toEqual({ kind: 'forbidden' })
    hostFetch.mockResolvedValueOnce(reply(409, put409))
    const c = await putWakeSettings('A', get.settings, 'x')
    expect(c.kind).toBe('conflict')
    if (c.kind === 'conflict') expect(c.current.revision).toBe(1)
    hostFetch.mockResolvedValueOnce(reply(503, put503))
    expect(await putWakeSettings('A', get.settings, 'x')).toEqual({ kind: 'unreachable', source: 'mlab' })
  })

  // A settings object whose known structure is broken is a format mismatch, in a 200 and in a 409's `current` alike.
  const rules = get.settings.rules
  const brokenSettings: [string, unknown][] = [
    ['no version', { ...get.settings, version: undefined }],
    ['no rules', { version: 1 }],
    ['rules is a list', { ...get.settings, rules: [] }],
    ['a rule is missing', { ...get.settings, rules: { ...rules, user_back: undefined } }],
    ['a rule is not an object', { ...get.settings, rules: { ...rules, disk_low: 3 } }],
    ['a switch is a string', { ...get.settings, enabled: 'yes' }],
    ['a number is a string', { ...get.settings, rules: { ...rules, disk_low: { ...rules.disk_low, below_gb: '14' } } }],
    ['a prompt is a number', { ...get.settings, rules: { ...rules, patrol: { ...rules.patrol, prompt: 1 } } }],
    ['times has a non-string', { ...get.settings, rules: { ...rules, patrol: { ...rules.patrol, times: [9] } } }],
    ['kinds_off is not a list', { ...get.settings, rules: { ...rules, wwd_alert: { ...rules.wwd_alert, kinds_off: 'x' } } }],
    ['an interval is missing', { ...get.settings, rules: { ...rules, patrol: { ...rules.patrol, interval_min: { resting: 10 } } } }],
  ]
  it.each(brokenSettings)('settings with %s is app_mismatch (200 and 409)', async (_name, settings) => {
    hostFetch.mockResolvedValueOnce(reply(200, { ...get, settings }))
    expect(await putWakeSettings('A', get.settings, 'x')).toEqual({ kind: 'app_mismatch' })
    hostFetch.mockResolvedValueOnce(reply(409, { error: 'etag_mismatch', current: { ...get, settings } }))
    expect(await putWakeSettings('A', get.settings, 'x')).toEqual({ kind: 'app_mismatch' })
  })

  it('a document of another version is read, not checked field by field (it is not writable anyway)', () => {
    expect(parseWakeSettingsResponse({ ...get, settings: { version: 2, shape: 'new' } })?.writable).toBe(false)
  })

  const { etag: _etag, ...noEtag } = get
  const { settings: _settings, ...noSettings } = get
  it.each([
    ['200 body is not an object', 200, '[1]'],
    ['200 without settings', 200, noSettings],
    ['200 without etag', 200, noEtag],
    ['200 without revision', 200, { ...get, revision: undefined }],
    ['200 not JSON', 200, 'not json'],
    ['409 without current', 409, { error: 'etag_mismatch' }],
    ['409 with a current of the wrong shape', 409, { error: 'etag_mismatch', current: { etag: 'x' } }],
    ['400 invalid without fields', 400, { error: 'invalid' }],
    ['400 invalid with fields not an array', 400, { error: 'invalid', fields: {} }],
    ['400 invalid with a field without a path', 400, { error: 'invalid', fields: [{ code: 'bad_time' }] }],
    ['400 invalid with a field without a code', 400, { error: 'invalid', fields: [{ path: 'x' }] }],
    ['403 with another error', 403, { error: 'nope' }],
    ['503 without source', 503, { error: 'source_unreachable' }],
    ['400 not JSON', 400, '<html>'],
  ])('%s is app_mismatch', async (_name, status, body) => {
    hostFetch.mockResolvedValueOnce(reply(status as number, body))
    expect(await putWakeSettings('A', get.settings, 'x')).toEqual({ kind: 'app_mismatch' })
  })
})
