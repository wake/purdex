// spa/src/components/journal/card-layout.ts — which open cards fit (J-2b T-2b.2, spec §11). Pure: the narrow-window drop is
// VISIBILITY ONLY, recomputed from the window width on every render and never written back, so widening restores what was open.
//
// Columns, left to right: [team card + roadmap under it] [journal] [tasks]. Narrow windows drop, in order: tasks, roadmap,
// team card; the journal stays.

/** Width kept for the panes behind the cards. */
export const PANE_RESERVE = 320
export const CARD_GAP = 12
/** The stack sits `right-3` of the content box. */
export const STACK_MARGIN = 12

export interface StackInput {
  /** The person's open state per card (team: open AND the tab has a team). */
  open: { team: boolean; roadmap: boolean; journal: boolean; tasks: boolean }
  widths: { team: number; roadmap: number; journal: number; tasks: number }
  /** The window width. */
  available: number
}

export type StackVisible = StackInput['open']

/** Width of the team column: the wider of the team card and the roadmap under it. */
export const teamColumnWidth = (v: Pick<StackVisible, 'team' | 'roadmap'>, w: Pick<StackInput['widths'], 'team' | 'roadmap'>): number =>
  Math.max(v.team ? w.team : 0, v.roadmap ? w.roadmap : 0)

export function visibleCards({ open, widths, available }: StackInput): StackVisible {
  const v: StackVisible = { ...open }
  const total = (): number => {
    const cols = [teamColumnWidth(v, widths), v.journal ? widths.journal : 0, v.tasks ? widths.tasks : 0].filter((w) => w > 0)
    return cols.reduce((a, b) => a + b, 0) + Math.max(0, cols.length - 1) * CARD_GAP + STACK_MARGIN
  }
  const budget = available - PANE_RESERVE
  for (const drop of ['tasks', 'roadmap', 'team'] as const) {
    if (total() <= budget) break
    v[drop] = false
  }
  return v
}
