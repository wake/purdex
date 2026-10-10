// spa/src/components/team/title-bar-layout.ts — where the team strip and the centred window title fit in the title bar
// (team spec §4.4, round 5): the strip sits at the right, right before the button group, and the window title stays centred.
import { useLayoutEffect, useRef, useState } from 'react'
import { HEADER_GAP } from './panel-layout'

/** The window title keeps at least this much room, centred, while the strip is shown (it is truncated first; only then does the strip give way to 「+N」). */
export const TITLE_MIN_W = 96
/** What the left side of the bar takes: the traffic-light reserve, the sidebar toggle and the bar's padding. */
export const TITLE_LEFT_RESERVE = 112
/** The bar's right padding (`px-2`). */
export const TITLE_RIGHT_EDGE = 8

/**
 * From the bar's width, the button group's width and the strip's real content width: `room` is the strip box's width (the
 * right half of the bar less half the title's minimum, the edge, the buttons and a gap, so the title can always keep
 * TITLE_MIN_W) and `titlePad` is the title overlay's symmetric side padding, which keeps the centred title clear of both.
 */
export function titleBarLayout({ barW, btnsW, stripW }: { barW: number; btnsW: number; stripW: number }): { room: number; titlePad: number } {
  const right = TITLE_RIGHT_EDGE + btnsW + HEADER_GAP
  const room = Math.max(0, Math.min(Math.floor(barW / 2) - TITLE_MIN_W / 2 - right, barW - TITLE_LEFT_RESERVE - right))
  // Priority in a narrow bar: the title's floor (TITLE_MIN_W, centred) comes first, so the padding is capped at what leaves it
  // that width; the traffic-light reserve and the strip both yield to it (the strip's room is already 0 there, so only 「+N」 /
  // nothing shows). A bar narrower than the floor gets no padding at all.
  const maxPad = Math.max(0, (barW - TITLE_MIN_W) / 2)
  const titlePad = Math.min(maxPad, Math.max(TITLE_LEFT_RESERVE, Math.min(stripW, room) + right))
  return { room, titlePad }
}

/** Measures the bar and its button group for `titleBarLayout`; `layout` is null while nothing is shown or measurable (no layout). */
export function useTitleBarLayout(active: boolean) {
  const barRef = useRef<HTMLDivElement>(null)
  const btnsRef = useRef<HTMLDivElement>(null)
  const [dims, setDims] = useState({ barW: 0, btnsW: 0 })
  const [stripW, setStripW] = useState(0)
  useLayoutEffect(() => {
    const bar = barRef.current
    const btns = btnsRef.current
    if (!active || !bar || !btns) return
    const measure = () => {
      const next = { barW: bar.clientWidth, btnsW: btns.offsetWidth }
      setDims((p) => (p.barW === next.barW && p.btnsW === next.btnsW ? p : next))
    }
    measure()
    if (typeof ResizeObserver === 'undefined') return
    const ro = new ResizeObserver(measure)
    ro.observe(bar)
    ro.observe(btns)
    return () => ro.disconnect()
  }, [active])
  const layout = active && dims.barW > 0 ? titleBarLayout({ ...dims, stripW }) : null
  return { barRef, btnsRef, setStripW, layout }
}
