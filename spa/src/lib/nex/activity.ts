// spa/src/lib/nex/activity.ts — `activity.phase` from an execution summary
// (nexen v0.13 worker_rollup). The phase set is OPEN: `awaiting_input` is
// already announced for `pending_ask`, and the contract (capability-matrix
// §0 `worker_rollup`, consumer-guide §9.3) requires every value this build
// does not know to be read as `model` — never an error, never "unknown".

export type ActivityPhase = 'queued' | 'starting' | 'model' | 'tool' | 'idle' | 'ended'

const KNOWN: ReadonlySet<string> = new Set<ActivityPhase>(['queued', 'starting', 'model', 'tool', 'idle', 'ended'])

export function normalizePhase(phase: unknown): ActivityPhase {
  return typeof phase === 'string' && KNOWN.has(phase) ? (phase as ActivityPhase) : 'model'
}
