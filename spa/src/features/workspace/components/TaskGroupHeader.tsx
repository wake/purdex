// spa/src/features/workspace/components/TaskGroupHeader.tsx — a group's header in the sidebar's task-group view (name pool v2 spec §7, NA-6b/6c): the colour square, the name, the host a manual group gathers, the count.
// A manual group's name is renamed in place (double-click; Enter saves, Esc or a click away drops the draft); a team's name opens the team edit form on a double-click (its own component); a group with nothing live in it
// offers 「清除」. When the host's groups cannot be edited (unsafe, not read yet) the name and 「清除」 stay, inert, and say why in their tooltip.
import { forwardRef, useEffect, useId, useRef, useState } from 'react'
import { useI18nStore } from '../../../stores/useI18nStore'

export interface HeaderEdit {
  /** Why the name cannot be edited now, in words; null = it can. */
  blocked: string | null
  /** Saves the new name; resolves when the answer is in (the caller says a refusal itself). */
  onCommit: (name: string) => Promise<void>
}

export interface HeaderClear {
  blocked: string | null
  onClick: () => void
}

interface Props {
  color: string
  name: string
  count: number
  strong?: boolean
  muted?: boolean
  host?: string
  edit?: HeaderEdit
  /** A team's header: its name opens the team edit form. */
  onDoubleClickName?: () => void
  clear?: HeaderClear
}

export const TaskGroupHeader = forwardRef<HTMLDivElement, Props>(function TaskGroupHeader({ color, name, count, strong = false, muted = false, host, edit, onDoubleClickName, clear }, ref) {
  const t = useI18nStore((s) => s.t)
  const reasonId = useId()
  const [editing, setEditing] = useState(false)
  const [draft, setDraft] = useState(name)
  const [pending, setPending] = useState(false)
  const input = useRef<HTMLInputElement>(null)
  const alive = useRef(true)
  useEffect(() => { alive.current = true; return () => { alive.current = false } }, [])
  useEffect(() => { if (editing) { input.current?.focus(); input.current?.select() } }, [editing])

  const start = () => { if (edit && edit.blocked === null) { setDraft(name); setEditing(true) } }
  const cancel = () => { if (!pending) setEditing(false) }
  const commit = async () => {
    const next = draft.trim()
    if (!edit || pending) return
    if (next === name) { setEditing(false); return }
    setPending(true)
    try { await edit.onCommit(next) } finally { if (alive.current) { setPending(false); setEditing(false) } }
  }

  const interactive = edit !== undefined || onDoubleClickName !== undefined
  const nameClass =`min-w-0 truncate ${strong ? 'font-semibold text-text-primary' : muted ? 'text-text-muted' : 'font-normal text-text-secondary'}`
  return (
    <div ref={ref} data-testid="task-group-header" className="mx-2 flex items-center gap-1.5 pl-1.5 pr-1.5 py-1 text-xs">
      <span aria-hidden="true" className="h-2.5 w-2.5 shrink-0 rounded-[3px]" style={{ backgroundColor: color }} />
      {editing ? (
        <input
          ref={input} data-testid="task-group-name-input" value={draft} disabled={pending} aria-label={name}
          onChange={(e) => setDraft(e.target.value)} onBlur={cancel}
          onKeyDown={(e) => {
            // Enter that picks a candidate in an IME (注音, 日本語) is the IME's, not a save: keyCode 229 is how browsers that do not set isComposing on the last keydown say so.
            if (e.key === 'Enter') { if (e.nativeEvent.isComposing || e.keyCode === 229) return; e.preventDefault(); void commit() }
            else if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); cancel() }
          }}
          className="min-w-0 flex-1 rounded border border-border-default bg-surface-primary px-1 py-0 text-xs text-text-primary outline-none focus:border-border-active"
        />
      ) : (
        <>
          {/* An editable name is a control: focusable, opened by Enter or F2 as well as a double-click; blocked, it stays focusable and says why (tooltip, and a hidden description). */}
          <span
            data-testid="task-group-name" title={edit?.blocked ?? undefined} aria-disabled={edit?.blocked ? true : undefined}
            role={interactive ? 'button' : undefined} tabIndex={interactive ? 0 : undefined} aria-label={interactive ? name : undefined}
            aria-describedby={edit?.blocked ? `${reasonId}-name` : undefined}
            onDoubleClick={onDoubleClickName ?? start}
            onKeyDown={interactive ? (e) => {
              if ((e.key === 'Enter' || e.key === 'F2') && !e.nativeEvent.isComposing) { e.preventDefault(); (onDoubleClickName ?? start)() }
            } : undefined}
            className={nameClass}
          >
            {name}
          </span>
          {edit?.blocked && <span id={`${reasonId}-name`} className="sr-only">{edit.blocked}</span>}
        </>
      )}
      {host !== undefined && <span data-testid="task-group-host" className="shrink-0 rounded bg-surface-hover px-1 text-[9.5px] text-text-secondary">{host}</span>}
      <span data-testid="task-group-count" className="ml-auto shrink-0 tabular-nums text-text-muted">{count}</span>
      {clear && (
        // aria-disabled, not `disabled`: it stays focusable so the reason is reachable; the reason is the tooltip and, for a screen reader, a hidden description (the name stays 「清除」).
        <button
          type="button" data-testid="task-group-clear" title={clear.blocked ?? undefined} aria-disabled={clear.blocked ? true : undefined}
          aria-label={t('taskgroups.clear')} aria-describedby={clear.blocked ? reasonId : undefined}
          onClick={() => { if (clear.blocked === null) clear.onClick() }}
          className={`shrink-0 rounded px-1 text-[10px] ${clear.blocked ? 'cursor-not-allowed text-text-muted opacity-50' : 'cursor-pointer text-text-secondary hover:bg-surface-hover hover:text-text-primary'}`}
        >
          {t('taskgroups.clear')}
          {clear.blocked && <span id={reasonId} className="sr-only">{clear.blocked}</span>}
        </button>
      )}
    </div>
  )
})
