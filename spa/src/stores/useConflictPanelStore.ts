// spa/src/stores/useConflictPanelStore.ts — which workspace row's sync-conflict panel is open (sidebar conflict icons
// spec §5). A store and not the row's own state because a second place opens it: the Home row's conflict popover
// ("Show" on a `tabs.<ws>` lock). One panel at a time. Never persisted: it is what is on screen right now.
//
// The row renders its panel only while it also HAS a lock; the row closes its own entry when the lock is gone, when
// a slave is on screen, and when it unmounts (`closeFor`, so a row going away never closes another row's panel).
import { create } from 'zustand'

interface ConflictPanelState {
  openWsId: string | null
  openFor: (wsId: string) => void
  toggle: (wsId: string) => void
  close: () => void
  /** Close only if `wsId`'s panel is the open one. */
  closeFor: (wsId: string) => void
}

export const useConflictPanelStore = create<ConflictPanelState>()((set, get) => ({
  openWsId: null,
  openFor: (wsId) => set({ openWsId: wsId }),
  toggle: (wsId) => set({ openWsId: get().openWsId === wsId ? null : wsId }),
  close: () => set({ openWsId: null }),
  closeFor: (wsId) => {
    if (get().openWsId === wsId) set({ openWsId: null })
  },
}))
