// spa/src/lib/nex/tasks.ts — the per-execution background task table
// (Bash `run_in_background` / subagents) built from nexen's durable
// `task_start` / `task_end` events and corrected by `/tasks` snapshots
// (capability-matrix §3 "task_start／task_end 的 payload", consumer-guide §9).
// Pure: the event reducer and the pane feed it.
//
// Two rules hold everywhere here:
// - closure is final. Each task_id ends exactly once; a replayed task_start
//   or a snapshot read before the task_end must never reopen a closed row.
// - task_end is the complete END state: it replaces every end field (status,
//   provider_status, closed_by, ended_at, summary, usage, …) wholesale — no
//   merging of old summary/usage. It does not carry the start facts
//   (description, task_type, command, started_at), so those stay from the
//   row's task_start.
import type { TaskKind, TaskStatus, TaskUsage, WorkerTask } from './types'

export type TaskTable = Record<string, WorkerTask>

// task_id is wire data: "__proto__" / "constructor" / "toString" must be
// ordinary rows. Reads only see own keys; writes define an own data
// property (a plain `t[id] = row` with "__proto__" would set the prototype).
const rowOf = (table: TaskTable, id: string): WorkerTask | undefined => (Object.hasOwn(table, id) ? table[id] : undefined)
function setRow(table: TaskTable, id: string, row: WorkerTask): void {
  Object.defineProperty(table, id, { value: row, writable: true, enumerable: true, configurable: true })
}
function withRow(table: TaskTable, id: string, row: WorkerTask): TaskTable {
  const next = { ...table }
  setRow(next, id, row)
  return next
}

const isRecord = (v: unknown): v is Record<string, unknown> => typeof v === 'object' && v !== null && !Array.isArray(v)
const str = (v: unknown): string => (typeof v === 'string' ? v : '')
/** Non-empty string or null — the wire's "key always present, null when unknown" fields. */
const strOrNull = (v: unknown): string | null => (typeof v === 'string' && v !== '' ? v : null)
const finiteOrNull = (v: unknown): number | null => (typeof v === 'number' && Number.isFinite(v) ? v : null)
const isCount = (v: unknown): v is number => typeof v === 'number' && Number.isFinite(v) && v >= 0

const KINDS: ReadonlySet<string> = new Set<TaskKind>(['shell', 'subagent', 'other'])
const TERMINAL: ReadonlySet<string> = new Set<TaskStatus>(['completed', 'failed', 'killed', 'lost'])

function kindOf(v: unknown): TaskKind {
  return typeof v === 'string' && KINDS.has(v) ? (v as TaskKind) : 'other'
}

/** Absent → running (a task_start payload has no status); unknown → failed, like nexen's own fail-closed rule. */
function statusOf(v: unknown): TaskStatus {
  if (v === undefined || v === 'running') return 'running'
  return typeof v === 'string' && TERMINAL.has(v) ? (v as TaskStatus) : 'failed'
}

function usageOf(v: unknown): TaskUsage | undefined {
  if (!isRecord(v) || !isCount(v.total_tokens) || !isCount(v.tool_uses) || !isCount(v.duration_ms)) return undefined
  return { total_tokens: v.total_tokens, tool_uses: v.tool_uses, duration_ms: v.duration_ms }
}

/**
 * A `task_start` / `task_end` payload or a `/tasks` item → a row, or null
 * when it has no usable `task_id`. Every field is guarded; `cost_usd` is
 * dropped (always null by contract).
 */
export function parseTask(raw: unknown, startSeq: number): WorkerTask | null {
  if (!isRecord(raw) || typeof raw.task_id !== 'string' || raw.task_id === '') return null
  const t: WorkerTask = {
    task_id: raw.task_id,
    turn_id: str(raw.turn_id),
    kind: kindOf(raw.kind),
    task_type: str(raw.task_type),
    tool_use_id: strOrNull(raw.tool_use_id),
    parent_tool_use_id: strOrNull(raw.parent_tool_use_id),
    description: str(raw.description),
    backgrounded: raw.backgrounded === true,
    status: statusOf(raw.status),
    provider_status: strOrNull(raw.provider_status),
    closed_by: strOrNull(raw.closed_by),
    started_at: finiteOrNull(raw.started_at),
    ended_at: finiteOrNull(raw.ended_at),
    startSeq,
  }
  if (typeof raw.command === 'string' && raw.command !== '') t.command = raw.command
  if (typeof raw.subagent_type === 'string' && raw.subagent_type !== '') t.subagent_type = raw.subagent_type
  if (typeof raw.summary === 'string' && raw.summary !== '') t.summary = raw.summary
  const usage = usageOf(raw.usage)
  if (usage) t.usage = usage
  return t
}

/** The start facts a row keeps across its task_end (the end payload does not carry them). */
function withStartFacts(end: WorkerTask, prev: WorkerTask | undefined): WorkerTask {
  const { summary, usage, command: _c, subagent_type: _s, ...endRest } = end
  const row: WorkerTask = {
    ...endRest,
    task_type: prev?.task_type || end.task_type,
    parent_tool_use_id: prev ? prev.parent_tool_use_id : end.parent_tool_use_id,
    description: prev?.description || end.description,
    backgrounded: prev ? prev.backgrounded : end.backgrounded,
    started_at: prev ? prev.started_at : end.started_at,
    startSeq: prev ? prev.startSeq : end.startSeq,
  }
  const command = prev?.command ?? end.command
  const subagentType = prev?.subagent_type ?? end.subagent_type
  if (command !== undefined) row.command = command
  if (subagentType !== undefined) row.subagent_type = subagentType
  if (summary !== undefined) row.summary = summary
  if (usage !== undefined) row.usage = usage
  return row
}

/**
 * Fold one durable task event into the table. Returns the same table for
 * anything that changes nothing (other kinds, invalid payloads, a start
 * replayed after its end).
 */
export function applyTaskEvent(table: TaskTable, kind: string, payload: unknown, seq: number): TaskTable {
  if (kind !== 'task_start' && kind !== 'task_end') return table
  const parsed = parseTask(payload, seq)
  if (!parsed) return table
  const prev = rowOf(table, parsed.task_id)
  if (kind === 'task_start') {
    if (prev && prev.status !== 'running') return table
    const row: WorkerTask = { ...parsed, status: 'running', provider_status: null, closed_by: null, ended_at: null, startSeq: prev ? prev.startSeq : seq }
    delete row.summary
    delete row.usage
    return withRow(table, row.task_id, row)
  }
  // An end that says running is not an end the contract can produce; it
  // still closes the row, fail-closed.
  const end = parsed.status === 'running' ? { ...parsed, status: 'failed' as const } : parsed
  return withRow(table, end.task_id, withStartFacts(end, prev))
}

/**
 * Merge a `/tasks` snapshot (nexen issue #83 correction: re-read after every
 * (re)open). The snapshot is the authority for running rows, except:
 * - a row already closed here stays closed (the snapshot was read before a
 *   task_end that has since arrived live);
 * - a running row the snapshot omits is dropped only if it started at or
 *   before `cursor` — nexen reads the cursor before the rows, so a row that
 *   started later is simply newer than the snapshot;
 * - closed rows the snapshot omits are kept (a `state=running` snapshot
 *   never lists them).
 * Rows first seen here get `startSeq = cursor`; known rows keep theirs.
 */
export function applyTaskSnapshot(table: TaskTable, items: WorkerTask[], cursor: number): TaskTable {
  const next: TaskTable = {}
  const inSnapshot = new Set<string>()
  for (const item of items) inSnapshot.add(item.task_id)
  for (const [id, row] of Object.entries(table)) {
    if (row.status !== 'running' || inSnapshot.has(id) || row.startSeq > cursor) setRow(next, id, row)
  }
  for (const item of items) {
    const prev = rowOf(table, item.task_id)
    if (prev && prev.status !== 'running') continue
    setRow(next, item.task_id, { ...item, startSeq: prev ? prev.startSeq : cursor })
  }
  return next
}

/** Running rows, oldest first (by start time, then start seq). */
export function runningTasks(table: TaskTable): WorkerTask[] {
  return Object.values(table)
    .filter((t) => t.status === 'running')
    .sort((a, b) => (a.started_at ?? Infinity) - (b.started_at ?? Infinity) || a.startSeq - b.startSeq)
}
