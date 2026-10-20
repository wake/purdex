// spa/src/lib/pool/exceptions.ts — the tolerant parse of `GET /api/pool/hosts/{alias}/exceptions`: the wire object `{ host, known, exceptions: [wire.Exception] }`
// (the bare list an older daemon relayed is still read; the daemon always sends an array when known, so a missing or non-array `exceptions` is a bad answer, i.e. not known). `known:false` — a host too old to say — is "not known" (null), not an empty list. An entry that does not
// read is left out; `allow` and `flags` are read (a class or flag the App does not offer is dropped). The App shows these as 「暫停」.
import { PAUSE_CLASSES, PAUSE_FLAGS } from './pause'
import type { PoolExceptionDetail, PoolHold } from './types'

const isObj = (v: unknown): v is Record<string, unknown> => typeof v === 'object' && v !== null && !Array.isArray(v)

const strings = (v: unknown): string[] => (Array.isArray(v) ? v.filter((x): x is string => typeof x === 'string') : [])

export function parseExceptionList(json: unknown): PoolExceptionDetail[] | null {
  let list: unknown = json
  if (isObj(json)) {
    if (json.known !== true) return null
    list = json.exceptions
  }
  if (!Array.isArray(list)) return null
  const out: PoolExceptionDetail[] = []
  for (const e of list) {
    if (!isObj(e) || typeof e.name !== 'string' || e.name === '') continue
    const until = typeof e.until === 'string' && !Number.isNaN(Date.parse(e.until)) ? Date.parse(e.until) : null
    const pid = typeof e.pid === 'number' && e.pid > 0 ? e.pid : null
    const holds = Array.isArray(e.holds) ? e.holds.filter((h): h is PoolHold => h === 'heavy' || h === 'light') : []
    const entry: PoolExceptionDetail = { name: e.name, until, pid, holds }
    if (isObj(e.allow)) {
      entry.allow = {
        classes: strings(e.allow.classes).filter((c) => (PAUSE_CLASSES as readonly string[]).includes(c)),
        repos: strings(e.allow.repos),
      }
    }
    const flags = strings(e.flags).filter((f) => (PAUSE_FLAGS as readonly string[]).includes(f))
    if (flags.length > 0) entry.flags = flags
    out.push(entry)
  }
  return out
}
