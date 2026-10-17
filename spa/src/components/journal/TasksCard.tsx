// spa/src/components/journal/TasksCard.tsx — the body of the tasks card (J-2d, spec §11): a sub-title, then the unit's tasks, open first,
// done next, dropped last and struck through. Each row: number (team `7bcf08-12`, solo `#12`), title, owner, state dot + word, refs.
// The tasks are read the first time the card opens (`UnitState.tasks === null`); after that the loader keeps them fresh on
// `journal.changed what=tasks`. Hovering a row is shared with the journal rows through useJournalHoverStore (momentary, not the view memo).
import { useCallback, useEffect, useMemo, useRef } from 'react'
import { PersonMark } from './PersonMark'
import { RefChips } from './RefChips'
import type { JournalTarget } from './useJournalTarget'
import { displayNumber, sortTasks, taskKeyOfTask, type TaskHover } from '../../lib/journal/tasks'
import { useI18nStore } from '../../stores/useI18nStore'
import { useJournalHoverStore } from '../../stores/useJournalHoverStore'
import { useJournalStore, type UnitState } from '../../stores/useJournalStore'
import type { JournalTask, TaskState } from '../../lib/journal/types'

const STATE_STYLE: Record<TaskState, { dot: string; text: string }> = {
  open: { dot: 'bg-accent', text: 'text-accent' },
  done: { dot: 'bg-status-success-text', text: 'text-status-success-text' },
  dropped: { dot: 'bg-text-muted', text: 'text-text-muted' },
}

interface Props {
  target: JournalTarget
  unit: UnitState
}

export function TasksCard({ target, unit }: Props) {
  const t = useI18nStore((s) => s.t)
  const readHost = target.read?.hostId
  const readSession = target.read?.sessionId
  const solo = unit.unit.kind === 'solo'
  const unread = unit.tasks === null
  useEffect(() => {
    if (unread && readHost !== undefined && readSession !== undefined) void useJournalStore.getState().loadTasks(readHost, readSession)
  }, [unread, readHost, readSession])
  const sorted = useMemo(() => (unit.tasks === null ? [] : sortTasks(unit.tasks)), [unit.tasks])

  return (
    <div data-testid="journal-tasks-body" className="h-full flex flex-col">
      <div data-testid="journal-tasks-subtitle" className="px-3 pb-1 -mt-0.5 text-[11px] text-text-muted truncate">
        {t(solo ? 'journal.tasks.subtitle.solo' : 'journal.tasks.subtitle.team')}
      </div>
      {unread && <div data-testid="journal-tasks-loading" className="text-text-muted py-6 text-center">{t('journal.tasks.loading')}</div>}
      {!unread && sorted.length === 0 && <div data-testid="journal-tasks-empty" className="text-text-muted py-6 text-center">{t('journal.tasks.empty')}</div>}
      {sorted.length > 0 && (
        <ul className="px-3 pb-2 space-y-0.5">
          {sorted.map((task) => (
            <TaskItem key={task.number} task={task} solo={solo} personName={unit.personName} remoteUrl={unit.project.remoteUrl} />
          ))}
        </ul>
      )}
    </div>
  )
}

function TaskItem({ task, solo, personName, remoteUrl }: { task: JournalTask; solo: boolean; personName: string; remoteUrl: string }) {
  const t = useI18nStore((s) => s.t)
  const key = taskKeyOfTask(task)
  // Lit while the pointer is on this task, or on one of its journal rows (a task_new / task_done).
  const linked = useJournalHoverStore((s) => s.hover !== null && s.hover.key === key)
  // What this task said the pointer is on, so that if the task goes away under the pointer (no mouseleave ever fires) it takes it back.
  const said = useRef<TaskHover | null>(null)
  const onEnter = useCallback(() => {
    said.current = { key, from: 'task' }
    useJournalHoverStore.getState().setHover(said.current)
  }, [key])
  const onLeave = useCallback(() => {
    said.current = null
    useJournalHoverStore.getState().setHover(null)
  }, [])
  useEffect(() => () => {
    const st = useJournalHoverStore.getState()
    if (said.current !== null && st.hover === said.current) st.setHover(null)
  }, [])
  const style = STATE_STYLE[task.state]
  const struck = task.state === 'dropped'
  return (
    <li
      data-testid="journal-task"
      data-task-key={key}
      data-state={task.state}
      data-linked={linked || undefined}
      onMouseEnter={onEnter}
      onMouseLeave={onLeave}
      className={`flex gap-2 py-1.5 px-1 -mx-1 rounded border-b border-border-subtle/50 last:border-b-0 ${linked ? 'bg-accent-muted ring-1 ring-accent' : ''}`}
    >
      <span data-testid="journal-task-number" className="w-16 shrink-0 text-[10.5px] font-mono text-text-muted pt-0.5 break-all">{displayNumber(task, solo)}</span>
      <div className="min-w-0 flex-1">
        <div data-testid="journal-task-title" className={`leading-[17px] break-words ${struck ? 'line-through text-text-muted' : 'text-text-primary'}`}>{task.title}</div>
        <div className="mt-0.5 flex items-center gap-2 flex-wrap text-[10.5px]">
          {task.owner !== '' && <span data-testid="journal-task-owner" className="text-text-secondary"><PersonMark text={task.owner} name={personName} /></span>}
          <span data-testid="journal-task-state" className={`inline-flex items-center gap-1 ${style.text}`}>
            <span className={`w-1.5 h-1.5 rounded-full ${style.dot}`} />{t(`journal.tasks.state.${task.state}`)}
          </span>
        </div>
        <RefChips refs={task.refs} remoteUrl={remoteUrl} />
      </div>
    </li>
  )
}
