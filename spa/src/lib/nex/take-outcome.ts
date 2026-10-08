// spa/src/lib/nex/take-outcome.ts — the user-facing outcome of a take-to-terminal /
// take-back that the daemon accepted: shared by ExecutionView.runTakeBack and the
// ended-worker pane. Global stores only, so it works after the pane unmounted.
import { useUndoToast } from '../../stores/useUndoToast'
import type { TFunction } from '../pane-labels'

export function announceTakeOutcome(t: TFunction, outcome: { result: { exited?: boolean }; swapped: boolean }): void {
  const toast = useUndoToast.getState()
  // The terminal took over, but the worker is still live: say so until dismissed — and only that (#1627 A): the toast
  // would say the execution was archived, and the worker was neither terminated nor archived.
  if (outcome.result.exited === false) {
    toast.show(t('worker.exit.after_transfer_failed'), undefined, undefined, { persistent: true })
    return
  }
  // On `swapped` the pane is already a terminal; the toast is global, so it still lands.
  toast.show(outcome.swapped ? t('takeback.success') : t('takeback.archived_no_pane'))
}
