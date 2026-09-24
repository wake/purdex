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
//   attach    `attachMaster(host, profile, direction)` — three arguments, for a pull as for a push.
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
// ONE DOOR BEFORE ANYTHING IRREVERSIBLE: `prepareRun(draft)` (review F1). It answers an immutable plan or the
// reason there is none, and it checks BOTH halves of what the user agreed to:
//   this device   no master attached · the host in the store and connected · the chosen local profile still there
//                 — before the host is asked, and AGAIN after it has answered (the ask takes up to 15 s).
//   the SOT       the profile is RE-LISTED and its fingerprint (`sotFingerprint`: every live, non-retired section's name, rev
//                 and hash — the index has them; no payload is fetched; a deleted section is simply not listed)
//                 must be the one the user was looking at when they chose it. The attack this closes: a profile
//                 seen EMPTY (or just created) is offered push only, without the "replaces what is there"
//                 warning; another device pushes a whole world into it; Start would overwrite that in silence.
//                 Changed → no run: the wizard goes back to the direction step with what is there NOW, says so,
//                 and the user chooses again (both directions, and the warning). Gone → back to the profile
//                 step. The list cannot be read → no run either; it is said, and Start may be pressed again.
//                 A profile this visit created is no exception: it is empty only while the host says so.
// A PULL TOUCHES NO HOST (host ownership H3b, spec §5.1). The host list is this device's: nothing here reads the SOT's
// `hosts` section, a pull removes no host, and the attach host must exist on this device (`brokenLocalPremise`:
// `host-gone`, before the host is asked and after). The pull's one host premise left is the attach host VERIFIED
// (`brokenPullPremise`, D3): the SOT's tabs and settings name hosts by daemon id, resolved through its claim.
// WHAT IS LEFT. For a push `attachMaster` takes no rev, and there is none to give it: under `push` the executor answers
// every conflict of the first reconciliation with keep-local — each PUT is a CAS on the rev ITS OWN index read
// gave, rebased and sent again on a 409 (executor.ts, THE FIRST RECONCILIATION). So a push is an unconditional
// overwrite for as long as that reconciliation lasts, and nothing this file knows can be handed down to narrow
// it. The window that remains is from the LAST re-list — the one right before the attach (`recheckBeforeAttach`) — to the end of the first reconciliation: a device that writes
// into the profile in those seconds is overwritten, as the user was told a push does.
//
// A CREATE WHOSE OUTCOME IS NOT KNOWN IS NEVER SIMPLY SENT AGAIN (`createSotProfile`, review F2). The daemon
// gives every POST a new id and allows equal names, so a POST that succeeded with its answer lost, pressed
// again, leaves an orphan per press. Unknown = timeout · network · aborted · thrown — and `server` / `malformed`
// too (a 5xx or an unreadable 2xx may follow the commit; the cost of counting them in is one list request).
// Then the host is listed and "ours" is looked for: same name, no section, no device attached, and an id that
// was NOT in the BASELINE — the ids of the last list this wizard visit had read from that host BEFORE its first
// POST (the component keeps it; ids, not `createdAt`: the daemon's clock is not this device's). Found → it MAY
// be ours, and that is all anybody can say: another device starting from the same list may have created a
// profile of that name in the same moment, and nothing in the protocol ties a POST to its profile. Taking it
// would attach two devices to one profile without either having chosen that. So it is POINTED AT (`maybe`), the
// user takes it from the list like any existing profile — empty, so push only, and `prepareRun` catches it being
// filled meanwhile — or gives the new one another name. Nothing is ever adopted. Not found → it was not
// created; the next press may POST. The list cannot be read → still unknown:
// the next press LOOKS FIRST (`lookFirst`) and does not POST until a list has been read. No baseline ("new" was
// chosen while the list had never been read): ours cannot be told from one that was always there, so a
// same-name empty profile is neither pointed at nor doubled — the user is shown the list and picks, or renames.
//
// THE RESULT IS SAID OUTSIDE THE WIZARD, TOO (`announceRun`, acceptance F6). The Settings tab belongs to the
// master's world, and a pull REPLACES that world: the page, the wizard and its "done" line are unmounted the
// moment the first section is applied. So a finished run is a toast (`useUndoToast` — it belongs to no world;
// the profile switcher's precedent), a push's as well for consistency; a FAILED run is a toast only when the
// wizard is no longer there to say it. The run's promise outlives the component and sets no state on it.
//
// REASONS ARE A CLOSED LIST. `attachMaster` answers `{ ok: false, reason: string }`, and when its PUT THROWS the
// reason is that error's message — a transport's text, unchecked for a URL, a header or a body. Anything that is
// not one of `ATTACH_REASONS` leaves this file as `'other'`; `write-failed` leaves without its `detail`.
import { useWorkspaceStore } from '../../../../features/workspace/store'
import { effectiveDeviceName } from '../../../../lib/device-name'
import { createProfile, listProfiles, type ProfileIndexEntry } from '../../../../lib/profile/api'
import { readMasterWorld } from '../../../../lib/profile/master-world'
import { attachMaster } from '../../../../lib/profile/start'
import { copyMasterAsSlave, promoteToMaster } from '../../../../lib/profile/switch-active'
import { useDeviceNameStore } from '../../../../stores/useDeviceNameStore'
import { selectDaemonIdMismatch, selectDaemonIdVerified, useHostStore } from '../../../../stores/useHostStore'
import { useI18nStore } from '../../../../stores/useI18nStore'
import { MASTER_PROFILE_ID, useLocalProfilesStore, type ParkedWorld } from '../../../../stores/useLocalProfilesStore'
import { endpointOfHost, selectMaster, useProfileStore, type SyncDirection } from '../../../../stores/useProfileStore'
import { useTabStore } from '../../../../stores/useTabStore'
import { useUndoToast } from '../../../../stores/useUndoToast'
import { defaultSlaveName } from '../profile-rules'

export interface WizardPlan {
  hostId: string
  profileId: string
  /** `'master'` = the master stays the master; otherwise the local profile that is promoted. */
  localId: typeof MASTER_PROFILE_ID | string
  direction: SyncDirection
  /** Pull only: the name of the copy kept of the world the pull replaces; null = no copy. */
  saveAs: string | null
  /** The host's address when the plan was made: every request of the run goes there, and the attach is not made
   *  if the host has been re-pointed since. */
  at: string
  /** The SOT profile's `sotFingerprint` the plan was made against — what the user saw. */
  seen: string
}

export type SubStepId = 'promote' | 'save' | 'attach'
export type SubStepState = 'pending' | 'running' | 'done' | 'failed'
export type RunResult =
  | { done: true }
  /** `recheck`: the attach was not made because the ask before it (`recheckBeforeAttach`) found the premises moved —
   *  `reason` is that refusal's reason, and the wizard handles it as it handles `prepareRun`'s. */
  | { done: false; failedAt: number; reason: string; recheck?: PrepareRefusal }

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

async function runSubStep(id: SubStepId, plan: WizardPlan): Promise<{ ok: true } | { ok: false; reason: string; recheck?: PrepareRefusal }> {
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
    // The promote and the copy took time: ask again what the plan was made against (review, attacker H1).
    const again = await recheckBeforeAttach(plan)
    if (!again.ok) return { ok: false, reason: again.reason, recheck: again }
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
      return { done: false, failedAt: i, reason: r.reason, ...(r.recheck === undefined ? {} : { recheck: r.recheck }) }
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

// === The one door before the run ===

/** What the wizard has collected. `seen`: the chosen profile's `sotFingerprint` AS THE USER SAW IT when choosing. */
export interface WizardDraft {
  hostId: string | null
  profileId: string | null
  seen: string | null
  localId: string
  direction: SyncDirection | null
  saveAs: string | null
}

export type PremiseReason = 'attached-elsewhere' | 'host-gone' | 'host-offline' | 'local-gone'

/** What a profile holds right now, for the direction step to offer the right choices. */
export interface SotNow {
  fingerprint: string
  empty: boolean
}

/**
 * Why a PULL cannot be made from this host (host-sync-identity spec §8; kept by host ownership D3). A pull resolves
 * the SOT's host references — the `d1_…` ids its tabs and settings name — through this device's hosts, the attach
 * host's CLAIMED daemon id among them; an unverified or mismatched claim can map them onto the wrong host (and the
 * start layer would block the sync anyway: `host-identity-mismatch`). So this device must have confirmed, this
 * session and at the host's current address, that the daemon there is the one the host's record claims.
 *   `master-mismatch`   the daemon at the host's address is another one than its record says.
 *   `master-unverified` nothing is confirmed (no claim yet, or not reached since the claim or the address changed).
 */
export type PullPremiseReason = 'master-unverified' | 'master-mismatch'

export type PrepareResult = { ok: true; plan: Readonly<WizardPlan> } | PrepareRefusal

export type PrepareRefusal =
  | { ok: false; reason: PremiseReason | PullPremiseReason | 'incomplete' | 'profile-gone' }
  | { ok: false; reason: 'profile-changed'; now: SotNow }
  /** `request`: the failure's class (wizard-shared.ts, `requestKey`) — never its message. */
  | { ok: false; reason: 'list-failed'; request: string }

/** Sections the sync no longer carries (host ownership H3b, spec §5.1): a legacy `hosts` row stays on the SOT and is
 *  no content any more — a pull brings nothing of it, a push does not replace it. H3a-2 adds the same set to
 *  projections.ts (`isRetiredSection`); whichever of the two lands second makes this read that one. */
const RETIRED_SECTIONS: ReadonlySet<string> = new Set(['hosts'])

/** The sections a direction decides about: every live one but the retired. */
function liveContent(entry: Pick<ProfileIndexEntry, 'sections'>): ProfileIndexEntry['sections'] {
  return entry.sections.filter((m) => !RETIRED_SECTIONS.has(m.section))
}

/** Every live section's name, rev and hash, in a fixed order — the retired ones left out (a straggler's `hosts`
 *  write must not bounce the wizard). The index lists no tombstone: a deleted section is one that is missing. The
 *  profile's NAME is not part of it — a rename changes nothing a direction decides. */
export function sotFingerprint(entry: Pick<ProfileIndexEntry, 'sections'>): string {
  return JSON.stringify(liveContent(entry).map((m) => [m.section, m.rev, m.hash]).sort((a, b) => (a[0] < b[0] ? -1 : a[0] > b[0] ? 1 : 0)))
}

/** `empty`: nothing but retired sections — a pull would bring nothing, so pull is not offered and push warns of nothing. */
export function sotNow(entry: Pick<ProfileIndexEntry, 'sections'>): SotNow {
  return { fingerprint: sotFingerprint(entry), empty: liveContent(entry).length === 0 }
}

/** This device's half, read off the stores as they are this instant. Null = every premise holds. */
export function brokenLocalPremise(hostId: string | null, localId: string, check: { host: boolean; local: boolean } = { host: true, local: true }): PremiseReason | null {
  if (selectMaster(useProfileStore.getState()) !== null) return 'attached-elsewhere'
  if (check.host) {
    const hosts = useHostStore.getState()
    if (hostId === null || hosts.hosts[hostId] === undefined) return 'host-gone'
    if (hosts.runtime[hostId]?.status !== 'connected') return 'host-offline'
  }
  if (check.local && localId !== MASTER_PROFILE_ID && !Object.hasOwn(useLocalProfilesStore.getState().slaves, localId)) return 'local-gone'
  return null
}

/** The host's profiles, or the class of the failure. */
async function listOrClass(hostId: string, expectEndpoint?: string): Promise<{ ok: true; rows: ProfileIndexEntry[] } | { ok: false; request: string }> {
  try {
    const r = await listProfiles(hostId, expectEndpoint === undefined ? undefined : { expectEndpoint })
    return r.kind === 'ok' ? { ok: true, rows: r.value } : { ok: false, request: r.reason }
  } catch {
    return { ok: false, request: 'thrown' }
  }
}

/** The pull premise of `hostId`, read off the host store this instant (`PullPremiseReason`). Null = it holds. */
export function brokenPullPremise(hostId: string): PullPremiseReason | null {
  const hosts = useHostStore.getState()
  if (selectDaemonIdMismatch(hosts, hostId) !== undefined) return 'master-mismatch'
  if (!selectDaemonIdVerified(hosts, hostId)) return 'master-unverified'
  return null
}

/**
 * THE door (see the header): a frozen plan, or why there is none. Nothing is written, here or on the host.
 * `promoted`: a retry after the promote went through — the chosen local profile IS the master by now, so it is
 * no longer looked for among the local profiles.
 *
 * A PULL ASKS ONE THING MORE (host-sync-identity spec §8, D3): the host verified, with no mismatch
 * (`brokenPullPremise`) — before the host is asked and again after. Nothing of the SOT's `hosts` is read (host
 * ownership H3b): the list is the only request, and it goes to the address the host had when this began
 * (`expectEndpoint`).
 */
export async function prepareRun(draft: WizardDraft, promoted = false): Promise<PrepareResult> {
  const { hostId, profileId, seen, direction } = draft
  if (hostId === null || profileId === null || seen === null || direction === null) return { ok: false, reason: 'incomplete' }
  const pulling = direction === 'pull'
  const premises = (): PremiseReason | PullPremiseReason | null =>
    brokenLocalPremise(hostId, draft.localId, { host: true, local: !promoted }) ?? (pulling ? brokenPullPremise(hostId) : null)
  const before = premises()
  if (before !== null) return { ok: false, reason: before }
  // Every request of this door goes here or nowhere (the host is in the store: `premises` said so).
  const at = endpointOfHost(useHostStore.getState().hosts[hostId])

  const listedNow = await listOrClass(hostId, at)
  if (!listedNow.ok) return { ok: false, reason: 'list-failed', request: listedNow.request }
  // The ask took a while: this device may have moved meanwhile.
  const after = premises()
  if (after !== null) return { ok: false, reason: after }

  const entry = listedNow.rows.find((row) => row.id === profileId)
  if (entry === undefined) return { ok: false, reason: 'profile-gone' }
  const now = sotNow(entry)
  if (now.fingerprint !== seen) return { ok: false, reason: 'profile-changed', now }

  return { ok: true, plan: Object.freeze({ hostId, profileId, localId: draft.localId, direction, saveAs: pulling ? draft.saveAs : null, at, seen }) }
}

/**
 * A RETRY'S PLAN: the plan the run was started with (`old` — its sub-steps' states are indexed by it), re-aimed at
 * what the door has just found (`fresh`): the host's address now (`at`) and the fingerprint (`seen`). Only those:
 * what is done stays done, and the retry goes on from where it stopped. Null when `fresh` is for another host or
 * profile, or would run other sub-steps, than `old` — then it is not the same plan, and the user chooses again.
 * The door is asked with `old`'s own draft, so this is a second lock, not the first.
 */
export function retargetPlan(old: Readonly<WizardPlan>, fresh: Readonly<WizardPlan>): Readonly<WizardPlan> | null {
  const steps = subStepsOf(old)
  const same = subStepsOf(fresh)
  if (old.hostId !== fresh.hostId || old.profileId !== fresh.profileId || steps.length !== same.length || steps.some((id, i) => id !== same[i])) return null
  return Object.freeze({ ...old, at: fresh.at, seen: fresh.seen })
}

/**
 * THE ASK BEFORE THE ATTACH (review, attacker H1). `prepareRun` answered before the promote and the copy; those are
 * asynchronous, and meanwhile the SOT may have moved, a host may have been added here, or the attach host may have
 * turned out to be at another daemon. So right before `attachMaster` everything `prepareRun` checks is checked
 * again, against the PLAN — the address it was made for (`at`) and the fingerprint it was made against (`seen`).
 * Anything moved → no attach, and the refusal is `prepareRun`'s own (the wizard sends the user back as it does for
 * those):
 *   the host re-pointed           `list-failed` / `endpoint-changed`   (retryable: the next door reads the new address,
 *                                                                      and the retry's plan is re-aimed there: `retargetPlan`)
 *   the fingerprint               `profile-changed`, with what is there now
 *   attach host verified no more  `master-unverified` / `master-mismatch`
 *   profile gone, this device's premises (the attach host removed here: `host-gone`) — as `prepareRun`.
 * The chosen local profile is not looked for: by now it has been promoted (or was the master all along). A host
 * added or removed here meanwhile (other than the attach host) changes nothing: a pull removes no host.
 */
export async function recheckBeforeAttach(plan: WizardPlan): Promise<PrepareResult> {
  const moved = (): boolean => {
    const host = useHostStore.getState().hosts[plan.hostId]
    return host !== undefined && endpointOfHost(host) !== plan.at
  }
  if (moved()) return { ok: false, reason: 'list-failed', request: 'endpoint-changed' }
  const again = await prepareRun({ hostId: plan.hostId, profileId: plan.profileId, seen: plan.seen, localId: MASTER_PROFILE_ID, direction: plan.direction, saveAs: plan.saveAs }, true)
  if (again.ok && (again.plan.at !== plan.at || moved())) return { ok: false, reason: 'list-failed', request: 'endpoint-changed' }
  return again
}

// === Creating the SOT profile ===

/** Failures after which nobody knows whether the daemon created the profile (see the header). */
const OUTCOME_UNKNOWN: ReadonlySet<string> = new Set(['timeout', 'network', 'aborted', 'server', 'malformed', 'thrown'])

export type CreateResult =
  | { ok: true; id: string }
  /** Unknown, and a profile that MAY be the one has appeared: shown to the user, never taken (see the header). */
  | { ok: false; outcome: 'maybe'; request: string; candidateId: string }
  /** `failed`: the daemon said no — nothing was created. `not-created`: unknown at first, then looked for and not
   *  there. `unknown`: could not be looked for — the next press must look first. `same-name`: no baseline, and a
   *  profile of that name, empty, is there: ours or not, nobody can say. `request`: the failure's class. */
  | { ok: false; outcome: 'failed' | 'not-created' | 'unknown' | 'same-name'; request: string }

type Looked = { kind: 'found'; id: string } | { kind: 'absent' } | { kind: 'same-name' } | { kind: 'list-failed'; request: string }

async function lookForCreated(hostId: string, name: string, baseline: readonly string[] | null): Promise<Looked> {
  const listedNow = await listOrClass(hostId)
  if (!listedNow.ok) return { kind: 'list-failed', request: listedNow.request }
  const blank = listedNow.rows.filter((row) => row.name === name && row.sections.length === 0 && row.attachments.length === 0)
  if (baseline === null) return blank.length > 0 ? { kind: 'same-name' } : { kind: 'absent' }
  const known = new Set(baseline)
  const ours = blank.filter((row) => !known.has(row.id)).sort((a, b) => b.createdAt - a.createdAt)
  return ours.length > 0 ? { kind: 'found', id: ours[0].id } : { kind: 'absent' }
}

/**
 * `baseline`: the ids the host listed before this visit's FIRST POST to it; null = never read. `lookFirst`: an
 * earlier attempt for this host and name ended `unknown` (or `same-name`) — the host is asked what it holds
 * BEFORE anything is sent, and nothing is sent unless it could be asked.
 */
export async function createSotProfile(hostId: string, name: string, baseline: readonly string[] | null, lookFirst: boolean): Promise<CreateResult> {
  const settle = (looked: Looked, request: string): CreateResult | null => {
    if (looked.kind === 'found') return { ok: false, outcome: 'maybe', request, candidateId: looked.id }
    if (looked.kind === 'list-failed') return { ok: false, outcome: 'unknown', request }
    if (looked.kind === 'same-name') return { ok: false, outcome: 'same-name', request }
    return null
  }
  if (lookFirst) {
    const looked = await lookForCreated(hostId, name, baseline)
    const settled = settle(looked, looked.kind === 'list-failed' ? looked.request : 'thrown')
    if (settled !== null) return settled
  }
  let request: string
  try {
    const r = await createProfile(hostId, name)
    if (r.kind === 'ok') return { ok: true, id: r.value.id }
    request = r.reason
  } catch {
    request = 'thrown'
  }
  if (!OUTCOME_UNKNOWN.has(request)) return { ok: false, outcome: 'failed', request }
  return settle(await lookForCreated(hostId, name, baseline), request) ?? { ok: false, outcome: 'not-created', request }
}

// === Saying the result ===

/** A finished run is always said; a failed one only when the wizard is not there to say it (`wizardMounted`). */
export function announceRun(result: RunResult, plan: WizardPlan, labels: { profile: string; host: string }, wizardMounted: boolean): void {
  const t = useI18nStore.getState().t
  if (result.done) {
    const saved = plan.direction === 'pull' && plan.saveAs !== null
    useUndoToast.getState().show(saved ? t('settings.profile.wizard.toast.done_saved', { ...labels, name: plan.saveAs ?? '' }) : t('settings.profile.wizard.toast.done', labels))
    return
  }
  if (wizardMounted) return
  // Which step, and nothing of why: the reason is a sentence of the wizard's, and the wizard is gone.
  useUndoToast.getState().show(t(`settings.profile.wizard.toast.stopped_${subStepsOf(plan)[result.failedAt]}`))
}
