// spa/src/components/room/WorkerTaskList.tsx — the room's task list, between
// the transcript and the reply box (it replaced the collapsible worker dock;
// the stream / observer / lease facts moved to the header). Terminal style, one
// row per task or subagent — `● general-purpose  Running mutation tests…  (4m)`
// — led by a `main` row for the main agent (running / idle). A running row
// has `●`, an ended one a dimmed `○`. Always expanded; with no task there is
// nothing to list and nothing is drawn (not even `main`). It never squeezes the
// transcript: shrink-0 with a max height and its own scroll. The inspect button
// scrolls the transcript to the call that started the task (R4 T3.2); rows are
// read-only otherwise — nexen has no task-kill verb.
import { ArrowSquareOut } from '@phosphor-icons/react'
import { useI18nStore } from '../../stores/useI18nStore'
import { useElapsedTicker } from '../../hooks/useElapsedTicker'
import { formatCoarseDuration } from '../../lib/nex/format-duration'
import type { WorkerTask } from '../../lib/nex/types'

export interface WorkerTaskListProps {
  /** Every task of the execution, oldest first (`allTasks`). */
  tasks: readonly WorkerTask[]
  /** The main agent has a turn running. */
  mainRunning: boolean
  /** Scrolls the transcript to the call `toolUseId`; absent → no inspect buttons. */
  onInspect?: (toolUseId: string) => void
}

/** The elapsed is minute-coarse, so a 30 s clock is enough. */
export const TASK_LIST_TICK_MS = 30_000

const firstLine = (text: string) => text.split('\n', 1)[0]

/** Who the row is: a subagent's type, `bash` for a shell task, else the provider's task type. */
function taskName(task: WorkerTask): string {
  if (task.kind === 'subagent') return task.subagent_type || task.task_type || 'agent'
  if (task.kind === 'shell') return 'bash'
  return task.task_type || 'task'
}

/** What it does: a shell task's command, else its description. */
function taskText(task: WorkerTask): string {
  const text = task.kind === 'shell' ? task.command || task.description : task.description
  return firstLine(text || task.summary || '')
}

function elapsed(task: WorkerTask, now: number): string {
  if (task.started_at === null) return ''
  const end = task.status === 'running' ? now : task.ended_at ?? now
  return formatCoarseDuration(end - task.started_at, 'minute')
}

export default function WorkerTaskList({ tasks, mainRunning, onInspect }: WorkerTaskListProps) {
  const t = useI18nStore((s) => s.t)
  const anyRunning = tasks.some((task) => task.status === 'running')
  // Only ticks while something runs; the hook clears its interval otherwise.
  const now = useElapsedTicker(anyRunning, TASK_LIST_TICK_MS)
  if (tasks.length === 0) return null
  return (
    <ul data-testid="worker-task-list" aria-label={t('room.dock.tasks')}
      className="shrink-0 max-h-32 overflow-y-auto border-t border-border-subtle px-4 py-1 text-xs font-mono text-text-muted">
      <li data-testid="worker-task-main" data-running={mainRunning} className="flex items-center gap-2 min-w-0 py-px">
        <span aria-hidden="true" className={`shrink-0 ${mainRunning ? 'text-status-success' : ''}`}>{mainRunning ? '●' : '○'}</span>
        <span className="shrink-0 text-text-primary">main</span>
        <span className="truncate">{t(mainRunning ? 'room.tasks.main_running' : 'room.tasks.main_idle')}</span>
      </li>
      {tasks.map((task) => {
        const running = task.status === 'running'
        const when = elapsed(task, now)
        const id = task.tool_use_id
        return (
          <li key={task.task_id} data-testid="worker-task-row" data-running={running} data-kind={task.kind}
            className={`flex items-center gap-2 min-w-0 py-px ${running ? '' : 'opacity-60'}`}>
            <span data-testid="worker-task-marker" aria-hidden="true" className={`shrink-0 ${running ? 'text-status-success' : ''}`}>{running ? '●' : '○'}</span>
            <span data-testid="worker-task-name" className="shrink-0 text-text-primary">{taskName(task)}</span>
            <span data-testid="worker-task-text" className="truncate min-w-0">{taskText(task)}</span>
            {when && <span data-testid="worker-task-elapsed" className="shrink-0 tabular-nums">({when})</span>}
            {id && onInspect && (
              <button type="button" data-testid="worker-task-inspect" aria-label={t('room.dock.inspect')} title={t('room.dock.inspect')}
                onClick={() => onInspect(id)}
                className="shrink-0 rounded p-0.5 hover:bg-surface-hover hover:text-text-primary">
                <ArrowSquareOut size={12} />
              </button>
            )}
          </li>
        )
      })}
    </ul>
  )
}
