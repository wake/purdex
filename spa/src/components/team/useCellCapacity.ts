// spa/src/components/team/useCellCapacity.ts — how many one-line cells really fit in a box (the pane's header row and the
// title-bar strip share it). The room is MEASURED: the box's width over a rendered cell's width (a status-light style draws
// a wider icon, an enlarged panel is wider than any stored width). Where nothing can be measured (no layout) it returns null
// and the caller falls back to its own default.
//
// Cells differ in width, and which cells are rendered depends on the capacity just computed. To keep that from oscillating
// (N -> N-1 -> N ...), the widest cell seen under the same premises (box width + indicator style + the seats) is remembered
// and only ever grows; the premises changing resets it.
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
}

export function useCellCapacity(box: RefObject<HTMLElement | null>, { key, total, base = 0, reserve = 0 }: Input): number | null {
  const [measured, setMeasured] = useState<number | null>(null)
  const indicatorStyle = useUISettingsStore((s) => s.tabIndicatorStyle)
  const seen = useRef({ key: '', unit: 0 })
  useLayoutEffect(() => {
    const el = box.current
    if (!el) return
    const measure = () => {
      const avail = el.clientWidth
      const premises = `${key}|${avail}|${indicatorStyle}`
      if (seen.current.key !== premises) seen.current = { key: premises, unit: 0 }
      let unit = seen.current.unit
      el.querySelectorAll<HTMLElement>('[data-testid="team-panel-cell"]').forEach((c) => { unit = Math.max(unit, c.offsetWidth) })
      seen.current.unit = unit
      if (!(unit > 0 && avail > 0)) { setMeasured(null); return }
      const room = avail - base
      const all = capacityOf(room, unit)
      setMeasured(total <= all ? all : capacityOf(room, unit, reserve))
    }
    measure()
    if (typeof ResizeObserver === 'undefined') return
    const ro = new ResizeObserver(measure)
    ro.observe(el)
    return () => ro.disconnect()
  })
  return measured
}
