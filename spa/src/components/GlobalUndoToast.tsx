import { useEffect, useRef } from 'react'
import { useUndoToast } from '../stores/useUndoToast'
import { useI18nStore } from '../stores/useI18nStore'

export function GlobalUndoToast() {
  const toast = useUndoToast((s) => s.toast)
  const notice = useUndoToast((s) => s.notice)
  const dismiss = useUndoToast((s) => s.dismiss)
  const dismissNotice = useUndoToast((s) => s.dismissNotice)
  const t = useI18nStore((s) => s.t)
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  useEffect(() => {
    if (!toast) return
    timerRef.current = setTimeout(() => dismiss(), 5000)
    return () => {
      if (timerRef.current) clearTimeout(timerRef.current)
    }
  }, [toast, dismiss])

  if (!toast && !notice) return null

  // The notice (a persistent failure) and the toast are kept apart and shown together: a later toast never hides it.
  return (
    <div className="fixed bottom-4 left-1/2 -translate-x-1/2 flex flex-col items-center gap-2 z-50">
      {notice && (
        <div
          role="alert"
          className="bg-zinc-800 border border-red-500/50 rounded-lg px-4 py-3 flex items-center gap-3 shadow-lg"
        >
          <span className="text-sm text-red-300 whitespace-pre-line">{notice.message}</span>
          <button className="text-sm text-zinc-400 hover:text-zinc-200 cursor-pointer" onClick={dismissNotice}>
            {t('common.close')}
          </button>
        </div>
      )}
      {toast && (
        <div
          role="status"
          aria-live="polite"
          className="bg-zinc-800 border border-zinc-700 rounded-lg px-4 py-3 flex items-center gap-3 shadow-lg"
        >
          <span className="text-sm text-zinc-300 whitespace-pre-line">{toast.message}</span>
          {toast.action && (
            <button
              className="text-sm text-blue-400 hover:text-blue-300 font-medium cursor-pointer"
              onClick={() => {
                toast.action!()
                dismiss()
              }}
            >
              {toast.actionLabel ?? t('hosts.undo')}
            </button>
          )}
        </div>
      )}
    </div>
  )
}
