// spa/src/lib/pool/exceptions.ts — the tolerant parse of `GET /api/pool/hosts/{alias}/exceptions` (a list of wwd's `wire.Exception`). An entry that does not
// read is left out; an answer that is no list is "not known" (null). The App shows these as 「暫停」.
import type { PoolExceptionDetail, PoolHold } from './types'

const isObj = (v: unknown): v is Record<string, unknown> => typeof v === 'object' && v !== null && !Array.isArray(v)

export function parseExceptionList(json: unknown): PoolExceptionDetail[] | null {
  if (!Array.isArray(json)) return null
  const out: PoolExceptionDetail[] = []
  for (const e of json) {
    if (!isObj(e) || typeof e.name !== 'string' || e.name === '') continue
    const until = typeof e.until === 'string' && !Number.isNaN(Date.parse(e.until)) ? Date.parse(e.until) : null
    const pid = typeof e.pid === 'number' && e.pid > 0 ? e.pid : null
    const holds = Array.isArray(e.holds) ? e.holds.filter((h): h is PoolHold => h === 'heavy' || h === 'light') : []
    out.push({ name: e.name, until, pid, holds })
  }
  return out
}
