// spa/src/stores/usePanelDraftStore.ts — the team panel's width WHILE its edge is being dragged (JV-2b). Plain zustand, no persist:
// a mousemove must not run the persist middleware (partialize, JSON, a synchronous localStorage read). The panel and the journal
// cards beside it both read it, so they follow the edge live; the persisted width (useTeamUiStore.panel) is written once, on mouseup.
import { create } from 'zustand'

interface PanelDraftState {
  /** null = not dragging. */
  draft: number | null
  setDraft: (width: number | null) => void
}

export const usePanelDraftStore = create<PanelDraftState>()((set) => ({
  draft: null,
  setDraft: (width) => set((s) => (s.draft === width ? s : { draft: width })),
}))
