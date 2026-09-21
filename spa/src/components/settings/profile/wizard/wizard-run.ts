// spa/src/components/settings/profile/wizard/wizard-run.ts — the wizard's last step: what is done, in which
// order, and what a failure stops (Profile Sync spec §4.9, decisions 10 and 12; P3 plan, P3d-3). No React in
// here: the order and the choice of primitive are what can lose a user's tabs, so they are pinned against the
// real switch-active.ts and the real stores (wizard-run.test.ts), not through a rendered tree.
//
// THE ORDER IS FIXED: promote → copy → attach.
//   promote   only when a local profile was chosen. A MOVE (decision 10): the chosen world takes the master slot,
//             the world that held it becomes a local profile. Refused while a master is attached
//             (`master-attached`) — which is why the wizard's FIRST step stops the sync.
//   copy      only for a pull with "keep a copy" ticked (decision 12).
//   attach    `attachMaster(host, profile, direction)`.
// A sub-step that fails STOPS the run: nothing after it is attempted, nothing before it is undone (a promote is a
// fact). `runPlan(plan, failedAt)` is the retry — that sub-step and the ones after it, never the ones before.
//
// WHICH WORLD A PULL REPLACES, AND THEREFORE WHICH ONE THE COPY IS OF. A pull replaces THE MASTER'S world —
// wherever it is, on screen or parked — as it is when the attach is made, i.e. AFTER the promote. That is not
// "what is on screen":
//
//   on screen   chosen as master     the world the pull replaces    `saveScreenAsSlave` would keep
//   ---------   ------------------   ---------------------------    ------------------------------------------
//   master      the master           the master's = the screen      the right one
//   master      a parked profile S   S's (parked master by then)    THE OLD MASTER — which the promote has just
//                                                                   kept as a profile anyway; S would be lost
//   profile S   S                    S's = the screen               the right one
//   profile S   the (parked) master  the parked master's            S — which the pull never touches
//   profile S   a parked profile T   T's (parked master by then)    S — likewise
//
// So the copy is `copyMasterAsSlave`, AFTER the promote, in every row: "the master's world, wherever it is" is
// by construction the world the attach is about to hand to the SOT. Before the promote it would copy the OLD
// master in rows 2, 3 and 5. (`copyMasterAsSlave` refuses an unsettled world where `saveScreenAsSlave` would
// not; so does the promote, and an unsettled refusal is retryable — the run stops there and says so.)
//
// REASONS ARE A CLOSED LIST. `attachMaster` answers `{ ok: false, reason: string }`, and when its PUT THROWS the
// reason is that error's message — a transport's text, unchecked for a URL, a header or a body. Anything that is
// not one of `ATTACH_REASONS` leaves this file as `'other'`; `write-failed` leaves without its `detail`.
import { useWorkspaceStore } from '../../../../features/workspace/store'
import { effectiveDeviceName } from '../../../../lib/device-name'
import { readMasterWorld } from '../../../../lib/profile/master-world'
import { attachMaster } from '../../../../lib/profile/start'
import { copyMasterAsSlave, promoteToMaster } from '../../../../lib/profile/switch-active'
import { useDeviceNameStore } from '../../../../stores/useDeviceNameStore'
import { MASTER_PROFILE_ID, useLocalProfilesStore, type ParkedWorld } from '../../../../stores/useLocalProfilesStore'
import type { SyncDirection } from '../../../../stores/useProfileStore'
import { useTabStore } from '../../../../stores/useTabStore'
import { defaultSlaveName } from '../profile-rules'

export interface WizardPlan {
  hostId: string
  profileId: string
  /** `'master'` = the master stays the master; otherwise the local profile that is promoted. */
  localId: typeof MASTER_PROFILE_ID | string
  direction: SyncDirection
  /** Pull only: the name of the copy kept of the world the pull replaces; null = no copy. */
  saveAs: string | null
}

export type SubStepId = 'promote' | 'save' | 'attach'
export type SubStepState = 'pending' | 'running' | 'done' | 'failed'
export type RunResult = { done: true } | { done: false; failedAt: number; reason: string }

/** Every reason `attachMaster` can answer that is NOT a thrown error's message: its own refusals, `superseded`,
 *  and the api layer's `FailureReason` (start.ts, `attachHeld`: `asYouWere(put.reason)`). */
export const ATTACH_REASONS = [
  'invalid-direction',
  'client-id-not-persisted',
  'unknown-host',
  'invalid-profile-id',
  'superseded',
  'network',
  'timeout',
  'aborted',
  'contended',
  'not-found',
  'too-large',
  'rejected',
  'unauthorized',
  'server',
  'malformed',
] as const

const KNOWN_ATTACH: ReadonlySet<string> = new Set(ATTACH_REASONS)

export function subStepsOf(plan: WizardPlan): SubStepId[] {
  const steps: SubStepId[] = []
  if (plan.localId !== MASTER_PROFILE_ID) steps.push('promote')
  if (plan.direction === 'pull' && plan.saveAs !== null) steps.push('save')
  steps.push('attach')
  return steps
}

function takenNames(): string[] {
  const local = useLocalProfilesStore.getState()
  return [...Object.values(local.slaves).map((s) => s.name), ...(local.master.name === null ? [] : [local.master.name])]
}

/** The default name of a local profile made here: this device's, numbered past every name in use and past `also`. */
export function offeredProfileName(also: readonly string[] = []): string {
  return defaultSlaveName(effectiveDeviceName(useDeviceNameStore.getState()), [...takenNames(), ...also])
}

async function runSubStep(id: SubStepId, plan: WizardPlan): Promise<{ ok: true } | { ok: false; reason: string }> {
  try {
    if (id === 'promote') {
      // Names the demoted master only if nobody named it; never like the copy that is made next.
      const r = await promoteToMaster(plan.localId, offeredProfileName(plan.saveAs === null ? [] : [plan.saveAs]))
      return r.ok ? { ok: true } : { ok: false, reason: r.reason }
    }
    if (id === 'save') {
      // THE MASTER'S world, after the promote — see the table in the header. Not `saveScreenAsSlave`.
      const r = copyMasterAsSlave(plan.saveAs ?? '')
      return r.ok ? { ok: true } : { ok: false, reason: r.reason }
    }
    const r = await attachMaster(plan.hostId, plan.profileId, plan.direction)
    if (r.ok) return { ok: true }
    return { ok: false, reason: KNOWN_ATTACH.has(r.reason) ? r.reason : 'other' }
  } catch {
    return { ok: false, reason: 'other' } // none of the three throws by contract; whatever it was is not shown
  }
}

/** Runs `plan`'s sub-steps from `from` on, in order, and stops at the first that fails. */
export async function runPlan(plan: WizardPlan, from: number, report: (index: number, state: SubStepState) => void): Promise<RunResult> {
  const steps = subStepsOf(plan)
  for (let i = from; i < steps.length; i++) {
    report(i, 'running')
    const r = await runSubStep(steps[i], plan)
    if (!r.ok) {
      report(i, 'failed')
      return { done: false, failedAt: i, reason: r.reason }
    }
    report(i, 'done')
  }
  return { done: true }
}

/** The world that will be the master's once `localId` is: what a pull replaces. Null = nobody can say right now. */
export function worldToBeMaster(localId: string): ParkedWorld | null {
  if (localId === MASTER_PROFILE_ID) {
    const read = readMasterWorld()
    return read.settled ? read.world : null
  }
  const local = useLocalProfilesStore.getState()
  if (!Object.hasOwn(local.slaves, localId)) return null
  const parked = local.slaves[localId].world
  if (parked !== null) return parked
  if (local.activeProfileId !== localId) return null
  const tab = useTabStore.getState()
  const ws = useWorkspaceStore.getState()
  return { workspaces: ws.workspaces, tabs: tab.tabs, activeWorkspaceId: ws.activeWorkspaceId, activeTabId: tab.activeTabId }
}

/** Counts, not a diff (spec §4.9). */
export function countWorld(world: ParkedWorld | null): { workspaces: number; tabs: number } | null {
  return world === null ? null : { workspaces: world.workspaces.length, tabs: Object.keys(world.tabs).length }
}
