// spa/src/lib/nex/worker-live.ts — what the worker's own Nexen events say is happening NOW, for the deck's live line (U4-4a): the tool that runs, and
// the background work (`task_start` / `task_end`). The transcript the deck draws comes a message at a time; these are fresher, so on a worker's deck
// they win over what the transcript's steps say. Pure: no React, no store.
import type { HeaderBackground } from '../conversations/types'
import type { ToolActivity } from './tool-activity'
import type { TaskTable } from './tasks'

/** A subagent is counted on the live line, not named (the deck's own rule): the tool that starts one is not "the tool that runs". */
const SUBAGENT_TOOLS = new Set(['Task', 'Agent'])

/** The tool that started last and has not ended, or null when none runs (the model is thinking or writing). */
export function runningToolName(tools: Record<string, ToolActivity>): string | null {
  let latest: ToolActivity | null = null
  for (const t of Object.values(tools)) {
    if (t.status !== 'running' || t.name === '' || SUBAGENT_TOOLS.has(t.name)) continue
    if (latest === null || t.startedAt >= latest.startedAt) latest = t
  }
  return latest ? latest.name : null
}

/**
 * The background work that runs, by kind — the shape the terminal deck's header carries (`shell` commands, `subagent`s, `other` work), so the
 * live line and the closing line say it in the same words. Only tasks that went to the background count (a foreground Bash is a tool, not a
 * task); undefined when nothing runs.
 */
export function workerBackground(tasks: TaskTable): HeaderBackground | undefined {
  const out = { shell: 0, subagent: 0, other: 0 }
  for (const t of Object.values(tasks)) {
    if (t.status === 'running' && t.backgrounded) out[t.kind] += 1
  }
  if (out.shell + out.subagent + out.other === 0) return undefined
  return Object.fromEntries(Object.entries(out).filter(([, n]) => n > 0)) as HeaderBackground
}
