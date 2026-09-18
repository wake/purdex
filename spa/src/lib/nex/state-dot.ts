// spa/src/lib/nex/state-dot.ts — the execution state → dot colour map shared
// by the Nex table rows and the sidebar Executions view (spec §4.3).
export const STATE_DOT_CLASSES: Record<string, string> = {
  queued: 'bg-text-muted',
  running: 'bg-green-400',
  idle: 'bg-amber-400',
  rejected: 'bg-red-400',
  failed: 'bg-red-400',
  terminated: 'bg-text-muted',
}
