import { describe, it, expect, vi, beforeEach } from 'vitest'
import hostsJson from './__fixtures__/pool_hosts_response.json'

const hostFetch = vi.fn()
// A rejection is made outside the spy: vitest reports a promise a spy returned and that rejects as an unhandled error.
let offline = false
vi.mock('../host-api', () => ({ hostFetch: (...a: unknown[]) => (offline ? Promise.reject(new TypeError('failed')) : hostFetch(...a)) }))

import { fetchFactsHistory, fetchPoolHosts, fetchPoolSeries, PoolReadError } from './api'

const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status })

beforeEach(() => { hostFetch.mockReset(); offline = false })

describe('fetchPoolHosts', () => {
  it('reads the hosts from the named daemon', async () => {
    hostFetch.mockResolvedValue(json(200, hostsJson))
    const hosts = await fetchPoolHosts('d1')
    expect(hostFetch).toHaveBeenCalledWith('d1', '/api/pool/hosts')
    expect(hosts.map((h) => h.alias)).toEqual(['air19'])
  })

  it('a refused read carries its classification', async () => {
    hostFetch.mockResolvedValue(json(503, { error: 'wwd_unavailable' }))
    const err = await fetchPoolHosts('d1').catch((e) => e)
    expect(err).toBeInstanceOf(PoolReadError)
    expect(err.poolError).toEqual({ code: 'wwd_unavailable', action: 'next_source' })
  })

  it('a network failure is no answer', async () => {
    offline = true
    const err = await fetchPoolHosts('d1').catch((e) => e)
    expect(err.poolError).toEqual({ code: 'no_answer', action: 'next_source' })
  })

  it('an answer that parses as nothing is outside the contract', async () => {
    hostFetch.mockResolvedValue(json(200, { hosts: 3 }))
    const err = await fetchPoolHosts('d1').catch((e) => e)
    expect(err.poolError.code).toBe('wwd_bad_response')
  })

  it('a non-JSON error body is classified by its status', async () => {
    hostFetch.mockResolvedValue(new Response('busy', { status: 429 }))
    const err = await fetchPoolHosts('d1').catch((e) => e)
    expect(err.poolError.code).toBe('busy')
  })
})

describe('fetchPoolSeries', () => {
  it('names the host, since and step', async () => {
    hostFetch.mockResolvedValue(json(200, { step: '1m', points: [], truncated: false }))
    await fetchPoolSeries('d1', 'air19', '2026-10-18T05:00:00Z', '1m')
    expect(hostFetch).toHaveBeenCalledWith('d1', '/api/pool/facts/series?host=air19&since=2026-10-18T05%3A00%3A00Z&step=1m')
  })

  it('host_not_local keeps the gauges and drops the chart', async () => {
    hostFetch.mockResolvedValue(json(501, { error: 'host_not_local' }))
    const err = await fetchPoolSeries('d1', 'air19', 'x', '1m').catch((e) => e)
    expect(err.poolError).toEqual({ code: 'host_not_local', action: 'no_chart' })
  })
})

describe('fetchFactsHistory', () => {
  it('asks the daemon for the host\'s 5 s records since a time', async () => {
    hostFetch.mockResolvedValue(json(200, { step: '5s', records: [] }))
    const h = await fetchFactsHistory('d1', 'air19', '2026-10-18T05:00:00Z', '5s')
    expect(hostFetch).toHaveBeenCalledWith('d1', '/api/pool/facts/history?host=air19&since=2026-10-18T05%3A00%3A00Z&step=5s')
    expect(h).toEqual({ step: '5s', records: [] })
  })
  it('host_not_local is a refusal that keeps its code', async () => {
    hostFetch.mockResolvedValue(json(501, { error: 'host_not_local' }))
    const err = await fetchFactsHistory('d1', 'air19', '2026-10-18T05:00:00Z', '5s').catch((e) => e)
    expect(err).toBeInstanceOf(PoolReadError)
    expect(err.poolError.code).toBe('host_not_local')
  })
  it('an answer that is not a history is wwd_bad_response', async () => {
    hostFetch.mockResolvedValue(json(200, { step: 'weird' }))
    const err = await fetchFactsHistory('d1', 'air19', '2026-10-18T05:00:00Z', '5s').catch((e) => e)
    expect(err.poolError.code).toBe('wwd_bad_response')
  })
})
