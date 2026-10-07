// spa/src/lib/nex/state-dot.ts — the one execution state → dot colour map
// (conversation entity D8): the list rows, the Nex admin table and the pane
// header all read it, and it matches the terminal agent badge (running green,
// idle grey, error red).
export const STATE_DOT_CLASSES: Record<string, string> = {
  running: 'bg-status-success',
  queued: 'bg-status-warning',
  idle: 'bg-text-muted',
  failed: 'bg-status-error',
  rejected: 'bg-status-error',
  terminated: 'bg-text-muted',
}

export const stateDotClass = (state: string): string => STATE_DOT_CLASSES[state] ?? 'bg-text-muted'

/**
 * A worker's dot (rows and the pane header): 「等待核准」 (permission channel PC2, spec §5.4) reuses the warning
 * token, which queued also uses — the surfaces put the HandPalm icon beside it so the two are never confused.
 * `awaitingApproval` comes from `isAwaitingApproval(summary)`.
 */
export const workerDotClass = (state: string, awaitingApproval: boolean): string =>
  awaitingApproval ? 'bg-status-warning' : stateDotClass(state)
