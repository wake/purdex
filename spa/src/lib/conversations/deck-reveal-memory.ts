// spa/src/lib/conversations/deck-reveal-memory.ts — which of a deck's turns have been drawn in full (#2469). A turn far above
// the reader is first drawn with plain agent text and swapped for the real markdown when it nears the viewport; the swap is
// never undone. The deck unmounts with its tab, so which turns were swapped lives here, keyed like the fold memory
// (`${paneId}\0${sessionId}`), and a deck that comes back draws them in full at once. Memory only.
const panes = new Map<string, Set<string>>()

export function isRevealed(key: string, turnId: string): boolean {
  return panes.get(key)?.has(turnId) ?? false
}

export function markRevealed(key: string, turnId: string): void {
  let set = panes.get(key)
  if (!set) { set = new Set(); panes.set(key, set) }
  set.add(turnId)
}

export function forgetReveals(key: string): void {
  panes.delete(key)
}

/** Forget every key of a pane (`${paneId}\0…`), sparing those `keep` returns true for. Called with the fold memory's sweep. */
export function forgetRevealsOfPane(paneId: string, keep?: (key: string) => boolean): void {
  for (const key of [...panes.keys()]) if (key.startsWith(`${paneId}\0`) && !keep?.(key)) panes.delete(key)
}
