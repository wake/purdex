// spa/src/lib/nex/worker-label.ts — the one rule for a worker's display name in
// a list row: session_title.text, else the first non-empty line of the brief,
// else the execution id.
import type { ExecutionSummary } from './types'

export function workerLabel(row: Pick<ExecutionSummary, 'id' | 'brief' | 'session_title'>): string {
  const title = row.session_title?.text?.trim()
  if (title) return title
  const line = (row.brief ?? '').split(/\r?\n/).map((l) => l.trim()).find((l) => l !== '')
  return line ?? row.id
}
