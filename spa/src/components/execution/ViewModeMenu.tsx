// spa/src/components/execution/ViewModeMenu.tsx — the worker pane's "view"
// menu (R2 plan T1.2): 指揮室 / 聊天 as radios, then 終端機 ("Take to
// terminal") as an action — it swaps the pane for a terminal instead of
// choosing a view of this worker. Controlled like `CostPanel` /
// `WorkerInfoPanel`: `ExecutionHeader` owns the open flag and the trigger's
// ref, so its anchor-lost effect can close this panel too. `ViewModeItems` is
// the bare list, reused by the header's overflow panel.
import type { RefObject } from 'react'
import { ChatsCircle, ListBullets, Terminal } from '@phosphor-icons/react'
import type { Icon } from '@phosphor-icons/react'
import { useI18nStore } from '../../stores/useI18nStore'
import { FloatingPanel } from '../FloatingPanel'
import type { ExecutionViewMode } from '../../types/tab'

const MENU_ITEM = 'flex items-center gap-2 w-full px-2 py-1 rounded text-left hover:bg-surface-hover disabled:opacity-40'

/** The views in menu order, with their icon and label key. */
const VIEW_MODES: ReadonlyArray<{ mode: ExecutionViewMode; icon: Icon; labelKey: string }> = [
  { mode: 'room', icon: ListBullets, labelKey: 'room.view.room' },
  { mode: 'chat', icon: ChatsCircle, labelKey: 'room.view.chat' },
]

/** The current view's icon + label — the menu trigger's content. */
export function ViewModeLabel({ mode }: { mode: ExecutionViewMode }) {
  const t = useI18nStore((s) => s.t)
  const { icon: ModeIcon, labelKey } = VIEW_MODES.find((v) => v.mode === mode) ?? VIEW_MODES[0]
  return <><ModeIcon size={12} /> {t(labelKey)}</>
}

export interface ViewModeItemsProps {
  mode: ExecutionViewMode
  /** Absent → the radios show the current view but are disabled. */
  onModeChange?: (mode: ExecutionViewMode) => void
  /** Present when the execution can be taken to a terminal; absent → no terminal item. */
  onTakeBack?: () => void
  takeBackBusy?: boolean
  /** Called after any item acts, so the surrounding panel can close. */
  onDone: () => void
}

export function ViewModeItems({ mode, onModeChange, onTakeBack, takeBackBusy = false, onDone }: ViewModeItemsProps) {
  const t = useI18nStore((s) => s.t)
  return (
    <>
      {VIEW_MODES.map(({ mode: m, icon: ModeIcon, labelKey }) => (
        <button key={m} type="button" role="menuitemradio" aria-checked={mode === m}
          data-testid={`view-mode-${m}`} disabled={!onModeChange}
          className={`${MENU_ITEM} ${mode === m ? 'font-medium' : ''}`}
          onClick={() => { onDone(); if (m !== mode) onModeChange?.(m) }}>
          <ModeIcon size={12} /> {t(labelKey)}
        </button>
      ))}
      {onTakeBack && (
        <>
          <div role="separator" className="my-0.5 h-px bg-border-subtle" />
          <button type="button" role="menuitem" data-testid="view-mode-terminal" disabled={takeBackBusy}
            className={MENU_ITEM} onClick={() => { onDone(); onTakeBack() }}>
            <Terminal size={12} /> {t('takeback.button')}
          </button>
        </>
      )}
    </>
  )
}

export interface ViewModeMenuProps extends Omit<ViewModeItemsProps, 'onDone'> {
  anchorRef: RefObject<HTMLElement | null>
  onClose: () => void
}

export default function ViewModeMenu({ anchorRef, onClose, ...items }: ViewModeMenuProps) {
  const t = useI18nStore((s) => s.t)
  return (
    <FloatingPanel title={t('room.view.label')} anchorRef={anchorRef} onClose={onClose} width={200} testId="view-mode-menu">
      <div role="menu" aria-label={t('room.view.label')} className="flex flex-col gap-0.5 text-xs text-text-primary">
        <ViewModeItems {...items} onDone={onClose} />
      </div>
    </FloatingPanel>
  )
}
