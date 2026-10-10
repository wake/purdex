// spa/src/components/team/panel-focus.ts — focus helpers the team panel's rows and buttons share (moved out of TeamPanel.tsx, #2418).
import { useEffect, useState } from 'react'

/** Buttons keep the terminal's focus: a mousedown on them does not move it. */
export const keepFocus = (e: React.MouseEvent) => e.preventDefault()

/** A draggable row cannot preventDefault on mousedown (the browser would never start the HTML5 drag), so it lets the focus
 *  move, remembers where it was, and hands it back when the press ends: mouseup / click / dragend on the row, or a mouseup
 *  anywhere (the pointer left the row without reaching the drag threshold), the window losing focus, or the row unmounting.
 *  It only hands back while the focus is still on the row (or nowhere): a focusable the person moved to is left alone. */
export function useReturnFocus() {
  const [api] = useState(() => {
    let held: { prev: HTMLElement; row: HTMLElement } | null = null
    let listening = false
    function restore() {
      if (listening) {
        listening = false
        document.removeEventListener('mouseup', restore)
        window.removeEventListener('blur', restore)
      }
      const h = held
      held = null
      if (!h || !h.prev.isConnected) return
      const a = document.activeElement
      if (a === h.row || a === document.body || a === null) h.prev.focus()
    }
    function remember(e: React.MouseEvent) {
      const a = document.activeElement
      const row = e.currentTarget as HTMLElement
      held = a instanceof HTMLElement && a !== row ? { prev: a, row } : null
      if (held && !listening) {
        listening = true
        document.addEventListener('mouseup', restore)
        window.addEventListener('blur', restore)
      }
    }
    return { remember, restore }
  })
  useEffect(() => api.restore, [api])
  return api
}
