// spa/src/lib/journal/tasks.ts — the tasks tab's pure rules (J-2d): the order, and the key that ties a task to its journal rows.
//
// The tie (read from the daemon, internal/module/workbook ingest_tasks.go `taskText` and journal_drain.go `writeTurn`): a task_new /
// task_done entry's text starts with `#<seq> ` (team and solo alike), and the tasks file numbers a task `<team>-<seq>` (team) or
// `<seq>` (solo, the `#` stripped by the API). So both sides reduce to the seq.
import type { EntryKind, JournalEntry, JournalTask, TaskState } from './types'

/** What the pointer is on: a task, or a task_new / task_done row (`key` is the seq both reduce to). Momentary: never a view memo. */
export interface TaskHover { key: string; from: 'row' | 'task' }

const STATE_ORDER: Record<TaskState, number> = { open: 0, done: 1, dropped: 2 }
const TASK_KINDS: ReadonlySet<EntryKind> = new Set<EntryKind>(['task_new', 'task_done'])

/** Open first, then done, dropped last; the file order is kept inside a state. */
export function sortTasks(tasks: readonly JournalTask[]): JournalTask[] {
  return tasks.map((t, i) => ({ t, i })).sort((a, b) => STATE_ORDER[a.t.state] - STATE_ORDER[b.t.state] || a.i - b.i).map((x) => x.t)
}

/** The seq a task's number names: `12`, `#12` and `7bcf08-12` are all `12`; a number that ends in no digits is kept as it is. */
export function taskKeyOfTask(task: Pick<JournalTask, 'number'>): string {
  const m = /(\d+)$/.exec(task.number.trim())
  return m ? m[1] : task.number.trim()
}

/** The seq a task_new / task_done entry's text starts with (`#17 a task`); null for any other kind, or a text that does not start so. */
export function taskKeyOfEntry(entry: Pick<JournalEntry, 'kind' | 'text'>): string | null {
  if (!TASK_KINDS.has(entry.kind)) return null
  const m = /^#(\d+)(?:\s|$)/.exec(entry.text)
  return m ? m[1] : null
}

/** How a task's number is shown: a team task as the file has it (`7bcf08-12`), a solo task as `#12`. */
export const displayNumber = (task: Pick<JournalTask, 'number'>, solo: boolean): string => (solo ? `#${task.number.replace(/^#/, '')}` : task.number)
