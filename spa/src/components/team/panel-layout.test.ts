// spa/src/components/team/panel-layout.test.ts — the header's width budget at the default 312px (TI-6). jsdom cannot lay
// anything out, so this adds up the named constants; the real-Chromium numbers are in the PR notes.
import { describe, it, expect } from 'vitest'
import { PANEL_DEFAULT_WIDTH } from '../../stores/useTeamUiStore'
import {
  AREA_BORDER, BUTTONS_W, CAPSULE_MAX_W, CELL_H, CELL_RING, HEADER_GAP, HEADER_H, HEADER_PX, CELL_W, CELL_W_MAX, CELL_PX, CELL_GAP, SEP_MARGIN, SEP_W, POPOVER_W, SUBAGENT_SLOT_W, PLUS_CHIP_W, capacityFromWidths, cellWidthFor, cellsWidth, firstRowCapacity, placeBelow,
} from './panel-layout'

describe('panel header budget', () => {
  it('spacing: 8px between cells and either side of the divider; header padding, gap and cell padding are back at their old values', () => {
    expect(CELL_GAP).toBe(8)
    expect(SEP_MARGIN).toBe(8)
    expect(SEP_W).toBe(1 + 2 * 8)
    expect([HEADER_PX, HEADER_GAP, CELL_PX]).toEqual([6, 4, 1])
    expect(CELL_W).toBe(43) // 2 x 1 padding + 5 subagent slot + 16 bot + 20 ring: nothing squeezed
  })

  // The room the cells have at the default width: the area's borders, the header's padding and gaps, the name capsule at its
  // cap and the two buttons come off 312. A cell is [subagent slot] + bot + ring (no squeezing: the content keeps its size).
  const ROOM = PANEL_DEFAULT_WIDTH - (AREA_BORDER + 2 * HEADER_PX + CAPSULE_MAX_W + 2 * HEADER_GAP + BUTTONS_W)

  it('at 312 the first row holds lead + 2 members (3 cells); the 4th wraps under the header', () => {
    expect(cellsWidth(3)).toBeLessThanOrEqual(ROOM)
    expect(cellsWidth(4)).toBeGreaterThan(ROOM)
    expect(firstRowCapacity(PANEL_DEFAULT_WIDTH)).toBe(3)
    // the per-cell sum agrees with the closed form
    expect(capacityFromWidths(Array(9).fill(CELL_W), ROOM)).toBe(3)
  })

  it('capacity grows with the width and never drops below one', () => {
    expect(firstRowCapacity(720)).toBeGreaterThan(firstRowCapacity(312))
    expect(firstRowCapacity(280)).toBeGreaterThanOrEqual(1)
    expect(firstRowCapacity(0)).toBe(1)
  })

  describe('capacityFromWidths', () => {
    it('adds the real widths in seat order, with the gaps and the divider after the lead', () => {
      const three = 3 * 38 + 2 * CELL_GAP + SEP_W // 147: three plain cells
      expect(capacityFromWidths([38, 38, 38, 50, 38], three)).toBe(3)
      expect(capacityFromWidths([38, 38, 38, 50, 38], three - 1)).toBe(2)
      expect(capacityFromWidths([38, 38, 38, 50, 38], three + CELL_GAP + 50)).toBe(4)
      expect(capacityFromWidths([38, 38, 38, 50, 38], three + CELL_GAP + 50 - 1)).toBe(3)
      expect(capacityFromWidths([38, 38, 38, 38, 38], three + CELL_GAP + 38)).toBe(4)
    })
    it('a wider cell later in the row costs only its own extra width', () => {
      const avail = 38 * 4 + 3 * CELL_GAP + SEP_W // exactly 4 plain cells
      expect(capacityFromWidths([38, 38, 38, 38, 38], avail)).toBe(4)
      expect(capacityFromWidths([38, 38, 38, 50, 38], avail)).toBe(3)
      expect(capacityFromWidths([38, 38, 38, 50, 38], avail + 12)).toBe(4)
    })
    it('never drops below one and handles all seats fitting', () => {
      expect(capacityFromWidths([60, 60], 10)).toBe(1)
      expect(capacityFromWidths([38], 500)).toBe(1)
      expect(capacityFromWidths([38, 38], 500)).toBe(2)
      expect(capacityFromWidths([], 500)).toBe(1)
    })
  })

  it('a cell (ring at the full mode 20px) sits inside the header row', () => {
    expect(CELL_RING).toBe(20)
    expect(CELL_H).toBeLessThanOrEqual(HEADER_H)
    expect(CELL_H).toBeGreaterThanOrEqual(CELL_RING)
  })

  it('a cell includes the subagent slot at its full size: nothing is squeezed to fit', () => {
    expect(CELL_W).toBe(2 * CELL_PX + SUBAGENT_SLOT_W + 16 + CELL_RING)
  })

  it('reserve keeps px free for the strip\'s +N chip only when someone does not fit; min 0 lets the strip show none', () => {
    const w = Array(5).fill(CELL_W)
    const avail = cellsWidth(5)
    expect(capacityFromWidths(w, avail, { reserve: PLUS_CHIP_W })).toBe(5) // everyone fits: no chip, nothing reserved
    expect(capacityFromWidths(w, avail - 1, { reserve: PLUS_CHIP_W })).toBeLessThan(5)
    expect(capacityFromWidths(w, 10, { reserve: PLUS_CHIP_W })).toBe(1) // the header row never goes below one
    expect(capacityFromWidths(w, 10, { reserve: PLUS_CHIP_W, min: 0 })).toBe(0) // the strip may: 「+N」 takes the rest
  })

  it('the edit form is kept inside the viewport on the left, right and bottom', () => {
    const size = { w: POPOVER_W, h: 200 }
    const view = { w: 1000, h: 700 }
    expect(placeBelow({ left: 100, bottom: 34 }, size, view)).toEqual({ left: 100, top: 38 })
    expect(placeBelow({ left: 900, bottom: 34 }, size, view)).toEqual({ left: 1000 - POPOVER_W - 8, top: 38 }) // right
    expect(placeBelow({ left: -50, bottom: 34 }, size, view)).toEqual({ left: 8, top: 38 }) // left
    expect(placeBelow({ left: 100, bottom: 650 }, size, view)).toEqual({ left: 100, top: 700 - 200 - 8 }) // bottom
    expect(placeBelow({ left: 0, bottom: 0 }, { w: POPOVER_W, h: 900 }, view).top).toBe(8) // taller than the viewport: top edge wins
  })

  it.each(['icon', 'dot', 'iconDot', 'badge'] as const)('a cell under the %s light style is the same CELL_W (at most 44px)', (style) => {
    expect(CELL_W_MAX).toBe(44)
    expect(cellWidthFor(style)).toBe(CELL_W)
    expect(CELL_W).toBeLessThanOrEqual(CELL_W_MAX)
  })

  it('a remote seat\'s cell is wider (host icon): the capacity counts its real width', () => {
    const remote = CELL_W + 12
    const plain = [CELL_W, CELL_W, CELL_W, CELL_W]
    expect(capacityFromWidths(plain, ROOM)).toBe(3)
    // 312: 43 + (8 + 17 + 43) + (8 + 43) = 162 of 165 for three plain cells; one remote cell (+12) anywhere pushes the 3rd out
    expect(capacityFromWidths([CELL_W, CELL_W, remote, CELL_W], ROOM)).toBe(2)
    expect(capacityFromWidths([CELL_W, remote, CELL_W, CELL_W], ROOM)).toBe(2)
    expect(capacityFromWidths([remote, CELL_W, CELL_W, CELL_W], ROOM)).toBe(2)
    expect(capacityFromWidths([remote, remote, remote, remote], ROOM)).toBe(2) // 55 + 80 = 135 <= 165; the 3rd: 198 > 165
  })
})
