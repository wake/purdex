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
 */
interface UndoToastState {
  toast: {
    message: string
    action?: () => void
    actionLabel?: string
  } | null
  show: (message: string, action?: () => void, actionLabel?: string) => void
  dismiss: () => void
}

export const useUndoToast = create<UndoToastState>()((set) => ({
  toast: null,
  show: (message, action, actionLabel) =>
    set({ toast: { message, action, actionLabel } }),
  dismiss: () => set({ toast: null }),
}))
