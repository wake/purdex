// spa/src/stores/useHandoffDialogStore.ts — which pane the "Hand to nex" confirm dialog is open on (shell cleanup spec
// §9.4). A store and not the leaf's own state because a second place opens it: the status bar's mode buttons, next to
// the pane context menu. One dialog at a time; `HandoffDialogHost` renders it. Never persisted: it is what is on
// screen right now.
import { create } from 'zustand'
import type { ExecutionViewMode, TmuxSessionContent } from '../types/tab'

export interface HandoffDialogTarget {
  tabId: string
  paneId: string
  /** The pane's live content when the dialog was opened; the dialog hands off exactly this session. */
  content: TmuxSessionContent
  /** The view the execution pane opens in after a successful handoff; absent → room. */
  mode?: ExecutionViewMode
}

interface HandoffDialogState {
  target: HandoffDialogTarget | null
  /** Open (or move) the dialog. Each call is a distinct opening, even with the same arguments. */
  open: (target: HandoffDialogTarget) => void
  close: () => void
  /**
   * Close only if `target` is still the open one. The dialog's own close goes through this: a request that finishes
   * after its dialog was closed (the pane went away mid-flight) must not close a dialog opened since.
   */
  closeFor: (target: HandoffDialogTarget) => void
}

export const useHandoffDialogStore = create<HandoffDialogState>()((set, get) => ({
  target: null,
  open: (target) => set({ target: { ...target } }),
  close: () => set({ target: null }),
  closeFor: (target) => {
    if (get().target === target) set({ target: null })
  },
}))
