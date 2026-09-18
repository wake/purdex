// spa/src/lib/nex/tool-activity.ts — per-tool_use timing derived from the
// durable frames only (P-B2 spec §4.2 A1–A4). Pure: no React, no store.
import { contentBlocks, obj } from './content-blocks'
import type { ExecutionState } from './event-reducer'

/**
 * ToolActivity v2 (P-B3 spec §4.2). The first four fields are the P-B2
 * timing derived from the raw frames; everything after the marker is the
 * N2 overlay copied from the daemon's `tool_use` / `tool_result` events.
 * Overlay fields are optional and absent until seen — absent ≠ null.
 */
export interface ToolActivity {
  name: string
  /** ev.created_at (unix ms, server clock) of the raw assistant frame; 0 = unknown, renderer shows no timer. */
  startedAt: number
  /** ev.created_at of the raw user frame. */
  endedAt: number | null
  status: 'running' | 'done' | 'error' | 'denied' | 'aborted'
  // ---- N2 overlay (all optional; absent = not (yet) seen from N2) ----
  /** null = known tool with no primary key (F9). */
  primaryArg?: { key: string; value: string } | null
  known?: boolean
  /** null = unmatched result (contract rule 9). */
  durationMs?: number | null
  output?: { totalLines: number; totalBytes: number; truncated: boolean; hasNonText: boolean }
  file?: { path: string; lines: number }
  diff?: { path: string; added: number; removed: number; hunks: DiffHunk[]; truncated: boolean }
}

export interface DiffHunk {
  oldStart: number
  oldLines: number
  newStart: number
  newLines: number
  lines: string[]
}

/**
 * What ToolCallBlock renders beyond the tool name + input (P-B2.2 R1/R2).
 * One variant per lifecycle stage, each carrying only the fields that stage
 * can use; absent → today's Stream-mode DOM. `streaming` is the in-flight
 * partial block (PartialMessageGroup); the rest come from the durable
 * ToolActivity via toToolCallActivity.
 */
export type ToolCallActivity =
  | { status: 'streaming'; rawInput: string }
  | { status: 'running'; startedAt: number; now: number }
  | { status: 'done' | 'error'; startedAt: number; endedAt: number; durationMs?: number | null }
  | { status: 'denied'; startedAt: number; endedAt: number; durationMs?: number | null }
  | { status: 'aborted' }

/**
 * Durable ToolActivity → the renderer's activity prop. `now` is only carried
 * by `running`; a done/error entry with no endedAt (never produced by the
 * reducer) maps to undefined so the block renders plain.
 */
export function toToolCallActivity(entry: ToolActivity, now: number): ToolCallActivity | undefined {
  switch (entry.status) {
    case 'running':
      return { status: 'running', startedAt: entry.startedAt, now }
    case 'done':
    case 'error':
    case 'denied':
      if (entry.endedAt == null) return undefined
      // Spread so an entry without durationMs yields a variant without the key (absent ≠ null).
      return {
        status: entry.status,
        startedAt: entry.startedAt,
        endedAt: entry.endedAt,
        ...('durationMs' in entry ? { durationMs: entry.durationMs } : {}),
      }
    case 'aborted':
      return { status: 'aborted' }
  }
}

export function recordToolStarts(s: ExecutionState, p: Record<string, unknown>, at: number): ExecutionState {
  let tools = s.tools
  for (const b of contentBlocks(p)) {
    if (b.type !== 'tool_use' || typeof b.id !== 'string' || tools[b.id]) continue
    tools = { ...tools, [b.id]: { name: typeof b.name === 'string' ? b.name : '', startedAt: at, endedAt: null, status: 'running' } }
  }
  return tools === s.tools ? s : { ...s, tools }
}

export function recordToolEnds(s: ExecutionState, p: Record<string, unknown>, at: number): ExecutionState {
  let tools = s.tools
  for (const b of contentBlocks(p)) {
    if (b.type !== 'tool_result' || typeof b.tool_use_id !== 'string') continue
    const t = tools[b.tool_use_id]
    // done/error/denied are final (denied only comes from N2 — spec N5: a raw
    // result must not downgrade it); an 'aborted' tool (A3 fired on a
    // turn-ending event before its tool_result landed) is still corrected by
    // that result.
    if (!t || t.status === 'done' || t.status === 'error' || t.status === 'denied') continue
    tools = { ...tools, [b.tool_use_id]: { ...t, endedAt: at, status: b.is_error === true ? 'error' : 'done' } }
  }
  return tools === s.tools ? s : { ...s, tools }
}

// ---------------------------------------------------------------------------
// N2 overlay (P-B3 spec §4.3 N1 / N2 / N6 / N7). Pure; the reducer wires
// these in for ev.kind === 'tool_use' | 'tool_result'. Every payload field is
// taken by type check, never by trust (N6); absent keys leave the entry's
// optional fields absent (absent ≠ null).
// ---------------------------------------------------------------------------

const num = (v: unknown): v is number => typeof v === 'number'
const bool = (v: unknown): v is boolean => typeof v === 'boolean'
const str = (v: unknown): v is string => typeof v === 'string'

type Overlay = Pick<ToolActivity, 'primaryArg' | 'known' | 'durationMs' | 'output' | 'file' | 'diff'>

/** `primary_arg` only when the key is present: null is a value (F9); a malformed object counts as absent. */
function readPrimaryArg(p: Record<string, unknown>): Pick<Overlay, 'primaryArg'> {
  if (!('primary_arg' in p)) return {}
  const v = p.primary_arg
  if (v === null) return { primaryArg: null }
  const o = obj(v)
  return o && str(o.key) && str(o.value) ? { primaryArg: { key: o.key, value: o.value } } : {}
}

function readHunk(v: unknown): DiffHunk | null {
  const h = obj(v)
  if (!h || !num(h.old_start) || !num(h.old_lines) || !num(h.new_start) || !num(h.new_lines)) return null
  if (!Array.isArray(h.lines) || !h.lines.every(str)) return null
  return { oldStart: h.old_start, oldLines: h.old_lines, newStart: h.new_start, newLines: h.new_lines, lines: h.lines }
}

/** The tool_result facts; each key only when present and well-formed. A malformed diff (any bad hunk) is dropped whole. */
function readResultFacts(p: Record<string, unknown>): Pick<Overlay, 'durationMs' | 'output' | 'file' | 'diff'> {
  const facts: Pick<Overlay, 'durationMs' | 'output' | 'file' | 'diff'> = {}
  if (p.duration_ms === null || num(p.duration_ms)) facts.durationMs = p.duration_ms
  const out = obj(p.output)
  if (out && num(out.total_lines) && num(out.total_bytes) && bool(out.truncated) && bool(out.has_non_text)) {
    // `text` is deliberately not stored — the raw user block carries the body (spec §4.4 R5).
    facts.output = { totalLines: out.total_lines, totalBytes: out.total_bytes, truncated: out.truncated, hasNonText: out.has_non_text }
  }
  const file = obj(p.file)
  if (file && str(file.path) && num(file.lines)) facts.file = { path: file.path, lines: file.lines }
  const diff = obj(p.diff)
  if (diff && str(diff.path) && num(diff.added) && num(diff.removed) && bool(diff.truncated) && Array.isArray(diff.hunks)) {
    const hunks = diff.hunks.map(readHunk)
    if (hunks.every((h): h is DiffHunk => h !== null)) {
      facts.diff = { path: diff.path, added: diff.added, removed: diff.removed, hunks, truncated: diff.truncated }
    }
  }
  return facts
}

/** Wire `status` → entry status; closed set, anything else (incl. prototype keys like 'constructor') → undefined. */
function mapResultStatus(v: unknown): ToolActivity['status'] | undefined {
  switch (v) {
    case 'ok': return 'done'
    case 'error': return 'error'
    case 'denied': return 'denied'
    default: return undefined
  }
}

/** Structural equality for ToolActivity entries (plain JSON shapes only), so an unchanged entry returns the same state. */
function sameEntry(a: unknown, b: unknown): boolean {
  if (a === b) return true
  if (Array.isArray(a) || Array.isArray(b)) {
    return Array.isArray(a) && Array.isArray(b) && a.length === b.length && a.every((x, i) => sameEntry(x, b[i]))
  }
  const oa = obj(a)
  const ob = obj(b)
  if (!oa || !ob) return false
  const ka = Object.keys(oa)
  return ka.length === Object.keys(ob).length && ka.every((k) => k in ob && sameEntry(oa[k], ob[k]))
}

function putEntry(s: ExecutionState, id: string, next: ToolActivity): ExecutionState {
  return sameEntry(s.tools[id], next) ? s : { ...s, tools: { ...s.tools, [id]: next } }
}

/**
 * N1 — derived `tool_use`. Unseen: fail-safe creation (A1 normally ran first,
 * same batch, raw before derived — F2). Seen: only the overlay fields and an
 * empty name are filled; startedAt / endedAt / status are never touched, which
 * is also what keeps a duplicate id (N7) on one entry.
 */
export function recordN2ToolUse(s: ExecutionState, p: Record<string, unknown>, at: number): ExecutionState {
  const id = p.tool_use_id
  if (!str(id)) return s
  const overlay: Pick<Overlay, 'primaryArg' | 'known'> = { ...readPrimaryArg(p), ...(bool(p.known) ? { known: p.known } : {}) }
  const t = s.tools[id]
  const next: ToolActivity = t
    ? { ...t, ...overlay, name: t.name === '' && str(p.name) ? p.name : t.name }
    : { name: str(p.name) ? p.name : '', startedAt: at, endedAt: null, status: 'running', ...overlay }
  return putEntry(s, id, next)
}

/**
 * N2 — derived `tool_result`. The mapped status overrides whatever raw A2 /
 * A3 set (`denied` is only knowable here; a result after `aborted` corrects
 * it); an unknown status string leaves the entry's status as is (closed set).
 * endedAt is kept when A2 already set it. Facts are copied per readResultFacts.
 */
export function recordN2ToolResult(s: ExecutionState, p: Record<string, unknown>, at: number): ExecutionState {
  const id = p.tool_use_id
  if (!str(id)) return s
  const mapped = mapResultStatus(p.status)
  const facts = readResultFacts(p)
  const t = s.tools[id]
  const next: ToolActivity = t
    ? { ...t, ...facts, status: mapped ?? t.status, endedAt: t.endedAt ?? at }
    : // Unseen + unknown status: the spec's "leave as is" has nothing to keep,
      // so 'done' is the conservative default — the result did arrive, and no
      // error / denial was claimed.
      { name: str(p.name) ? p.name : '', startedAt: 0, endedAt: at, status: mapped ?? 'done', ...facts }
  return putEntry(s, id, next)
}

export function endTurn(s: ExecutionState, at: number): ExecutionState {
  const tools = { ...s.tools }
  for (const [id, t] of Object.entries(tools)) {
    if (t.status === 'running') tools[id] = { ...t, endedAt: at, status: 'aborted' }
  }
  return { ...s, partial: null, turnLive: false, tools }
}
