// spa/src/components/team/useCellCapacity.ts — how many one-line cells really fit in a box (the pane's header row and the
// title-bar strip share it). The room is MEASURED: the box's width over a rendered cell's width (a status-light style draws
// a wider icon, an enlarged panel is wider than any stored width). Where nothing can be measured (no layout) it returns null
// and the caller falls back to its own default.
//
// Cells differ in width, and which cells are rendered depends on the capacity just computed. To keep that from oscillating
// (N -> N-1 -> N ...), the widest cell seen under the same premises (box width + indicator style + the seats) is remembered
// and only ever grows; the premises changing resets it. When the SEATS or the indicator style change, the capacity measured
// for the old ones is not used for the new: the box goes back to drawing every candidate cell (measured null) for one pass,
// so a seat that used to sit past the capacity is measured too before the new capacity is decided.
import { useLayoutEffect, useRef, useState, type RefObject } from 'react'
import { useUISettingsStore } from '../../stores/useUISettingsStore'
import { capacityOf } from './panel-layout'

interface Input {
  /** Identity of what is drawn (team key + seat ids): another set of seats starts the widest-seen over. */
  key: string
  /** How many seats there are. */
  total: number
  /** Px of the box taken by things beside the cells (the strip's team name); 0 for the header row, whose box is the cells'. */
  base?: number
  /** Px kept free at the end when not everyone fits (the strip's 「+N」); 0 for the header row. */
  reserve?: number
  /** The fewest cells the box may be left with: 1 for the header row (the default), 0 for the strip (「+N」 takes the rest). */
  min?: number
}

export function useCellCapacity(box: RefObject<HTMLElement | null>, { key, total, base = 0, reserve = 0, min = 1 }: Input): number | null {
  const [measured, setMeasured] = useState<number | null>(null)
  const indicatorStyle = useUISettingsStore((s) => s.tabIndicatorStyle)
  const seen = useRef({ key: '', seats: '', unit: 0 })
  const measuredNow = useRef<number | null>(null)
  useLayoutEffect(() => {
    const el = box.current
    if (!el) return
    const measure = () => {
      const avail = el.clientWidth
      const seats = `${key}|${indicatorStyle}`
      const premises = `${seats}|${avail}`
      if (seen.current.seats !== seats && measuredNow.current !== null) {
        // Other seats / style than the capacity was measured for: draw all of them again before deciding (the effect runs
        // again after this render, and measures every cell).
        seen.current = { key: premises, seats, unit: 0 }
        measuredNow.current = null
        setMeasured(null)
        return
      }
      if (seen.current.key !== premises) seen.current = { key: premises, seats, unit: 0 }
      let unit = seen.current.unit
      el.querySelectorAll<HTMLElement>('[data-testid="team-panel-cell"]').forEach((c) => { unit = Math.max(unit, c.offsetWidth) })
      seen.current.unit = unit
      let next: number | null = null
      if (unit > 0 && avail > 0) {
        const room = avail - base
        const all = capacityOf(room, unit, 0, min)
        next = total <= all ? all : capacityOf(room, unit, reserve, min)
      }
      measuredNow.current = next
      setMeasured(next)
    }
    measure()
    if (typeof ResizeObserver === 'undefined') return
    const ro = new ResizeObserver(measure)
    ro.observe(el)
    return () => ro.disconnect()
  })
  return measured
}
