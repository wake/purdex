// spa/src/stores/useUndoToast.ts — Global undo toast state
import { create } from 'zustand'

/**
 * Toast schema (codex round-1 B4):
 *  - action == null      → render no button (used by create / switch failure paths)
 *  - action != null      → render button; label = actionLabel ?? t('hosts.undo')
 *
 * Renamed semantically from "restore" to "action" — the field can host an undo
 * callback OR a retry callback; existing back-compat callers (delete-host undo)
 * pass a function and stay green.
 *
 * `show(…, { persistent: true })` is a NOTICE, not a toast: a failure the user must act on. It is kept apart
 * (`notice`) — never replaced by a later toast, an Undo offer included, and never timed out — and shown alongside
 * the toast until the user closes it (`dismissNotice`). A later persistent notice replaces an earlier one.
 */
interface UndoToastState {
  toast: {
    message: string
    action?: () => void
    actionLabel?: string
  } | null
  notice: { message: string } | null
  show: (message: string, action?: () => void, actionLabel?: string, opts?: { persistent?: boolean }) => void
  /** Clears the toast; a notice stays. */
  dismiss: () => void
  /** Clears the notice; the toast stays. */
  dismissNotice: () => void
}

export const useUndoToast = create<UndoToastState>()((set) => ({
  toast: null,
  notice: null,
  show: (message, action, actionLabel, opts) =>
    set(opts?.persistent ? { notice: { message } } : { toast: { message, action, actionLabel } }),
  dismiss: () => set({ toast: null }),
  dismissNotice: () => set({ notice: null }),
}))
