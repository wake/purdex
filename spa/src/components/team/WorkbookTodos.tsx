// spa/src/components/team/WorkbookTodos.tsx — the 待辦 tab (v2, read only): the open todos (title; the detail on hover / click;
// the count in the header; the whole section is absent when there are none), then 已完成, the done record newest first.
import { useState } from 'react'
import { useI18nStore } from '../../stores/useI18nStore'
import { useWorkbookStore, selectTodoCaps, type ConvState } from '../../stores/useWorkbookStore'
import { formatWhen } from '../../lib/workbook/view-model'
import type { WorkbookTodo } from '../../lib/workbook/types'

interface Props {
  hostId: string
  convKey: string | null
  conv: ConvState | undefined
  /** A done todo was clicked: the view jumps to the entry that closed it. */
  onJump: (todo: WorkbookTodo) => void
}

function OpenTodo({ todo }: { todo: WorkbookTodo }) {
  const [shown, setShown] = useState(false)
  return (
    <li
      data-testid="workbook-todo"
      role="button"
      tabIndex={0}
      title={todo.detail || undefined}
      onClick={() => setShown((v) => !v)}
      onKeyDown={(e) => { if (e.key === 'Enter' || e.key === ' ') setShown((v) => !v) }}
      className="rounded px-1 py-0.5 cursor-pointer hover:bg-surface-hover break-words"
    >
      <div>{todo.title}</div>
      {shown && todo.detail !== '' && <div className="text-[11px] text-text-muted whitespace-pre-wrap">{todo.detail}</div>}
    </li>
  )
}

/** What closed a done todo: the thing of the entry that did (if loaded), 「重整」 for a refresh. */
function closer(todo: WorkbookTodo, conv: ConvState | undefined, refreshLabel: string): string {
  const e = conv?.entries.find((x) => x.id === todo.closedEntryId)
  if (e) return e.kind === 'refresh' ? refreshLabel : e.thing
  return todo.closedBy === 'refresh' ? refreshLabel : ''
}

export function WorkbookTodos({ hostId, convKey, conv, onJump }: Props) {
  const t = useI18nStore((s) => s.t)
  const open = conv?.todos.open ?? []
  const done = conv?.todos.done ?? []
  const caps = selectTodoCaps(conv)
  const more = !!conv && convKey !== null && !conv.todos.doneExhausted && !caps.doneCapped && done.length > 0
  return (
    <div data-testid="workbook-todos" className="flex flex-col gap-2">
      {open.length > 0 && (
        <section data-testid="workbook-open-todos">
          <div className="text-[10px] text-text-muted mb-0.5">{t('team.workbook.todos', { count: open.length })}</div>
          {caps.openCapped && <div className="text-[11px] text-text-muted mb-0.5">{t('team.workbook.open_capped')}</div>}
          <ul className="flex flex-col gap-0.5">{open.map((x) => <OpenTodo key={x.id} todo={x} />)}</ul>
        </section>
      )}
      {done.length > 0 && (
        <section data-testid="workbook-done-todos">
          <div className="text-[10px] text-text-muted mb-0.5">{t('team.workbook.done_todos')}</div>
          <ul className="flex flex-col gap-0.5">
            {done.map((x) => (
              <li
                key={x.id}
                data-testid="workbook-done-todo"
                data-todo-id={x.id}
                role="button"
                tabIndex={0}
                onClick={() => onJump(x)}
                onKeyDown={(e) => { if (e.key === 'Enter' || e.key === ' ') onJump(x) }}
                className="flex items-baseline gap-1.5 rounded px-1 py-0.5 cursor-pointer hover:bg-surface-hover"
              >
                <span data-testid="workbook-done-time" className="text-[10px] text-text-muted flex-shrink-0">{formatWhen(x.closedAt)}</span>
                <span className="truncate min-w-0 flex-1" title={x.title}>{x.title}</span>
                <span className="text-[10px] text-text-muted flex-shrink-0 max-w-[40%] truncate">{closer(x, conv, t('team.workbook.closed_by_refresh'))}</span>
              </li>
            ))}
          </ul>
          {caps.doneCapped && <div className="text-[11px] text-text-muted mt-0.5">{t('team.workbook.done_capped')}</div>}
          {more && (
            <button
              type="button"
              disabled={conv.todos.loading}
              onMouseDown={(e) => e.preventDefault()}
              onClick={() => { void useWorkbookStore.getState().loadMoreDone(hostId, convKey) }}
              className="mt-1 text-[11px] text-text-secondary hover:text-text-primary cursor-pointer disabled:opacity-50"
            >
              {t('team.workbook.more')}
            </button>
          )}
        </section>
      )}
      {open.length === 0 && done.length === 0 && <div className="text-text-secondary">{t('team.workbook.no_todos')}</div>}
    </div>
  )
}
