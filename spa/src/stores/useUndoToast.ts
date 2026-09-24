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
 *  - persistent          → not dismissed after a while: it stays until closed (a failure the user must act on)
 */
interface UndoToastState {
  toast: {
    message: string
    action?: () => void
    actionLabel?: string
    persistent?: boolean
  } | null
  show: (message: string, action?: () => void, actionLabel?: string, opts?: { persistent?: boolean }) => void
  dismiss: () => void
}

export const useUndoToast = create<UndoToastState>()((set) => ({
  toast: null,
  show: (message, action, actionLabel, opts) =>
    set({ toast: opts?.persistent ? { message, action, actionLabel, persistent: true } : { message, action, actionLabel } }),
  dismiss: () => set({ toast: null }),
}))
