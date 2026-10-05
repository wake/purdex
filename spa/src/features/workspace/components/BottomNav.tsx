// spa/src/features/workspace/components/BottomNav.tsx — the activity bar's bottom button group (shell cleanup spec
// §4.5, rules B and C). One entry list, three looks: the wide bar's labelled rows (today's look, with a toggle to
// compact at the end of the first row), the wide bar's compact single row of icon buttons (the toggle back is last),
// and the narrow bar's icon column (no toggle; it is icon-only already).
import type { Ref } from 'react'
import { Plus, Lightning, HardDrives, Sliders, CaretDown, CaretUp, type Icon } from '@phosphor-icons/react'
import { useI18nStore } from '../../../stores/useI18nStore'

export interface BottomNavProps {
  variant: 'wide' | 'narrow'
  /** Wide only: one row of icon buttons instead of labelled rows. Ignored by the narrow variant. */
  compact: boolean
  workersOpen: boolean
  onAddWorkspace: () => void
  onToggleWorkers: () => void
  onOpenHosts: () => void
  onOpenSettings: () => void
  /** Wide only: the rows ↔ compact toggle. Without it the toggle is not rendered. */
  onToggleCompact?: () => void
  /** Lands on the Workers button, e.g. as a floating panel's anchor. */
  workersRef?: Ref<HTMLButtonElement>
}

interface Entry {
  key: string
  label: string
  icon: Icon
  onClick: () => void
  /** Set only on a toggle entry: drives aria-pressed and the active style. */
  pressed?: boolean
}

const ROW = 'flex items-center gap-2 px-2 py-1.5 rounded-md text-sm cursor-pointer'
const ICON = 'w-[30px] h-[30px] rounded-md flex items-center justify-center cursor-pointer'
// The active style is the one the title bar's toggles use.
const ACTIVE = 'text-accent-base bg-accent-base/10 hover:bg-accent-base/20'
const IDLE_WIDE = 'text-text-secondary hover:text-text-primary hover:bg-surface-hover'
const IDLE_NARROW = 'text-text-secondary hover:text-text-primary hover:bg-surface-secondary'

export function BottomNav({
  variant,
  compact,
  workersOpen,
  onAddWorkspace,
  onToggleWorkers,
  onOpenHosts,
  onOpenSettings,
  onToggleCompact,
  workersRef,
}: BottomNavProps) {
  const t = useI18nStore((s) => s.t)

  const entries: Entry[] = [
    { key: 'new-workspace', label: t('nav.new_workspace'), icon: Plus, onClick: onAddWorkspace },
    { key: 'workers', label: t('nav.workers'), icon: Lightning, onClick: onToggleWorkers, pressed: workersOpen },
    { key: 'hosts', label: t('nav.hosts'), icon: HardDrives, onClick: onOpenHosts },
    { key: 'settings', label: t('nav.settings'), icon: Sliders, onClick: onOpenSettings },
  ]
  const refFor = (e: Entry) => (e.key === 'workers' ? workersRef : undefined)
  const tone = (e: Entry, idle: string) => (e.pressed ? ACTIVE : idle)

  const iconButton = (e: Entry, idle: string) => {
    const EntryIcon = e.icon
    return (
      <button
        key={e.key}
        ref={refFor(e)}
        title={e.label}
        aria-pressed={e.pressed}
        onClick={e.onClick}
        className={`${ICON} ${tone(e, idle)}`}
      >
        <EntryIcon size={16} />
      </button>
    )
  }

  if (variant === 'narrow') {
    return (
      <div data-testid="bottom-nav" data-compact="false" className="flex shrink-0 flex-col items-center gap-2 pb-1">
        {entries.map((e) => iconButton(e, IDLE_NARROW))}
      </div>
    )
  }

  if (compact) {
    return (
      <div
        data-testid="bottom-nav"
        data-compact="true"
        className="flex shrink-0 flex-row items-center justify-between px-2 pb-1 pt-2"
      >
        {entries.map((e) => iconButton(e, IDLE_WIDE))}
        {onToggleCompact && (
          <button
            data-testid="bottom-nav-compact-toggle"
            title={t('nav.bottom_rows')}
            onClick={onToggleCompact}
            className={`${ICON} ${IDLE_WIDE}`}
          >
            <CaretUp size={14} />
          </button>
        )}
      </div>
    )
  }

  const row = (e: Entry, extra = '') => {
    const EntryIcon = e.icon
    return (
      <button
        key={e.key}
        ref={refFor(e)}
        title={e.label}
        aria-pressed={e.pressed}
        onClick={e.onClick}
        className={`${ROW} ${tone(e, IDLE_WIDE)}${extra}`}
      >
        <EntryIcon size={16} />
        <span className="truncate">{e.label}</span>
      </button>
    )
  }
  const [first, ...rest] = entries

  return (
    <div data-testid="bottom-nav" data-compact="false" className="flex shrink-0 flex-col gap-1 px-2 pb-1 pt-2">
      <div className="flex items-center gap-1">
        {row(first, ' min-w-0 flex-1')}
        {onToggleCompact && (
          <button
            data-testid="bottom-nav-compact-toggle"
            title={t('nav.bottom_compact')}
            onClick={onToggleCompact}
            className="shrink-0 w-6 h-6 rounded-md flex items-center justify-center text-text-muted hover:text-text-primary hover:bg-surface-hover cursor-pointer"
          >
            <CaretDown size={12} />
          </button>
        )}
      </div>
      {rest.map((e) => row(e))}
    </div>
  )
}
