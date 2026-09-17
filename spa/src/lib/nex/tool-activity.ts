// spa/src/lib/nex/tool-activity.ts — per-tool_use timing derived from the
// durable frames only (P-B2 spec §4.2 A1–A4). Pure: no React, no store.
import { contentBlocks } from './content-blocks'
import type { ExecutionState } from './event-reducer'

export interface ToolActivity {
  name: string
  /** ev.created_at (unix ms, server clock); 0 = unknown, renderer shows no timer. */
  startedAt: number
  endedAt: number | null
  status: 'running' | 'done' | 'error' | 'aborted'
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
    // done/error are final; an 'aborted' tool (A3 fired on a turn-ending
    // event before its tool_result landed) is still corrected by that result.
    if (!t || t.status === 'done' || t.status === 'error') continue
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
