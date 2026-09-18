// spa/src/components/ConfirmDialog.tsx — the small modal shell shared by the
// nex confirm steps ("Hand to nex", "Take back to terminal"): backdrop, title
// + body, Cancel / Confirm. Presentational: the caller owns whatever the
// confirm does. While `busy`, Escape, the backdrop and both buttons are inert
// and the confirm button shows a spinner. `children` render under the body
// for the caller's own controls (a checkbox, a warning line).
import { useEffect, type ReactNode } from 'react'
import { ArrowsClockwise } from '@phosphor-icons/react'
import { useI18nStore } from '../stores/useI18nStore'

export interface ConfirmDialogProps {
  /** `${testIdPrefix}-dialog` / `-cancel` / `-confirm`. */
  testIdPrefix: string
  title: string
  body: string
  confirmLabel: string
  busy?: boolean
  onCancel: () => void
  onConfirm: () => void
  children?: ReactNode
}

export function ConfirmDialog({ testIdPrefix, title, body, confirmLabel, busy = false, onCancel, onConfirm, children }: ConfirmDialogProps) {
  const t = useI18nStore((s) => s.t)

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && !busy) onCancel()
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [busy, onCancel])

  const titleId = `${testIdPrefix}-dialog-title`
  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/50"
      role="dialog"
      aria-modal="true"
      aria-labelledby={titleId}
      data-testid={`${testIdPrefix}-dialog`}
      onClick={() => { if (!busy) onCancel() }}
    >
      <div
        className="w-[420px] rounded-lg border border-border-default bg-surface-primary shadow-lg"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="border-b border-border-subtle px-4 py-3">
          <h3 id={titleId} className="text-sm font-medium text-text-primary">{title}</h3>
          <p className="mt-1 text-xs text-text-muted">{body}</p>
          {children}
        </div>
        <div className="flex justify-end gap-2 px-4 py-3">
          <button
            data-testid={`${testIdPrefix}-cancel`}
            onClick={onCancel}
            disabled={busy}
            className="px-3 py-1 rounded-md text-xs text-text-secondary hover:bg-surface-hover cursor-pointer disabled:opacity-50 disabled:cursor-default"
          >
            {t('common.cancel')}
          </button>
          <button
            data-testid={`${testIdPrefix}-confirm`}
            onClick={onConfirm}
            disabled={busy}
            className="px-3 py-1 rounded-md text-xs bg-accent text-white cursor-pointer disabled:opacity-50 disabled:cursor-default flex items-center gap-1.5"
          >
            {busy && <ArrowsClockwise size={12} className="animate-spin" />}
            {confirmLabel}
          </button>
        </div>
      </div>
    </div>
  )
}
