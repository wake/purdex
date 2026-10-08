// spa/src/lib/agent-lights/hook-cursor.ts — the per-connection cursor of the daemon's `hook` frames (U1-3b, spec §7).
//
// A daemon at alpha.612+ stamps every `hook` frame with `(epoch, seq)` (one contiguous counter per daemon process)
// and sends an `agent.snapshot` frame first to a client that opted in with `?agent=v2`. The client applies the
// snapshot, then only frames that follow it without a hole; anything else is dropped or forces a resync.
// The cursor is runtime-only and per connection; it shares nothing with the `nex.*` `(epoch, bseq)` cursor.
import type { NormalizedEvent } from '../../stores/useAgentStore'

/** null = no snapshot applied on this connection yet. `last` is the seq of the last frame the cursor accepted. */
export type HookCursor = { epoch: string; last: number } | null

export type HookDecision = 'legacy' | 'drop' | 'apply' | 'resync'

const isSeq = (v: unknown): v is number => typeof v === 'number' && Number.isSafeInteger(v) && v >= 0
const isEpoch = (v: unknown): v is string => typeof v === 'string' && v !== ''

/**
 * What to do with a `hook` frame's value given the connection's cursor. The caller advances `cursor.last` on `apply`.
 *
 * - neither `epoch` nor `seq`: a daemon before alpha.611 (no snapshot, no cursor) → `legacy` (apply at once); on a
 *   connection that already has a cursor a v2 daemon always stamps both, so it is dropped;
 * - either present but malformed → `drop`;
 * - no snapshot yet on this connection → `drop` (the snapshot will carry the state);
 * - a different epoch → `resync` (only a snapshot changes the epoch; a counter rotation lands here too);
 * - `seq` not after the last → `drop` (duplicate / old); `seq` skipping ahead → `resync` (a frame was lost).
 */
export function decideHookFrame(cursor: HookCursor, value: { epoch?: unknown; seq?: unknown }): HookDecision {
  const { epoch, seq } = value
  if (epoch === undefined && seq === undefined) {
    if (cursor === null) return 'legacy'
    console.warn('[agent-lights] hook frame without epoch/seq on a v2 connection dropped')
    return 'drop'
  }
  if (!isEpoch(epoch) || !isSeq(seq)) {
    console.warn('[agent-lights] hook frame with a malformed epoch/seq dropped', epoch, seq)
    return 'drop'
  }
  if (cursor === null) return 'drop'
  if (epoch !== cursor.epoch) return 'resync'
  if (seq <= cursor.last) return 'drop'
  if (seq !== cursor.last + 1) return 'resync'
  return 'apply'
}

export interface AgentSnapshotFrame {
  epoch: string
  seq: number
  sessions: { session: string; event: NormalizedEvent }[]
}

/** The `value` of an `agent.snapshot` frame, validated; null (with a warning) for anything malformed. */
export function parseAgentSnapshot(value: unknown): AgentSnapshotFrame | null {
  const bad = (why: string): null => {
    console.warn('[agent-lights] malformed agent.snapshot dropped:', why)
    return null
  }
  if (typeof value !== 'object' || value === null) return bad('not an object')
  const v = value as Record<string, unknown>
  if (!isEpoch(v.epoch)) return bad('epoch')
  if (!isSeq(v.seq)) return bad('seq')
  if (!Array.isArray(v.sessions)) return bad('sessions')
  const sessions: AgentSnapshotFrame['sessions'] = []
  for (const s of v.sessions) {
    if (typeof s !== 'object' || s === null) return bad('session entry')
    const e = s as Record<string, unknown>
    if (typeof e.session !== 'string' || e.session === '') return bad('session code')
    if (typeof e.event !== 'object' || e.event === null) return bad('session event')
    sessions.push({ session: e.session, event: e.event as NormalizedEvent })
  }
  return { epoch: v.epoch, seq: v.seq, sessions }
}
