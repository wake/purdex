// spa/src/components/HandoffConfirmDialog.tsx — the confirm step in front of
// "Hand to nex" (P-C.3 spec §4.4). The handoff exits Claude Code in the pane
// and continues it headless with no permission prompts, so it is never a
// one-click action. The dialog owns only the busy state and the toasts; the
// request, the pane swap and the single-flight live in `lib/nex/handoff.ts`.
import { useEffect, useRef, useState } from 'react'
import { ArrowsClockwise } from '@phosphor-icons/react'
import { useI18nStore } from '../stores/useI18nStore'
import { useUndoToast } from '../stores/useUndoToast'
import { useTabStore } from '../stores/useTabStore'
import { HandoffApiError } from '../lib/nex/handoff-api'
import {
  handToNex,
  executionContentFor,
  handoffErrorMessage,
  manualResumeHint,
  type HandToNexArgs,
} from '../lib/nex/handoff'

interface Props extends HandToNexArgs {
  onClose: () => void
}

export function HandoffConfirmDialog({ onClose, ...args }: Props) {
  const t = useI18nStore((s) => s.t)
  const [busy, setBusy] = useState(false)
  // Ref, not state: two clicks in one event burst both see `busy === false`
  // before React commits the first setBusy.
  const inFlight = useRef(false)

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && !inFlight.current) onClose()
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [onClose])

  const confirm = async () => {
    if (inFlight.current) return
    inFlight.current = true
    setBusy(true)
    const toast = useUndoToast.getState()
    try {
      const { result, swapped } = await handToNex(args)
      if (swapped) {
        toast.show(t('handoff.success'))
      } else {
        const from = { sessionCode: args.sessionCode, tmuxInstance: args.tmuxInstance, cachedName: args.cachedName }
        toast.show(
          t('handoff.success'),
          () => { useTabStore.getState().openSingletonTab(executionContentFor(args.hostId, result.execution_id, from)) },
          t('handoff.open_execution'),
        )
      }
      onClose()
    } catch (err) {
      if (err instanceof HandoffApiError) {
        const id = manualResumeHint(err)
        const message = handoffErrorMessage(t, err)
        toast.show(id ? `${message}\n${t('takeback.manual_resume', { id })}` : message)
      } else {
        toast.show(t('handoff.error.generic', { code: 'unknown' }))
      }
      inFlight.current = false
      setBusy(false)
    }
  }

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/50"
      role="dialog"
      aria-modal="true"
      aria-labelledby="handoff-dialog-title"
      data-testid="handoff-dialog"
      onClick={() => { if (!busy) onClose() }}
    >
      <div
        className="w-[420px] rounded-lg border border-border-default bg-surface-primary shadow-lg"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="border-b border-border-subtle px-4 py-3">
          <h3 id="handoff-dialog-title" className="text-sm font-medium text-text-primary">{t('handoff.confirm_title')}</h3>
          <p className="mt-1 text-xs text-text-muted">{t('handoff.confirm_body')}</p>
        </div>
        <div className="flex justify-end gap-2 px-4 py-3">
          <button
            data-testid="handoff-cancel"
            onClick={onClose}
            disabled={busy}
            className="px-3 py-1 rounded-md text-xs text-text-secondary hover:bg-surface-hover cursor-pointer disabled:opacity-50 disabled:cursor-default"
          >
            {t('common.cancel')}
          </button>
          <button
            data-testid="handoff-confirm"
            onClick={() => { void confirm() }}
            disabled={busy}
            className="px-3 py-1 rounded-md text-xs bg-accent text-white cursor-pointer disabled:opacity-50 disabled:cursor-default flex items-center gap-1.5"
          >
            {busy && <ArrowsClockwise size={12} className="animate-spin" />}
            {t('handoff.menu')}
          </button>
        </div>
      </div>
    </div>
  )
}
