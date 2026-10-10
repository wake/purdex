// spa/src/components/team/panel-layout.test.ts — the header's width budget (TI-6; #2355: 4px gaps, a minimum width that holds a lead + 3 members). jsdom cannot lay
// anything out, so this adds up the named constants; the real-Chromium numbers are in the PR notes.
import { describe, it, expect } from 'vitest'
import { HOST_BADGE_BOX_DEFAULT, HOST_BADGE_BOX_MAX } from '../../stores/useUISettingsStore'
import {
  AREA_BORDER, BUTTONS_W, CAPSULE_MAX_W, CELL_H, HEADER_GAP, HEADER_H, HEADER_PX, CELL_W, CELL_PL, CELL_PR, CELL_INNER_GAP, CELL_ICON_BOX_W, CELL_GAP, SEP_LINE_W, SEP_W, POPOVER_W, PLUS_CHIP_W, capacityFromWidths, cellWidthFor, cellsWidth, firstRowCapacity, panelMinWidth, placeBelow,
} from './panel-layout'

describe('panel header budget', () => {
  it('spacing: 4px between cells and either side of the divider; header padding, gap and cell padding are back at their old values', () => {
    expect(CELL_GAP).toBe(4)
    // The divider is an ordinary flex child of the cells row: the row's gap gives it 4 on EACH side, so it carries no margin
    // of its own. SEP_W is what it adds beyond the one gap every pair of cells has anyway: its line plus the second gap.
    expect(SEP_LINE_W).toBe(1)
    expect(SEP_W).toBe(SEP_LINE_W + CELL_GAP)
    expect(SEP_W).toBe(5)
    expect(CELL_GAP + SEP_LINE_W + CELL_GAP).toBe(9) // lead's edge -> line 4, line -> the next cell 4
    expect(cellsWidth(2)).toBe(2 * CELL_W + 4 + 1 + 4) // two cells with the divider between them: 9 apart
    expect([HEADER_PX, HEADER_GAP]).toEqual([6, 4])
  })

  it('a cell is the sidebar bead\'s box: 6px | bot run | 6px | host-box-sized ring | 3px — nothing squeezed', () => {
    expect([CELL_PL, CELL_PR, CELL_INNER_GAP, CELL_H]).toEqual([6, 3, 6, 24]) // pl-1.5, pr-[3px], gap-1.5, h-6
    expect(CELL_W).toBe(CELL_PL + CELL_ICON_BOX_W + CELL_INNER_GAP + HOST_BADGE_BOX_DEFAULT + CELL_PR)
    expect(CELL_W).toBe(48) // measured in Chromium: 6 + 17 + 6 + 16 + 3
    expect(cellWidthFor('iconDot')).toBe(62) // measured: iconDot draws its light beside the icon
    expect(cellWidthFor('badge', 24)).toBe(CELL_W + (24 - HOST_BADGE_BOX_DEFAULT)) // the ring follows the host box setting
  })

  // The room the cells have at a panel width: the area's borders, the header's padding and gaps, the name capsule at its cap
  // and the two buttons come off the width.
  const roomAt = (w: number) => w - (AREA_BORDER + 2 * HEADER_PX + CAPSULE_MAX_W + 2 * HEADER_GAP + BUTTONS_W)

  it.each([
    // [panel width, light style, cells in the first row]. A remote seat's cell is the same width (no host square any more).
    [312, 'badge', 3],
    [312, 'iconDot', 2],
    [440, 'badge', 5],
    [440, 'iconDot', 4],
  ] as const)('at %ipx (%s) the first row holds %i cells; the rest wrap under the header', (width, style, n) => {
    const w = cellWidthFor(style)
    expect(roomAt(width)).toBeGreaterThanOrEqual(cellsWidth(n, w))
    expect(roomAt(width)).toBeLessThan(cellsWidth(n + 1, w))
    expect(capacityFromWidths(Array(9).fill(w), roomAt(width))).toBe(n)
    if (style === 'badge') expect(firstRowCapacity(width)).toBe(n)
  })

  describe('the minimum width holds a lead + 3 members (4 cells) in one row (#2355, widths measured in Chromium)', () => {
    // The pieces beside the cells, measured: capsule 84 (its cap), toggle 19 + expand 20 + gap 2 = 41, header padding 6 + 6,
    // the gap between capsule | cells | buttons 4 + 4, the area's border 1 + 1  =>  147 off the panel width.
    const BESIDE = AREA_BORDER + 2 * HEADER_PX + CAPSULE_MAX_W + 2 * HEADER_GAP + BUTTONS_W
    it('beside-the-cells measures 147', () => {
      expect(BUTTONS_W).toBe(41)
      expect(BESIDE).toBe(147)
    })
    it('the minimum follows the light style and the host box: today\'s values are 356 (badge) and 412 (iconDot)', () => {
      expect(cellsWidth(4, cellWidthFor('iconDot'))).toBe(265) // 4 x 62 + 3 x 4 + 5
      expect(cellsWidth(4, cellWidthFor('badge'))).toBe(209) // 4 x 48 + 3 x 4 + 5
      expect(panelMinWidth('badge')).toBe(356)
      expect(panelMinWidth('iconDot')).toBe(412)
      expect(panelMinWidth('badge')).toBe(BESIDE + cellsWidth(4, cellWidthFor('badge', HOST_BADGE_BOX_DEFAULT)))
      expect(panelMinWidth('icon')).toBe(356)
      // a bigger ring grows every cell, so the minimum follows it (4 cells x the extra px)
      expect(panelMinWidth('badge', HOST_BADGE_BOX_MAX)).toBe(356 + 4 * (HOST_BADGE_BOX_MAX - HOST_BADGE_BOX_DEFAULT))
    })
    it.each(['badge', 'iconDot'] as const)('at the minimum, 4 %s cells fit in the first row; one px less and the 4th wraps', (style) => {
      const widths = Array(9).fill(cellWidthFor(style))
      expect(capacityFromWidths(widths, panelMinWidth(style) - BESIDE)).toBe(4)
      expect(capacityFromWidths(widths, panelMinWidth(style) - 1 - BESIDE)).toBe(3)
    })
    it('the badge fallback (no layout) agrees at the badge need', () => {
      expect(firstRowCapacity(356)).toBe(4)
      expect(firstRowCapacity(355)).toBe(3)
      expect(firstRowCapacity(356, 'badge')).toBe(4)
      expect(firstRowCapacity(355, 'badge')).toBe(3)
    })
    it('the fallback follows the light style: iconDot holds 4 at 412 and 3 at 411 (not the badge cell\'s 5)', () => {
      expect(firstRowCapacity(412, 'iconDot')).toBe(4)
      expect(firstRowCapacity(411, 'iconDot')).toBe(3)
    })
    it('the fallback follows the host box too (a bigger ring widens every cell)', () => {
      const min = panelMinWidth('badge', HOST_BADGE_BOX_MAX)
      expect(firstRowCapacity(min, 'badge', HOST_BADGE_BOX_MAX)).toBe(4)
      expect(firstRowCapacity(min - 1, 'badge', HOST_BADGE_BOX_MAX)).toBe(3)
      const minDot = panelMinWidth('iconDot', HOST_BADGE_BOX_MAX)
      expect(firstRowCapacity(minDot, 'iconDot', HOST_BADGE_BOX_MAX)).toBe(4)
      expect(firstRowCapacity(minDot - 1, 'iconDot', HOST_BADGE_BOX_MAX)).toBe(3)
    })
  })

  it('capacity grows with the width and never drops below one', () => {
    expect(firstRowCapacity(720)).toBeGreaterThan(firstRowCapacity(312))
    expect(firstRowCapacity(280)).toBeGreaterThanOrEqual(1)
    expect(firstRowCapacity(0)).toBe(1)
  })

  describe('capacityFromWidths', () => {
    it('adds the real widths in seat order, with the gaps and the divider after the lead', () => {
      const three = 3 * 38 + 2 * CELL_GAP + SEP_W // 139: three plain cells
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

  it('a cell sits inside the header row, at any host box size', () => {
    expect(CELL_H).toBeLessThanOrEqual(HEADER_H)
    expect(CELL_H).toBeGreaterThanOrEqual(HOST_BADGE_BOX_MAX) // the biggest ring still fits the cell's height
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

  it.each(['icon', 'dot', 'badge'] as const)('a cell under the %s light style is CELL_W; iconDot draws its light beside the icon, so it is wider', (style) => {
    expect(cellWidthFor(style)).toBe(CELL_W)
    expect(cellWidthFor('iconDot')).toBeGreaterThan(CELL_W)
  })
})
