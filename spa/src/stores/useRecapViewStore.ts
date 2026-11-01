// spa/src/stores/useRecapViewStore.ts — which session's recap history the panel area shows (plan T14b). One value for the window, never persisted
// (a reload or a tab switch comes back to the normal panel). `scope` says where the view sits: a team's key for a team seat (also an ended one), the
// tab id for a personal session; the panel area shows the view only where its own scope matches.
import { create } from 'zustand'
import { useTeamUiStore } from './useTeamUiStore'

export interface RecapTarget {
  scope: string
  /** The team the view sits in; null for a personal session. */
  teamKey: string | null
  hostId: string
  sessionId: string
  /** What the panel called the session when the view was opened, and its peer address ('' when none): the view's heading names it. */
  title: string
  address: string
}

interface RecapViewState {
  open: RecapTarget | null
  show: (target: RecapTarget) => void
  close: () => void
}

export const useRecapViewStore = create<RecapViewState>()((set) => ({
  open: null,
  show: (target) => set({ open: target }),
  close: () => set((s) => (s.open === null ? s : { open: null })),
}))

export const isRecapOpen = (open: RecapTarget | null, scope: string, sessionId: string): boolean =>
  open !== null && open.scope === scope && open.sessionId === sessionId

/** Show a session's recap history, bringing its panel out of the title bar or the one-line form first (the history needs the full form). */
export function openRecapView(target: RecapTarget): void {
  const ui = useTeamUiStore.getState()
  if (target.teamKey !== null) {
    if ((ui.panelMode[target.teamKey] ?? 'full') !== 'full') ui.setPanelMode(target.teamKey, 'full')
  } else if (ui.sharedPanelMode !== 'full') {
    ui.setSharedPanelMode('full')
  }
  useRecapViewStore.getState().show(target)
}
