// spa/src/components/team/panel-layout.ts — the panel header's fixed measures (TI-6). Both modes share ONE header height, and
// the one-line cells are sized so that at the default width (312) a lead + 3 members fit in the first row with the name
// capsule at its 84px cap; a 5th seat wraps to a region under the header, so the first row never changes height.
// jsdom has no layout, so the widths are named constants (and the row-capacity arithmetic is a pure function) the tests can add up.

import { HOST_BADGE_BOX_DEFAULT } from '../../stores/useUISettingsStore'

/** The header row of both modes (content box; no vertical padding). Full mode used to be py-2 around an 18px row = 34. */
export const HEADER_H = 34
/** The header's side padding and the gap between its parts (capsule | cells or count | buttons). */
export const HEADER_PX = 6
export const HEADER_GAP = 4
/** A single click on the team name waits this long for a second one (a double-click edits instead of toggling). */
export const NAME_CLICK_DELAY_MS = 280
/** The name capsule's cap in one-line mode. */
export const CAPSULE_MAX_W = 84
/** Mode toggle (11px caret + px-1) and enlarge (12px icon + px-1) buttons, and the gap-0.5 between them. */
export const TOGGLE_BTN_W = 19
export const EXPAND_BTN_W = 20
export const BTN_GAP = 2
export const BUTTONS_W = TOGGLE_BTN_W + BTN_GAP + EXPAND_BTN_W

/**
 * One one-line cell (round 4, user 2026-10-10): the sidebar bead's box (`h-6 pl-1.5 pr-[3px]`) holding the bot icon exactly as
 * the sidebar tab row draws it, 6px (`gap-1.5`), and the usage ring, which is as big as the sidebar host box. Nothing in it is
 * squeezed to fit: the subagent dots float into the left padding (absolute, as in the sidebar) and the light into the top right,
 * so neither adds width. These constants are the NOMINAL width — used where nothing can be laid out; the real width of each
 * cell is measured (capacityFromWidths).
 */
export const CELL_H = 24
export const CELL_PL = 6
export const CELL_PR = 3
/** The gap between the bot and the ring (`gap-1.5`). */
export const CELL_INNER_GAP = 6
/** The bot icon's box in TabIcon's badge layout: 1px margin + 16px box + 0.5px margin, rounded up. */
export const CELL_ICON_BOX_W = 18
/** iconDot draws its light in a 16px slot BESIDE the 14px icon (TabIcon), so the run is wider by about the icon. */
export const CELL_ICON_BOX_W_ICONDOT = 32
/** A cell's nominal width under a light style, with a ring of `box` px (default: the host box's default). */
export function cellWidthFor(style: 'icon' | 'dot' | 'iconDot' | 'badge', box: number = HOST_BADGE_BOX_DEFAULT): number {
  return CELL_PL + (style === 'iconDot' ? CELL_ICON_BOX_W_ICONDOT : CELL_ICON_BOX_W) + CELL_INNER_GAP + box + CELL_PR
}
export const CELL_W = cellWidthFor('badge')
export const CELL_GAP = 8
/**
 * The 1px divider after the lead. It is an ordinary flex child of the cells row, so the row's CELL_GAP leaves 8px on EACH
 * side of it and it carries no margin of its own (a margin inside a cell's wrapper stacked on the gap: 16 / 8). Between the lead
 * and the next cell the row therefore spends CELL_GAP + line + CELL_GAP; SEP_W is what that adds beyond the one CELL_GAP
 * every pair of cells has anyway.
 */
export const SEP_LINE_W = 1
export const SEP_W = SEP_LINE_W + CELL_GAP

/** The area's own 1px border on each side. */
export const AREA_BORDER = 2

/** Cells (lead included) that fit in the header's first row at panel width `width`; at least 1. */
export function firstRowCapacity(width: number): number {
  const avail = width - AREA_BORDER - 2 * HEADER_PX - CAPSULE_MAX_W - 2 * HEADER_GAP - BUTTONS_W
  return Math.max(1, Math.floor((avail - SEP_W + CELL_GAP) / (CELL_W + CELL_GAP)))
}

/** The strip's 「+N」 chip (title bar): reserved at the end of the cells when some seats do not fit. */
export const PLUS_CHIP_W = 28

/**
 * How many cells fit in `avail` px, given every seat's REAL cell width in seat order (cells differ: a remote seat draws a
 * host icon). Adds width + CELL_GAP per cell, and the divider after the lead once there are 2+; the largest prefix that
 * fits, at least `min`. It depends only on the widths and `avail`, never on how many cells are currently shown.
 * The header row keeps `min` 1 (the default): its first row always holds the lead. The title-bar strip passes 0, so a seat
 * that does not fit goes into 「+N」 however narrow the bar is, and `reserve` px (the chip) when not everyone fits.
 */
export function capacityFromWidths(widths: readonly number[], avail: number, opts: { reserve?: number; min?: number } = {}): number {
  const { reserve = 0, min = 1 } = opts
  const fit = (room: number): number => {
    let used = 0
    let k = 0
    for (let i = 0; i < widths.length; i++) {
      used += widths[i] + (i > 0 ? CELL_GAP : 0) + (i === 1 ? SEP_W : 0)
      if (used > room) break
      k = i + 1
    }
    return k
  }
  const all = fit(avail)
  return Math.max(min, all >= widths.length ? all : fit(avail - reserve))
}

/** Width the first row's cells take for `n` seats (divider after the lead when there are 2 or more). */
export function cellsWidth(n: number, cellW: number = CELL_W): number {
  return n * cellW + Math.max(0, n - 1) * CELL_GAP + (n >= 2 ? SEP_W : 0)
}

/** The edit form under the header (TI-7): its width, the gap to the viewport edge and to the header. */
export const POPOVER_W = 260
const EDGE = 8
const GAP = 4

/** Where the form goes for a header at `r`: under its left edge, kept inside the viewport on the left, right and bottom. */
export function placeBelow(r: { left: number; bottom: number }, size: { w: number; h: number }, view: { w: number; h: number }): { left: number; top: number } {
  const left = Math.max(EDGE, Math.min(r.left, view.w - size.w - EDGE))
  let top = r.bottom + GAP
  if (top + size.h > view.h - EDGE) top = Math.max(EDGE, view.h - size.h - EDGE)
  return { left, top }
}
