// spa/src/components/deck/focus-input.ts — focusing the session input when its pane comes to the front (c9d9c7-53): the deck and the chat
// both do it, at activation (a tab switch, a view switch) and again when the input shows up late (the conversation was still resolving when
// the pane was activated, so the frame took the focus and the input mounted a moment after). It does not take the focus from somebody who is
// using something else, and it leaves the choice question in the dock alone.

export type FocusInputResult =
  /** The input has the focus (caret at the end of the draft). */
  | 'focused'
  /** There is no input in the pane (yet). */
  | 'no_input'
  /** A choice question waits in the dock: the card is the thing to answer, the input is not taken. */
  | 'asking'
  /** The reader is typing in something else (a dialog, another pane, a settings field): not ours to take. */
  | 'busy'

const EDITABLE = 'input, textarea, select, [contenteditable=""], [contenteditable="true"]'

/** Whether `el` is something the reader types into. */
export function isEditable(el: Element | null): boolean {
  return !!el && (el.matches(EDITABLE) || (el as HTMLElement).isContentEditable === true)
}

/**
 * Focuses `root`'s session input unless that would take the focus from the reader. `frameOnly`: only when nothing but the pane's own
 * frame (or the page) has the focus — the late retry must not pull the focus back from wherever the reader went since.
 */
export function focusSessionInput(root: HTMLElement | null, opts: { frameOnly?: boolean; request?: boolean } = {}): FocusInputResult {
  if (!root) return 'no_input'
  // a modal dialog is up: nothing here takes the focus from it, not even the pane's frame
  if (document.querySelector('[aria-modal="true"], dialog[open]')) return 'busy'
  const input = root.querySelector<HTMLTextAreaElement>('[data-testid="session-input"] textarea')
  if (!input) return 'no_input'
  if (root.querySelector('[data-testid="dock-card"]')) return 'asking'
  const active = document.activeElement
  if (active === input) return 'focused'
  const idle = !active || active === document.body || active === root
  if (opts.frameOnly && !idle) return 'busy'
  // typing somewhere else, or a modal dialog is up: leave it. The pane's own terminal view is not "somewhere else": the focus is in it
  // exactly when the reader switches from the terminal to this view, and that switch is what this is for.
  const ownPane = root.parentElement
  // (an explicit request, a click on a notification, is the reader asking for this pane: it may take the focus from a field)
  if (!opts.request && !idle && !root.contains(active) && !(ownPane && ownPane.contains(active)) && isEditable(active)) return 'busy'
  input.focus()
  input.setSelectionRange(input.value.length, input.value.length)
  return 'focused'
}
