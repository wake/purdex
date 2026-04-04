// spa/src/stores/useUndoToast.ts — Global undo toast state
import { create } from 'zustand'

interface UndoToastState {
  toast: { message: string; restore: () => void } | null
  show: (message: string, restore: () => void) => void
  dismiss: () => void
}

export const useUndoToast = create<UndoToastState>()((set) => ({
  toast: null,
  show: (message, restore) => set({ toast: { message, restore } }),
  dismiss: () => set({ toast: null }),
}))
