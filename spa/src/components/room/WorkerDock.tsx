// spa/src/components/room/WorkerDock.tsx — the worker's "current state" strip
// (worker pane spec §4.6), rendered inside the pane (user, Q3) between the
// transcript and the input. It carries what is *current* rather than
// chronological: the SSE state, the observer count and the lease holder —
// the facts the slimmed header (§4.7) gave up. Collapsed it is one row
// (`● live · 2 observers · lease: you`); expanded it is a small table with
// one row per fact. The open flag is local: the dock is not transcript
// content, so it is not part of the turn fold store.
// R4 T3.2: running background tasks (Bash `run_in_background`, subagents —
// nexen v0.13 task events) lead the dock: collapsed `2 running ● pnpm dev
// (4m) ● …` before the facts, expanded one row per task with a button that
// scrolls the transcript to the call that started it. No tasks (or an older
// daemon) → the dock is exactly as before. Rows are read-only: nexen has no
// task-kill verb.
import { useState } from 'react'
import { ArrowSquareOut, CaretDown, CaretRight, Circle, Robot, Terminal } from '@phosphor-icons/react'
import { useI18nStore } from '../../stores/useI18nStore'
import { useElapsedTicker } from '../../hooks/useElapsedTicker'
import { formatCoarseDuration } from '../../lib/nex/format-duration'
import type { ExecutionState } from '../../lib/nex/event-reducer'
import type { ExecutionLeaseView, TaskKind, WorkerTask } from '../../lib/nex/types'

export interface WorkerDockProps {
  sse: ExecutionState['sse']
  observers: number
  lease?: ExecutionLeaseView | null
  isMine: (principal: string | undefined) => boolean
  /** Running tasks, oldest first (`runningTasks`). */
  tasks?: readonly WorkerTask[]
  /** Scrolls the transcript to the call `toolUseId`; absent → no inspect buttons. */
  onInspect?: (toolUseId: string) => void
}

/** The dock's elapsed is minute-coarse, so a 30 s clock is enough. */
export const DOCK_TICK_MS = 30_000

const SSE_DOT: Record<ExecutionState['sse'], string> = {
  open: 'bg-status-success',
  idle: 'bg-status-warning', connecting: 'bg-status-warning', reconnecting: 'bg-status-warning',
  closed: 'bg-status-error',
  paused: 'bg-text-muted',
}

const KIND_ICON: Record<TaskKind, typeof Terminal> = { shell: Terminal, subagent: Robot, other: Circle }

/** Shell → its command; anything else → its description; else the provider's names. */
function taskLabel(task: WorkerTask): string {
  const text = task.kind === 'shell' ? task.command || task.description : task.description
  return text || task.subagent_type || task.task_type
}

const firstLine = (text: string) => text.split('\n', 1)[0]

function elapsed(task: WorkerTask, now: number): string {
  return task.started_at === null ? '' : formatCoarseDuration(now - task.started_at, 'minute')
}

const NO_TASKS: readonly WorkerTask[] = []

export default function WorkerDock({ sse, observers, lease, isMine, tasks = NO_TASKS, onInspect }: WorkerDockProps) {
  const t = useI18nStore((s) => s.t)
  const [expanded, setExpanded] = useState(false)
  // Only ticks while tasks are shown; the hook clears its interval otherwise.
  const now = useElapsedTicker(tasks.length > 0, DOCK_TICK_MS)
  const sseText = t(`execution.sse.${sse === 'idle' ? 'connecting' : sse}`)
  const mine = !!lease && isMine(lease.principal_id)
  const leaseShort = !lease ? t('execution.lease_none')
    : `${t('room.dock.lease')}: ${mine ? t('room.dock.you') : lease.principal_id}`
  const leaseFull = !lease ? t('execution.lease_none')
    : `${lease.principal_id}${mine ? ` ${t('execution.lease_you')}` : ''}`
  const Caret = expanded ? CaretDown : CaretRight

  return (
    <div data-testid="worker-dock" className="shrink-0 border-t border-border-subtle px-4 py-1 text-xs text-text-muted">
      <div className="flex items-center gap-2 min-w-0">
        <button type="button" data-testid="worker-dock-toggle" aria-expanded={expanded}
          aria-label={t(expanded ? 'room.dock.collapse' : 'room.dock.expand')}
          onClick={() => setExpanded((v) => !v)}
          className="shrink-0 rounded p-0.5 hover:bg-surface-hover hover:text-text-primary">
          <Caret size={10} />
        </button>
        {!expanded && tasks.length > 0 && (
          // The i18n store has no plural rules; `_one` / `_other` is picked here.
          <span data-testid="worker-dock-tasks" className="flex items-center gap-2 min-w-0 overflow-hidden">
            <span className="shrink-0 text-text-primary">{t(`room.dock.running_${tasks.length === 1 ? 'one' : 'other'}`, { count: tasks.length })}</span>
            {tasks.map((task) => {
              const when = elapsed(task, now)
              return (
                <span key={task.task_id} className="flex items-center gap-1 min-w-0 max-w-[16rem]">
                  <span className="shrink-0 w-1.5 h-1.5 rounded-full bg-status-success" />
                  <span data-testid="worker-dock-task" className="truncate min-w-0">{firstLine(taskLabel(task))}{when && ` (${when})`}</span>
                </span>
              )
            })}
          </span>
        )}
        <span data-testid="worker-dock-dot" className={`shrink-0 w-1.5 h-1.5 rounded-full ${SSE_DOT[sse]}`} />
        {!expanded && (
          <span data-testid="worker-dock-row" className="truncate min-w-0">
            {sseText} · {observers} {t('room.dock.observers')} · {leaseShort}
          </span>
        )}
      </div>
      {expanded && tasks.length > 0 && (
        <ul data-testid="worker-dock-task-list" aria-label={t('room.dock.tasks')} className="ml-6 my-0.5">
          {tasks.map((task) => {
            const Icon = KIND_ICON[task.kind]
            const id = task.tool_use_id
            return (
              <li key={task.task_id} data-testid="worker-dock-task-row" className="flex items-start gap-2 py-0.5 min-w-0">
                <Icon size={12} data-testid="worker-dock-task-icon" data-kind={task.kind} className="mt-0.5 shrink-0" />
                <span className="flex-1 min-w-0 text-text-primary font-mono whitespace-pre-wrap break-all">{taskLabel(task)}</span>
                <span className="shrink-0 tabular-nums">{elapsed(task, now)}</span>
                {id && onInspect && (
                  <button type="button" data-testid="worker-dock-inspect" aria-label={t('room.dock.inspect')} title={t('room.dock.inspect')}
                    onClick={() => onInspect(id)}
                    className="shrink-0 rounded p-0.5 hover:bg-surface-hover hover:text-text-primary">
                    <ArrowSquareOut size={12} />
                  </button>
                )}
              </li>
            )
          })}
        </ul>
      )}
      {expanded && (
        <table data-testid="worker-dock-table" className="ml-6 my-0.5 border-collapse">
          <tbody>
            <tr>
              <th scope="row" className="pr-3 py-0.5 text-left font-normal">{t('room.dock.sse')}</th>
              <td className="py-0.5 text-text-primary">{sseText}</td>
            </tr>
            <tr>
              <th scope="row" className="pr-3 py-0.5 text-left font-normal">{t('room.dock.observers')}</th>
              <td className="py-0.5 text-text-primary tabular-nums">{observers}</td>
            </tr>
            <tr>
              <th scope="row" className="pr-3 py-0.5 text-left font-normal">{t('room.dock.lease')}</th>
              <td className="py-0.5 text-text-primary font-mono break-all">{leaseFull}</td>
            </tr>
          </tbody>
        </table>
      )}
    </div>
  )
}
