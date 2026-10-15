// spa/src/components/journal/CardFrame.tsx — the frame of one floating card: width with a left-edge resize handle
// (draft while dragging, committed once on mouseup — the TeamPanelArea pattern), a header, a close button.
import { useRef, useState } from 'react'
import { X } from '@phosphor-icons/react'
import { RegionResize } from '../RegionResize'
import { keepFocus } from '../../lib/keep-focus'
import { useI18nStore } from '../../stores/useI18nStore'
import { clampCard } from '../../stores/useJournalUiStore'

interface Props {
  testId: string
  width: number
  /** null = not resizable (the team card). */
  onCommitWidth: ((w: number) => void) | null
  title?: React.ReactNode
  actions?: React.ReactNode
  onClose?: () => void
  children?: React.ReactNode
}

export function CardFrame({ testId, width, onCommitWidth, title, actions, onClose, children }: Props) {
  const t = useI18nStore((s) => s.t)
  const [draft, setDraft] = useState<number | null>(null)
  const draftRef = useRef<number | null>(null)
  return (
    <div
      data-testid={testId}
      className="relative pointer-events-auto flex flex-col min-h-0 max-h-full overflow-hidden rounded-b-lg border border-t-0 border-border-default bg-surface-elevated shadow-xl font-sans text-xs text-text-primary shrink-0 transition-[width] duration-150"
      style={{ width: draft ?? width }}
    >
      {onCommitWidth && (
        <div className="absolute inset-y-0 left-0 z-10 flex" title={t('journal.card.resize')}>
          <RegionResize
            resizeEdge="left"
            onResize={(delta) => {
              const next = clampCard((draftRef.current ?? width) + delta)
              draftRef.current = next
              setDraft(next)
            }}
            onResizeEnd={() => {
              if (draftRef.current !== null) onCommitWidth(draftRef.current)
              draftRef.current = null
              setDraft(null)
            }}
          />
        </div>
      )}
      {(title || actions || onClose) && (
        <div className="flex items-center gap-1 px-3 h-8 shrink-0 border-b border-border-subtle">
          <div className="flex-1 min-w-0 truncate font-medium">{title}</div>
          {actions}
          {onClose && (
            <button
              type="button"
              data-testid={`${testId}-close`}
              aria-label={t('journal.card.close')}
              title={t('journal.card.close')}
              className="p-0.5 rounded text-text-secondary hover:text-text-primary hover:bg-surface-hover cursor-pointer"
              onMouseDown={keepFocus}
              onClick={onClose}
            >
              <X size={12} />
            </button>
          )}
        </div>
      )}
      <div className="flex-1 min-h-0 overflow-y-auto">{children}</div>
    </div>
  )
}
