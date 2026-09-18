// spa/src/lib/nex/tool-activity.ts — per-tool_use timing derived from the
// durable frames only (P-B2 spec §4.2 A1–A4). Pure: no React, no store.
import { contentBlocks } from './content-blocks'
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

export function endTurn(s: ExecutionState, at: number): ExecutionState {
  const tools = { ...s.tools }
  for (const [id, t] of Object.entries(tools)) {
    if (t.status === 'running') tools[id] = { ...t, endedAt: at, status: 'aborted' }
  }
  return { ...s, partial: null, turnLive: false, tools }
}
