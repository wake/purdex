// spa/src/components/journal/card-layout.ts — which open cards fit (J-2b T-2b.2, spec §11; JV-2: the team card left the stack, the
// team panel is its own docked area again). Pure: the narrow-window drop is VISIBILITY ONLY, recomputed from the window width on
// every render and never written back, so widening restores what was open.
//
// Columns, left to right: [roadmap / 待確認 detail] [journal] [tasks]. They sit left of the team panel when it is in the pane
// (`reserved` = its footprint). Narrow windows drop, in order: tasks, roadmap; the journal stays.

/** Width kept for the panes behind the cards. */
export const PANE_RESERVE = 320
export const CARD_GAP = 12
/** The stack sits `right-3` of the content box. */
export const STACK_MARGIN = 12

export interface StackInput {
  /** The person's open state per card. */
  open: { roadmap: boolean; journal: boolean; tasks: boolean }
  widths: { roadmap: number; journal: number; tasks: number }
  /** The window width. */
  available: number
  /** What the docked team panel takes from the right (its width plus a gap); 0 when it is not in the pane. */
  reserved?: number
}

export type StackVisible = StackInput['open']

export function visibleCards({ open, widths, available, reserved = 0 }: StackInput): StackVisible {
  const v: StackVisible = { ...open }
  const total = (): number => {
    const cols = [v.roadmap ? widths.roadmap : 0, v.journal ? widths.journal : 0, v.tasks ? widths.tasks : 0].filter((w) => w > 0)
    return cols.reduce((a, b) => a + b, 0) + Math.max(0, cols.length - 1) * CARD_GAP + STACK_MARGIN + reserved
  }
  const budget = available - PANE_RESERVE
  for (const drop of ['tasks', 'roadmap'] as const) {
    if (total() <= budget) break
    v[drop] = false
  }
  return v
}
