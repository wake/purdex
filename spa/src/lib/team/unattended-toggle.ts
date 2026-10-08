// spa/src/lib/team/unattended-toggle.ts — one press of the title-bar 無人值守模式 button (unattended spec D-U23-5; plan
// PU-2b): from off or partial every reachable shown host is turned on, from on every one is turned off. The PUTs run in
// parallel. Unreachable and unsupported hosts are never written; a host that comes back later is not changed — the
// button reads partial until the next press.
//
// A 200 is success whatever its `list_failed` (plan ruling 30): the answer's state was validated by `putUnattended`,
// its `approved` is never read. The store is NOT written from the answer: the daemon's `team.unattended` `changed`
// event reaches every window, this one included, strictly in order (BroadcastStrict, ruling 29) — an answer applied
// here could land after a newer press made in another window and show the switch wrong. A failure (a `bad_response`
// included, though the write may have taken effect) is reported, never guessed: the next event corrects the button.
// The caller toasts the failures once (`unattended.toast.failed`).
import { ApprovalApiError } from './approval-api'
import { putUnattended } from './unattended-api'
import type { UnattendedAggregate } from './unattended-aggregate'

export interface UnattendedToggleFailure {
  hostId: string
  /** The API error code (`network`, `unsupported`, `bad_request`, …); `error` for anything else. */
  code: string
}

export interface UnattendedToggleResult {
  /** What every reachable host was asked to become. */
  target: boolean
  failed: UnattendedToggleFailure[]
}

export async function toggleUnattended(agg: UnattendedAggregate): Promise<UnattendedToggleResult> {
  const target = agg.mode !== 'on'
  const results = await Promise.allSettled(agg.reachable.map((hostId) => putUnattended(hostId, target)))
  const failed: UnattendedToggleFailure[] = []
  results.forEach((r, i) => {
    if (r.status === 'rejected') {
      failed.push({ hostId: agg.reachable[i], code: r.reason instanceof ApprovalApiError ? r.reason.code : 'error' })
    }
  })
  return { target, failed }
}
