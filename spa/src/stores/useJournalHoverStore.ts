// spa/src/stores/useJournalHoverStore.ts — the tasks tab's hover link (J-2d, spec §11): what the pointer is on, a task or one of its
// task_new / task_done journal rows. Momentary, so never the view memo; and a store of its own rather than the card stack's state, so a
// pointer moving over a row re-renders only the rows / tasks that subscribe to their own key (a selector returning "am I linked"),
// never the stack or the whole stream.
import { create } from 'zustand'
import type { TaskHover } from '../lib/journal/tasks'

interface JournalHoverState {
  hover: TaskHover | null
  setHover: (h: TaskHover | null) => void
}

export const useJournalHoverStore = create<JournalHoverState>((set) => ({
  hover: null,
  setHover: (hover) => set({ hover }),
}))
