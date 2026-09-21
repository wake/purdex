// spa/src/features/workspace/lib/adopt-standalone.ts — EVERY TAB BELONGS TO EXACTLY ONE WORKSPACE, kept as a
// standing invariant rather than a boot step (Profile Sync spec §4.3; P3 plan, P3c-1).
//
// `insertTab` (../store.ts) gives every tab it is asked about a workspace. This file is for the tabs nobody
// asked it about: device-state's restore and merge write `tabOrder` wholesale (lib/device-state, until P4b), a
// pre-P3c device has them persisted, and there may be a producer nobody has found. Whenever the tab world
// changes and a tab is in no workspace, it is moved into `Unsorted` (`UNSORTED_WORKSPACE_ID`), created only
// then. It also re-points an `activeWorkspaceId` of `null` — the old "Home" view, which no longer exists — at
// a workspace. It is app-level, not Profile Sync's: it runs for a user with no master, too.
//
// EXACTLY ONE is also NOT TWO: old storage, a snapshot restore and cross-window last-write-wins can list one tab
// in two workspaces (the tab bar renders it in both; `tabOrder` has it once). The FIRST workspace in workspace
// order keeps it — a rule with no choice in it, so two windows that each apply it get the same world — and a
// workspace that loses its active tab gets `activeTabId: null`, which is what `removeTabFromWorkspace` and
// `insertTab`'s dedup do. (A `workspace.tabs` id that is in no tab store is not this file's business.)
//
// NOT IN THE SAME TICK, AND NOT AT START: A DEBOUNCE. The tab store and the workspace store are two stores: a
// restore writes one and then the other, and another window's `addTab` + `insertTab` arrive here as two
// rehydrates, in two tasks (lib/storage/sync.ts). In between, a tab that HAS a workspace looks like one that has
// none — and adopting it then would write this window's not-yet-rehydrated workspaces over the other window's
// (zustand persists the whole store per write; that is not solved here). So a change only (re-)arms a timer, and
// the look happens `ADOPTION_SETTLE_MS` after the LAST change — a throttle would fire 10 ms after a rehydrate
// that came late in the wait. The look reads the stores as they are THEN, never a copy from when it was armed.
// Start is no exception: what storage held a moment ago may be the first half of another window's write, so the
// first look waits like any other (a pre-P3c device shows its ownerless tabs in no workspace for that long).
// Re-pointing `activeWorkspaceId` waits, too — it is a write of the same whole store.
//   …AND A DEADLINE, BECAUSE MEMBERSHIP ITSELF MAY NEVER GO QUIET (another window's agent opening and closing tabs
// more often than every ADOPTION_SETTLE_MS): what has needed repair for ADOPTION_MAX_WAIT_MS — THAT tab id, without
// a break, in a settled world — is repaired then, alone. See `pendingSince`.
//   ONLY MEMBERSHIP PUSHES IT BACK. A busy agent rewrites pane records in the tab store several times a second
// (useAgentStore → `setPaneRebuild`), in every window, and each of those is a rehydrate in the others. None of
// that changes who owns what, so none of it re-arms the timer: the look cannot be starved, and needs no cap.
//
// ONLY A SETTLED WORLD. While another window is half-way through a profile switch, this one holds the tabs of
// one world and the workspaces of another (lib/profile/master-world.ts): every tab looks ownerless, and
// adopting them would put a whole profile's tabs into the wrong profile's `Unsorted`, for good. Unsettled →
// nothing is written; the store change that settles the world arms the timer again (hence the third
// subscription). For a user who never made a local profile the world is always settled: the three epochs are
// 0, both ids `master`, the fence absent.
//
// THE RULE ITSELF IS NOT IN THIS FILE: who keeps a tab, who adopts one, and when the pointer follows the tab on
// screen is `repairTabOwnership` (lib/profile/sections.ts) — shared with switch-active.ts, which repairs a world
// it is about to park (nobody repairs a parked one) and asks `tabOwnershipQuiet` below whether the wait this
// file applies is over. This file is the WHEN: the debounce, the settled check, the write.
//
// NO LOOP. A write here changes the workspace store, which calls the subscriber again — which finds nothing to
// do and arms nothing. The timer exists only while there is something to do.
import { useLocalProfilesStore } from '../../../stores/useLocalProfilesStore'
import { useI18nStore } from '../../../stores/useI18nStore'
import { useTabStore } from '../../../stores/useTabStore'
import { readMasterWorld } from '../../../lib/profile/master-world'
import { repairTabOwnership } from '../../../lib/profile/sections'
import { UNSORTED_WORKSPACE_ID, useWorkspaceStore } from '../store'

/** How long the tab world must have been left alone before a tab without a workspace is believed to be one. */
export const ADOPTION_SETTLE_MS = 500

/**
 * …or how long THE SAME tab must have been in need of repair, however busy the rest of the world is. The
 * debounce alone can be starved for ever: another window (a busy agent) can change the membership more often
 * than every ADOPTION_SETTLE_MS. The transition the debounce protects — another window's `addTab`, its
 * `insertTab` one rehydrate later — lasts milliseconds; a tab id that has been ownerless, or listed twice,
 * for this long is no transition.
 */
export const ADOPTION_MAX_WAIT_MS = 3000

/** What a look depends on, as a string: who owns which tab, which tabs exist, the pointer, and the three world tags. */
function signature(): string {
  const tab = useTabStore.getState()
  const ws = useWorkspaceStore.getState()
  const local = useLocalProfilesStore.getState()
  return JSON.stringify([
    Object.keys(tab.tabs), ws.workspaces.map((w) => [w.id, w.tabs]), ws.activeWorkspaceId,
    tab.worldId, tab.worldEpoch, ws.worldId, ws.worldEpoch, local.activeProfileId, local.worldEpoch,
  ])
}

/**
 * Anything to do? One pass over the workspaces, one over the tabs. The same questions `reconcile` asks (a tab
 * `tabOrder` does not mention counts, too), so "yes" means a write.
 */
function needsWork(): boolean {
  const { workspaces, activeWorkspaceId } = useWorkspaceStore.getState()
  if (activeWorkspaceId === null && workspaces.length > 0) return true
  const owned = new Set<string>()
  for (const ws of workspaces) {
    for (const id of ws.tabs) {
      if (owned.has(id)) return true
      owned.add(id)
    }
  }
  return Object.keys(useTabStore.getState().tabs).some((id) => !owned.has(id))
}

/**
 * When the membership signature last moved, while the invariant is installed; `null` while it is not.
 * `tabOwnershipQuiet` is for whoever must decide NOW whether a tab without a workspace is really one
 * (switch-active.ts, about to park the screen): only after the same wait this file gives it. Not installed →
 * nobody is watching → not quiet: it fails closed.
 */
let lastChangeAt: number | null = null

/**
 * WHAT NEEDS REPAIR RIGHT NOW, AND SINCE WHEN: tab id → when it was first seen ownerless or listed twice, without
 * a break since (`POINTER`: the `null` pointer, which a starved debounce would leave, too). Only what needs repair
 * NOW is in here — an id that is fine again is dropped at the next change, so it cannot grow. NO AGE WHILE THE
 * WORLD IS UNSETTLED: tabs of one world next to workspaces of another make every tab look ownerless; the map is
 * emptied, and a tab's clock starts when the world settles.
 */
const pendingSince = new Map<string, number>()
const POINTER = '#pointer'

function pendingNow(): Set<string> {
  const { workspaces, activeWorkspaceId } = useWorkspaceStore.getState()
  const pending = new Set<string>()
  if (activeWorkspaceId === null && workspaces.length > 0) pending.add(POINTER)
  const owned = new Set<string>()
  for (const ws of workspaces) {
    for (const id of ws.tabs) {
      if (owned.has(id)) pending.add(id)
      owned.add(id)
    }
  }
  for (const id of Object.keys(useTabStore.getState().tabs)) if (!owned.has(id)) pending.add(id)
  return pending
}

/** Brings `pendingSince` up to date. Called on every membership change: what needs repair cannot change without one. */
function observePending(now: number): void {
  if (!readMasterWorld().settled) {
    pendingSince.clear()
    return
  }
  const pending = pendingNow()
  for (const id of [...pendingSince.keys()]) if (!pending.has(id)) pendingSince.delete(id)
  for (const id of pending) if (!pendingSince.has(id)) pendingSince.set(id, now)
}

const agedAt = (now: number): string[] => [...pendingSince].filter(([, since]) => now - since >= ADOPTION_MAX_WAIT_MS).map(([id]) => id)

/** Quiet for ADOPTION_SETTLE_MS — or everything that needs repair has needed it for ADOPTION_MAX_WAIT_MS, so the wait is bounded. */
export function tabOwnershipQuiet(now: number = Date.now()): boolean {
  if (lastChangeAt === null) return false
  return now - lastChangeAt >= ADOPTION_SETTLE_MS || (pendingSince.size > 0 && agedAt(now).length === pendingSince.size)
}

export const __pendingRepairCountForTest = (): number => pendingSince.size
export const __allPendingAgedForTest = (now: number): boolean => pendingSince.size > 0 && agedAt(now).length === pendingSince.size

/** One look, on what the stores hold NOW. `only`: these tab ids and no other (the bounded way out); absent = everything. */
function reconcile(only?: ReadonlySet<string>): void {
  if (!needsWork() || !readMasterWorld().settled) return
  const { tabs, tabOrder, activeTabId } = useTabStore.getState()
  const current = useWorkspaceStore.getState()
  // The rule itself — who keeps a tab, who adopts one, when the pointer follows — is `repairTabOwnership`'s.
  const { workspaces, activeWorkspaceId, adopted, dropped, membershipChanged: membership } = repairTabOwnership(
    { workspaces: current.workspaces, tabs, tabOrder, activeTabId, activeWorkspaceId: current.activeWorkspaceId },
    { unsortedName: useI18nStore.getState().t('workspace.unsorted'), newWorkspaceId: UNSORTED_WORKSPACE_ID, only },
  )
  // Cannot happen while `needsWork` and the repair agree; if they ever stop, this is what keeps a
  // write that changes nothing from arming the next timer, for ever.
  if (!membership && activeWorkspaceId === current.activeWorkspaceId) return

  useWorkspaceStore.setState(membership ? { workspaces, activeWorkspaceId } : { activeWorkspaceId })
  if (adopted.length > 0) console.info(`[workspace] ${adopted.length} tab(s) had no workspace; moved into "${UNSORTED_WORKSPACE_ID}"`)
  if (dropped > 0) console.info(`[workspace] ${dropped} tab listing(s) removed: a tab was in more than one workspace`)
}

/** Installs the invariant for the lifetime of the app (main.tsx). Returns how to stop it. */
export function startStandaloneAdoption(): () => void {
  let timer: ReturnType<typeof setTimeout> | null = null
  let last: string | null = null
  // THE DEADLINE: one timer, for the OLDEST thing that needs repair — it exists only while something does, and a
  // membership change that leaves the oldest one as it is does not re-arm it (or a busy agent would arm timers
  // all day). When it fires, what is ADOPTION_MAX_WAIT_MS old is repaired and NOTHING ELSE: a younger one may be
  // another window's tab whose workspace is one rehydrate away.
  let deadlineTimer: ReturnType<typeof setTimeout> | null = null
  let deadlineAt: number | null = null

  const armDeadline = (): void => {
    const oldest = pendingSince.size === 0 ? null : Math.min(...pendingSince.values()) + ADOPTION_MAX_WAIT_MS
    if (oldest === deadlineAt) return
    if (deadlineTimer !== null) clearTimeout(deadlineTimer)
    deadlineAt = oldest
    deadlineTimer = oldest === null
      ? null
      : setTimeout(() => {
          deadlineTimer = null
          deadlineAt = null
          observePending(Date.now()) // an unsettled world empties it: nothing is repaired, and its change re-arms
          const aged = agedAt(Date.now())
          if (aged.length > 0) reconcile(new Set(aged)) // its write is a change: `onChange` re-arms for what is left
          armDeadline()
        }, Math.max(0, oldest - Date.now()))
  }

  const onChange = (): void => {
    const now = signature()
    if (now === last) return
    last = now
    lastChangeAt = Date.now()
    observePending(lastChangeAt)
    armDeadline()
    if (timer !== null) clearTimeout(timer)
    timer = needsWork()
      ? setTimeout(() => {
          timer = null
          reconcile()
        }, ADOPTION_SETTLE_MS)
      : null
  }

  onChange()
  const unsubscribe = [useTabStore.subscribe(onChange), useWorkspaceStore.subscribe(onChange), useLocalProfilesStore.subscribe(onChange)]
  return () => {
    for (const off of unsubscribe) off()
    if (timer !== null) clearTimeout(timer)
    if (deadlineTimer !== null) clearTimeout(deadlineTimer)
    timer = null
    deadlineTimer = null
    lastChangeAt = null
    pendingSince.clear()
  }
}
