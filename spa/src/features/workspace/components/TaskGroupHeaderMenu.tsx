// spa/src/features/workspace/components/TaskGroupHeaderMenu.tsx — the right-click menu of a manual group's header (task groups spec §6.1): rename, colour (the team palette, and the default), 「刪除任務編組」. A plain popup in the
// style of WorkspaceContextMenu: a backdrop that closes it, Escape closes it, it stays inside the window. Keyboard: it takes focus on open (the first item), ArrowUp / ArrowDown / Home / End move among its buttons, Tab stays inside, and
// closing hands focus back to what had it. The delete asks first when the group holds tabs or members — that question is the caller's (a ConfirmDialog).
import { useEffect, useLayoutEffect, useRef } from 'react'
import { PencilSimple, Trash } from '@phosphor-icons/react'
import { TEAM_COLORS } from '../../../components/team/team-display'
import { TITLE_BAR_HEIGHT } from '../../../components/FloatingPanel'
import { useI18nStore } from '../../../stores/useI18nStore'

const PADDING = 4

interface Props {
  position: { x: number; y: number }
  /** The group's colour now (`#rrggbb`); undefined = the default. */
  color: string | undefined
  onRename: () => void
  /** `undefined` = back to the default colour. */
  onColor: (color: string | undefined) => void
  onDelete: () => void
  onClose: () => void
}

export function TaskGroupHeaderMenu({ position, color, onRename, onColor, onDelete, onClose }: Props) {
  const t = useI18nStore((s) => s.t)
  const ref = useRef<HTMLDivElement>(null)

  // keep the menu inside the window and out of the title bar's drag region, as the other context menus do
  useLayoutEffect(() => {
    const el = ref.current
    if (!el) return
    const rect = el.getBoundingClientRect()
    let { x, y } = position
    if (x + rect.width > window.innerWidth) x = window.innerWidth - rect.width - PADDING
    if (y + rect.height > window.innerHeight) y = window.innerHeight - rect.height - PADDING
    if (x < 0) x = PADDING
    if (y < TITLE_BAR_HEIGHT) y = TITLE_BAR_HEIGHT
    el.style.left = `${x}px`
    el.style.top = `${y}px`
  }, [position])

  const buttons = () => Array.from(ref.current?.querySelectorAll<HTMLButtonElement>('button') ?? [])

  // take focus on open and give it back on close
  useEffect(() => {
    const before = document.activeElement as HTMLElement | null
    buttons()[0]?.focus()
    return () => { if (before && before.isConnected) before.focus() }
  }, [])

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); onClose(); return }
      const items = buttons()
      if (items.length === 0) return
      const at = items.indexOf(document.activeElement as HTMLButtonElement)
      const move = (to: number) => { e.preventDefault(); items[(to + items.length) % items.length].focus() }
      if (e.key === 'ArrowDown') move(at + 1)
      else if (e.key === 'ArrowUp') move(at < 0 ? -1 : at - 1)
      else if (e.key === 'Home') move(0)
      else if (e.key === 'End') move(items.length - 1)
      else if (e.key === 'Tab') move(e.shiftKey ? (at < 0 ? -1 : at - 1) : at + 1) // Tab stays inside the menu
    }
    document.addEventListener('keydown', onKey, true)
    return () => document.removeEventListener('keydown', onKey, true)
  }, [onClose])

  const item = 'w-full flex items-center gap-2 px-3 py-1.5 text-xs text-text-secondary hover:text-text-primary hover:bg-surface-hover cursor-pointer transition-colors'
  const swatch = (value: string | undefined, label: string) => {
    const on = (color ?? undefined)?.toLowerCase() === value?.toLowerCase()
    return (
      <button
        key={value ?? 'default'} type="button" data-testid={value ? `task-group-menu-color-${value}` : 'task-group-menu-color-default'}
        aria-label={label} aria-pressed={on} title={label}
        onClick={() => { onColor(value); onClose() }}
        className={`h-4 w-4 shrink-0 cursor-pointer rounded-[4px] border focus:outline-none focus-visible:ring-1 focus-visible:ring-border-active ${on ? 'ring-1 ring-border-active' : ''} ${value ? 'border-transparent' : 'border-border-default bg-surface-hover'}`}
        style={value ? { backgroundColor: value } : undefined}
      />
    )
  }

  return (
    <>
      <div data-testid="task-group-menu-backdrop" className="fixed inset-0 z-40" style={{ WebkitAppRegion: 'no-drag' } as React.CSSProperties} onMouseDown={onClose} />
      <div
        ref={ref} data-testid="task-group-menu" role="menu"
        className="fixed z-50 min-w-44 rounded-lg border border-border-default bg-surface-secondary py-1 shadow-xl"
        style={{ left: position.x, top: position.y, WebkitAppRegion: 'no-drag' } as React.CSSProperties}
      >
        <button type="button" role="menuitem" data-testid="task-group-menu-rename" onClick={() => { onRename(); onClose() }} className={item}>
          <PencilSimple size={14} />
          {t('taskgroups.menu_rename')}
        </button>
        <div className="px-3 py-1.5">
          <div className="mb-1 text-[10px] text-text-muted">{t('taskgroups.menu_color')}</div>
          <div className="flex flex-wrap items-center gap-1.5">
            {swatch(undefined, t('taskgroups.menu_color_default'))}
            {TEAM_COLORS.map((c) => swatch(c, c))}
          </div>
        </div>
        <div className="my-1 border-t border-border-default" />
        <button type="button" role="menuitem" data-testid="task-group-menu-delete" onClick={() => { onDelete(); onClose() }} className={`${item} text-status-error-text hover:text-status-error-text`}>
          <Trash size={14} />
          {t('taskgroups.menu_delete')}
        </button>
      </div>
    </>
  )
}
