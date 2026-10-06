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
