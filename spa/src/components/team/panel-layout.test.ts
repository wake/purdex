// spa/src/components/team/panel-layout.test.ts — the header's width budget at the default 312px (TI-6). jsdom cannot lay
// anything out, so this adds up the named constants; the real-Chromium numbers are in the PR notes.
import { describe, it, expect } from 'vitest'
import { PANEL_DEFAULT_WIDTH } from '../../stores/useTeamUiStore'
import {
  AREA_BORDER, BUTTONS_W, CAPSULE_MAX_W, CELL_H, CELL_RING, HEADER_GAP, HEADER_H, HEADER_PX, CELL_W, CELL_W_MAX, cellWidthFor, cellsWidth, firstRowCapacity,
} from './panel-layout'

describe('panel header budget', () => {
  it('lead + 3 members (4 cells), the 84px capsule and both buttons fit in 312px', () => {
    const total = AREA_BORDER + 2 * HEADER_PX + CAPSULE_MAX_W + 2 * HEADER_GAP + cellsWidth(4) + BUTTONS_W
    expect(total).toBeLessThanOrEqual(PANEL_DEFAULT_WIDTH)
  })

  it('5 cells do not fit in 312px, so the 5th wraps', () => {
    const total = AREA_BORDER + 2 * HEADER_PX + CAPSULE_MAX_W + 2 * HEADER_GAP + cellsWidth(5) + BUTTONS_W
    expect(total).toBeGreaterThan(PANEL_DEFAULT_WIDTH)
    expect(firstRowCapacity(PANEL_DEFAULT_WIDTH)).toBe(4)
  })

  it('capacity grows with the width and never drops below one', () => {
    expect(firstRowCapacity(720)).toBeGreaterThan(firstRowCapacity(312))
    expect(firstRowCapacity(280)).toBeGreaterThanOrEqual(1)
    expect(firstRowCapacity(0)).toBe(1)
  })

  it('a cell (ring at the full mode 20px) sits inside the header row', () => {
    expect(CELL_RING).toBe(20)
    expect(CELL_H).toBeLessThanOrEqual(HEADER_H)
    expect(CELL_H).toBeGreaterThanOrEqual(CELL_RING)
  })

  it.each(['icon', 'dot', 'iconDot', 'badge'] as const)('a cell under the %s light style is at most 44px and 4 of them fit the 312 budget', (style) => {
    expect(CELL_W_MAX).toBe(44)
    const w = cellWidthFor(style)
    expect(w).toBeLessThanOrEqual(CELL_W_MAX)
    const total = AREA_BORDER + 2 * HEADER_PX + CAPSULE_MAX_W + 2 * HEADER_GAP + 4 * w + 3 * 2 + 5 + BUTTONS_W
    expect(total).toBeLessThanOrEqual(PANEL_DEFAULT_WIDTH)
    expect(w).toBe(CELL_W)
  })
})
