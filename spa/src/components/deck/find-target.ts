// spa/src/components/deck/find-target.ts — where a ⌘F came from (#2665).

/** A ⌘F that came from the right panel (or its overlay) is the panel's, not the stream's: the stream may be inert behind a modal one. */
export function fromRightPanel(target: EventTarget | null): boolean {
  return target instanceof Element && target.closest('[data-testid="session-right-panel"], [data-testid="split-overlay"]') !== null
}
