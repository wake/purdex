import { describe, it, expect, vi, beforeEach } from 'vitest'

const hostFetch = vi.fn()
// A plain function in front of the spy: vitest's spy would report a throw of the mock itself as an error of the test.
let impl: (...a: unknown[]) => unknown = hostFetch
vi.mock('../host-api', () => ({ hostFetch: (...a: unknown[]) => impl(...a) }))

import { checkMarks, runProbeCheck } from './probe-check'

const json = (status: number, body: unknown) => ({ ok: status >= 200 && status < 300, status, json: async () => body, text: async () => JSON.stringify(body) })

beforeEach(() => { hostFetch.mockReset(); impl = hostFetch })

describe('runProbeCheck', () => {
  it('POSTs the daemon\'s alias to /api/fleet/probe-check through hostFetch and parses the answer', async () => {
    hostFetch.mockResolvedValue(json(200, { ssh: 'ok', measure: 'ok', error: null, reading: { cpu: 1, mem: 2, level: null, disk: 3 } }))
    const r = await runProbeCheck('ha', 'nas-a')
    expect(hostFetch).toHaveBeenCalledTimes(1)
    const [id, path, init] = hostFetch.mock.calls[0]
    expect([id, path, init.method]).toEqual(['ha', '/api/fleet/probe-check', 'POST'])
    expect(JSON.parse(init.body)).toEqual({ ssh_alias: 'nas-a' })
    expect(r).toEqual({ kind: 'answer', answer: { ssh: 'ok', measure: 'ok', error: null } })
  })
  it('a 404 is "unsupported" (a daemon before F-2c)', async () => {
    hostFetch.mockResolvedValue(json(404, {}))
    expect(await runProbeCheck('ha', 'x')).toEqual({ kind: 'unsupported' })
  })
  it('another failure, or a thrown fetch, is an error with a text', async () => {
    hostFetch.mockResolvedValue(json(403, {}))
    expect((await runProbeCheck('ha', 'x')).kind).toBe('error')
    impl = () => { throw new Error('boom') }
    expect(await runProbeCheck('ha', 'x')).toEqual({ kind: 'error', text: 'boom' })
  })
  it('an unknown mark or code in the answer is read as unknown / null, never as ok', async () => {
    hostFetch.mockResolvedValue(json(200, { ssh: 'weird', measure: 7, error: 'nope' }))
    expect(await runProbeCheck('ha', 'x')).toEqual({ kind: 'answer', answer: { ssh: 'unknown', measure: 'unknown', error: null } })
  })
})

describe('checkMarks: the words of spec 8.2', () => {
  it.each([
    ['auth', 'fleet_hosts.check.auth'],
    ['host_key', 'fleet_hosts.check.host_key'],
    ['alias_gone', 'fleet_hosts.check.alias_gone'],
  ] as const)('%s: SSH failed with its reason, the measure stays unknown', (error, key) => {
    const m = checkMarks({ ssh: 'failed', measure: 'unknown', error })
    expect(m.ssh).toEqual({ mark: 'failed', reasonKey: key })
    expect(m.measure.mark).toBe('unknown')
    expect(m.note).toBeNull()
  })
  it.each(['timeout', 'unreachable'] as const)('%s: both marks unknown and one "cannot reach" note', (error) => {
    const m = checkMarks({ ssh: 'unknown', measure: 'unknown', error })
    expect([m.ssh.mark, m.measure.mark]).toEqual(['unknown', 'unknown'])
    expect(m.note).toBe('fleet_hosts.check.unreachable')
  })
  it.each(['remote_exit', 'parse'] as const)('%s: SSH ok, the reading failed', (error) => {
    const m = checkMarks({ ssh: 'ok', measure: 'failed', error })
    expect(m.ssh.mark).toBe('ok')
    expect(m.measure).toEqual({ mark: 'failed', reasonKey: 'fleet_hosts.check.no_reading' })
  })
  it('ok / ok has no reason and no note; unknown is never ok', () => {
    const m = checkMarks({ ssh: 'ok', measure: 'ok', error: null })
    expect([m.ssh.mark, m.measure.mark, m.note]).toEqual(['ok', 'ok', null])
    expect(checkMarks({ ssh: 'unknown', measure: 'unknown', error: null }).ssh.mark).toBe('unknown')
  })
})
