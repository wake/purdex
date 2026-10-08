// spa/src/lib/nex/nex-host-events.ts — the host-events side of #1866 (§3.5, §4.4): the opt-in query parameter and the
// parsing of the two nex frames. `value` is a JSON string; anything malformed is ignored with a warning, never thrown.
import { sanitizeExecutionsPage } from './validate-executions'
import type { NexDelta, NexHello } from './execution-list-effects'

export const NEX_HELLO_EVENT = 'nex.executions.hello'
export const NEX_DELTA_EVENT = 'nex.execution'
/** The daemon accepts exactly one `nex=v1`; without it no nex frame is sent on the connection (§3.5). */
export const NEX_OPT_IN = { key: 'nex', value: 'v1' } as const

export const isNexHostEvent = (type: string): boolean => type === NEX_HELLO_EVENT || type === NEX_DELTA_EVENT

const isRecord = (v: unknown): v is Record<string, unknown> => typeof v === 'object' && v !== null && !Array.isArray(v)
const isCount = (v: unknown): v is number => typeof v === 'number' && Number.isSafeInteger(v) && v >= 0

function parseValue(raw: unknown): Record<string, unknown> | null {
  try {
    const v: unknown = typeof raw === 'string' ? JSON.parse(raw) : raw
    return isRecord(v) ? v : null
  } catch {
    return null
  }
}

export function parseNexHello(raw: unknown): NexHello | null {
  const v = parseValue(raw)
  if (!v || typeof v.epoch !== 'string' || v.epoch === '' || !isCount(v.bseq)) return null
  return { epoch: v.epoch, bseq: v.bseq }
}

export function parseNexDelta(raw: unknown): NexDelta | null {
  const v = parseValue(raw)
  if (!v || typeof v.epoch !== 'string' || v.epoch === '' || !isCount(v.bseq)) return null
  if (typeof v.id !== 'string' || v.id === '' || !isCount(v.ver)) return null
  if (!Array.isArray(v.cause) || !v.cause.every((c) => typeof c === 'string')) return null
  let row: NexDelta['row'] = null
  if (v.row !== null) {
    // The same coercion a list page gets, so a delta can never put a row into the cache that a page would have dropped.
    const page = sanitizeExecutionsPage({ items: [v.row] })
    if (page.items.length !== 1 || page.items[0].id !== v.id) return null
    row = page.items[0]
  }
  return { epoch: v.epoch, bseq: v.bseq, id: v.id, ver: v.ver, cause: v.cause as string[], row }
}

/** Parse and hand over one nex frame; true when the event was a nex frame (handled or ignored). */
export function dispatchNexHostEvent(
  hostId: string,
  event: { type: string; value: unknown },
  sink: { onHello: (hostId: string, hello: NexHello) => void; applyDelta: (hostId: string, delta: NexDelta) => void },
): boolean {
  if (event.type === NEX_HELLO_EVENT) {
    const hello = parseNexHello(event.value)
    if (hello) sink.onHello(hostId, hello)
    else console.warn('nex-delta: malformed hello ignored', { hostId })
    return true
  }
  if (event.type === NEX_DELTA_EVENT) {
    const delta = parseNexDelta(event.value)
    if (delta) sink.applyDelta(hostId, delta)
    else console.warn('nex-delta: malformed delta ignored', { hostId })
    return true
  }
  return false
}
