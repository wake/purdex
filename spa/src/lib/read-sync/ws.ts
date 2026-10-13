// spa/src/lib/read-sync/ws.ts — the two read frames of `/ws/host-events?reads=v1` (app-badge spec §7.2):
//   `session.reads`  {"reads":[{"code":…,"through":T}]}  on connect: every read the daemon remembers (`[]` for none);
//   `session.read`   {"code":…,"through":T}               one per accepted read (`session` is the code).
// Both are the usual host-event envelope: the body is a JSON string in `value`. Each read is applied by the cover rule
// (`useAgentStore.applyReads`), which is idempotent and monotonic, so the snapshot and the live frames need no relative
// order. A received read is NEVER reported back: that would echo a read between devices forever.
import { useAgentStore } from '../../stores/useAgentStore'
import type { HostEvent } from '../host-events'
import { parseReadCursor, type ReadCursor } from './cursor'

interface Read {
  code: string
  through: ReadCursor
}

function parseRead(v: unknown): Read | null {
  if (typeof v !== 'object' || v === null) return null
  const { code, through } = v as Record<string, unknown>
  const cursor = parseReadCursor(through)
  if (typeof code !== 'string' || code === '' || !cursor) return null
  return { code, through: cursor }
}

function parseBody(value: string): unknown {
  try {
    return JSON.parse(value)
  } catch {
    return undefined
  }
}

/**
 * Handles `session.reads` / `session.read`; false for any other frame. The envelope is validated whole (a frame that
 * is not the shape is dropped); inside a snapshot a malformed entry is skipped and the others still apply.
 */
export function handleReadsEvent(hostId: string, event: HostEvent): boolean {
  if (event.type !== 'session.reads' && event.type !== 'session.read') return false
  const body = parseBody(event.value)
  if (event.type === 'session.read') {
    const read = parseRead(body)
    if (read) useAgentStore.getState().applyReads(hostId, [read])
    return true
  }
  const reads = typeof body === 'object' && body !== null ? (body as { reads?: unknown }).reads : undefined
  if (!Array.isArray(reads)) return true
  const valid: Read[] = []
  for (const r of reads) {
    const read = parseRead(r)
    if (read) valid.push(read)
  }
  if (valid.length > 0) useAgentStore.getState().applyReads(hostId, valid)
  return true
}
