// spa/src/components/team/panel-layout.ts — the panel header's fixed measures (TI-6). Both modes share ONE header height, and
// the one-line cells are sized so that at the default width (312) a lead + 3 members fit in the first row with the name
// capsule at its 84px cap; a 5th seat wraps to a region under the header, so the first row never changes height.
// jsdom has no layout, so the widths are named constants (and the row-capacity arithmetic is a pure function) the tests can add up.

/** The header row of both modes (content box; no vertical padding). Full mode used to be py-2 around an 18px row = 34. */
export const HEADER_H = 34
/** The header's side padding and the gap between its parts (capsule | cells or count | buttons). */
export const HEADER_PX = 6
export const HEADER_GAP = 4
/** The name capsule's cap in one-line mode. */
export const CAPSULE_MAX_W = 84
/** Mode toggle (11px caret + px-1) and enlarge (12px icon + px-1) buttons, and the gap-0.5 between them. */
export const TOGGLE_BTN_W = 19
export const EXPAND_BTN_W = 20
export const BTN_GAP = 2
export const BUTTONS_W = TOGGLE_BTN_W + BTN_GAP + EXPAND_BTN_W

/** One one-line cell: bot + context ring. The ring is the full mode's 20px; the cell stays inside the header's 34px. */
export const CELL_ICON = 12
/** TabIcon draws the glyph in a 16px box with a 1.5px left margin; the cell cancels the margin, so the icon takes 16. */
export const CELL_ICON_SLOT = 16
export const CELL_ICON_PULL = -1.5
export const CELL_RING = 20
export const CELL_PX = 1
export const CELL_INNER_GAP = 0
export const CELL_W = CELL_PX * 2 + CELL_ICON_SLOT + CELL_INNER_GAP + CELL_RING
/** The widest a one-line cell may be under any light style (iconDot used to be 50: dot slot + icon side by side). */
export const CELL_W_MAX = 44
/**
 * A cell's width under a light style. Every style takes CELL_W: the panel draws iconDot as the corner-overlay light
 * (TeamSeatIcon `compact`), so the status dot sits on the icon instead of taking a 16px slot of its own.
 */
export function cellWidthFor(_style: 'icon' | 'dot' | 'iconDot' | 'badge'): number {
  return CELL_W
}
export const CELL_H = 26
export const CELL_GAP = 2
/** The 1px divider after the lead, with its side margin. */
export const SEP_W = 1 + 2 * 2

/** The area's own 1px border on each side. */
export const AREA_BORDER = 2

/** Cells (lead included) that fit in the header's first row at panel width `width`; at least 1. */
export function firstRowCapacity(width: number): number {
  const avail = width - AREA_BORDER - 2 * HEADER_PX - CAPSULE_MAX_W - 2 * HEADER_GAP - BUTTONS_W
  return Math.max(1, Math.floor((avail - SEP_W + CELL_GAP) / (CELL_W + CELL_GAP)))
}

/** Width the first row's cells take for `n` seats (divider after the lead when there are 2 or more). */
export function cellsWidth(n: number): number {
  return n * CELL_W + Math.max(0, n - 1) * CELL_GAP + (n >= 2 ? SEP_W : 0)
}
