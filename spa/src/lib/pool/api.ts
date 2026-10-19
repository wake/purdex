// spa/src/lib/pool/api.ts — reads of `/api/pool/*` from one daemon. A refused read throws a PoolReadError carrying the classification.
import { hostFetch } from '../host-api'
import { classify, type PoolCallKind, type PoolError } from './errors'
import { parseHostsAnswer, parseSeries } from './parse'
import type { PoolHost, PoolSeries } from './types'

export class PoolReadError extends Error {
  constructor(readonly path: string, readonly status: number | null, readonly poolError: PoolError) {
    super(`GET ${path}: ${status ?? 'no answer'} (${poolError.code})`)
  }
}

async function readJson(hostId: string, path: string, kind: PoolCallKind = 'read'): Promise<unknown> {
  let res: Response
  try {
    res = await hostFetch(hostId, path)
  } catch {
    throw new PoolReadError(path, null, classify(null, null, kind))
  }
  let body: unknown = null
  try { body = await res.json() } catch { /* a body that is not JSON is classified by its status */ }
  if (!res.ok) throw new PoolReadError(path, res.status, classify(res.status, body, kind))
  return body
}

/** `unreadable` is an answer that parsed as nothing: the daemon is outside the contract, like wwd_bad_response. */
const unreadable = (path: string) => new PoolReadError(path, 200, { code: 'wwd_bad_response', action: 'next_source' })

export async function fetchPoolHosts(hostId: string): Promise<PoolHost[]> {
  const path = '/api/pool/hosts'
  const hosts = parseHostsAnswer(await readJson(hostId, path))
  if (!hosts) throw unreadable(path)
  return hosts
}

/** `GET /api/pool/facts/series` (`pool.series.v1`). `since` is an RFC 3339 time. */
export async function fetchPoolSeries(hostId: string, alias: string, since: string, step: string): Promise<PoolSeries> {
  const path = `/api/pool/facts/series?host=${encodeURIComponent(alias)}&since=${encodeURIComponent(since)}&step=${encodeURIComponent(step)}`
  const series = parseSeries(await readJson(hostId, path))
  if (!series) throw unreadable(path)
  return series
}
