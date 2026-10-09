// spa/src/components/team/useCellCapacity.ts — how many one-line cells really fit (the pane's header row and the title-bar
// strip share it). Every seat's cell is always rendered somewhere (the first row, the wrapped region under it, or the
// strip's hidden measuring row), so each one's REAL width is read in seat order and the capacity is the longest prefix that
// fits (capacityFromWidths). It depends only on those widths and the box width, never on how many cells are currently shown,
// so it cannot oscillate (N -> N-1 -> N), and a seat that joins past the capacity is measured like any other.
// Where nothing can be measured (no layout, or a cell not laid out) it returns null and the caller falls back to its default.
import { useLayoutEffect, useState, type RefObject } from 'react'
import { useUISettingsStore } from '../../stores/useUISettingsStore'
import { capacityFromWidths } from './panel-layout'

interface Input {
  /** More cells that belong after the ones inside the box (the panel's wrapped region), in seat order. */
  extra?: RefObject<HTMLElement | null>
  /** Px of the box taken by things beside the cells (the strip's team name); 0 for the header row, whose box is the cells'. */
  base?: number
  /** Px kept free at the end when not everyone fits (the strip's 「+N」); 0 for the header row. */
  reserve?: number
  /** The fewest cells the box may be left with: 1 for the header row (the default), 0 for the strip (「+N」 takes the rest). */
  min?: number
}

const CELL = '[data-testid="team-panel-cell"]'

export function useCellCapacity(box: RefObject<HTMLElement | null>, { extra, base = 0, reserve = 0, min = 1 }: Input = {}): number | null {
  const [measured, setMeasured] = useState<number | null>(null)
  useUISettingsStore((s) => s.tabIndicatorStyle) // a light style change widens the cells: re-render so the effect below re-measures
  useLayoutEffect(() => {
    const el = box.current
    if (!el) return
    const cells = () => [...el.querySelectorAll<HTMLElement>(CELL), ...(extra?.current?.querySelectorAll<HTMLElement>(CELL) ?? [])]
    const measure = () => {
      const avail = el.clientWidth - base
      const widths = cells().map((c) => c.offsetWidth)
      setMeasured(el.clientWidth > 0 && widths.length > 0 && widths.every((w) => w > 0) ? capacityFromWidths(widths, avail, { reserve, min }) : null)
    }
    measure()
    if (typeof ResizeObserver === 'undefined') return
    // A cell can change width by itself (e.g. its host badge's size setting), with neither a re-render nor a container
    // resize: observe every cell too. The effect re-runs each render, so a changed cell set is re-bound. measure() only
    // changes state when the capacity really differs (setState bails out on an equal value), so this cannot oscillate.
    const ro = new ResizeObserver(measure)
    ro.observe(el)
    cells().forEach((c) => ro.observe(c))
    return () => ro.disconnect()
  })
  return measured
}
