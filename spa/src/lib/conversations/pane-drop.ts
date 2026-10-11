// spa/src/lib/conversations/pane-drop.ts — who takes the files dropped anywhere on a session pane (Mac feedback 10-11): the pane's input.
// The input registers its `attach` on its drop element; a pane (deck / chat) looks the element up under its own root when a drag arrives, so a
// pane without an input (loading, unreadable, no session code) takes nothing and shows nothing.

export const INPUT_DROP_SELECTOR = '[data-testid="session-input-drop"]'

type Attach = (files: readonly File[]) => void

const targets = new WeakMap<Element, Attach>()

/** The input's drop element takes files through `attach`; the returned function takes it back. */
export function registerDropTarget(el: Element, attach: Attach): () => void {
  targets.set(el, attach)
  return () => { if (targets.get(el) === attach) targets.delete(el) }
}

/** The attach of the input under `root`, or null when the pane has none. */
export function dropTargetIn(root: Element | null): Attach | null {
  const el = root?.querySelector(INPUT_DROP_SELECTOR)
  return (el && targets.get(el)) || null
}
