// spa/src/lib/keep-focus.ts — the shell chrome's "a mouse press does not take focus" handler (shell polish spec §4,
// rule F).
import type { MouseEvent } from 'react'

/**
 * `onMouseDown` for a shell chrome button (activity bar, status bar, title bar). A mouse press normally moves focus to
 * the button; the button then keeps it, so the next key — even Shift, or a screenshot hotkey — makes it match
 * `:focus-visible` and draws the outline, and Space re-fires the button instead of reaching the pane the user was
 * typing in. Preventing the mousedown default leaves focus where it was; the click still fires.
 *
 * Keyboard use is unaffected: the button stays in the tab order, and Enter/Space activate it once it has focus.
 * Propagation is not stopped, so document mousedown listeners (outside-click closers) still see the press.
 */
export function keepFocus(e: MouseEvent): void {
  e.preventDefault()
}
