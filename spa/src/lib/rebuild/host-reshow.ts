// spa/src/lib/rebuild/host-reshow.ts — showing a host again recovers its sessions (host ownership H2d-4, §0.21
// step 3).
//
// While a host is hidden in this workbench its panes are gated and the per-pane sweeps skip it (the revive pass
// included), so a pane whose session came back stays on Rebuild. The daemon pushes a `sessions` frame only when its
// list changes, so an idle host would never revive it on its own. On each hidden → shown transition this calls the
// EXISTING `recoverHostSessions` (refresh → the `sessions` reconcile → the revive pass, fenced as today; deferred to
// the operation-lock release when the lock is held).
//
// - A store subscriber, not the switch's click handler: a show that arrives by a `settings` apply recovers too.
// - Transitions are tracked per LOCAL id (`isRefShown`), and each reshown local row gets its own call: two rows
//   claiming one daemon (an identity conflict) are shown together, but reconcile / revive are scoped to the local id
//   they are given, so deduping by daemon would leave the other row's panes on Rebuild.
// - Not a transition: a host added (it has no "before"), a host removed, a daemonId learned (the local-id form keeps
//   the host shown across the re-key).
// - PER WORKBENCH (2026-09-25 plan, A6): the list judged is the CURRENT one (`currentShownIdsNow` — the workbench on
//   screen's), so a world switch that turns X from hidden to shown recovers X, and a write to the on-screen slave's
//   own list does too. While nobody can say whose list applies (`resolveShownOwnerNow` is null: a switch or a promote
//   half-arrived) nothing is judged and the baseline is kept — the unsettled moments of a switch are no transitions.
// - The baseline is taken once FOUR stores have hydrated — the host store (which local row a `d1_…` id means), the
//   shown store, the local profiles and the tab store (whose list, and is it settled) — so the boot hydration of a
//   stored list, or of a daemonId, is no transition.
// - Never writes the tab / workspace / shown-hosts / local-profiles stores.
import { useWorkspaceStore } from '../../features/workspace/store'
import { useHostStore } from '../../stores/useHostStore'
import { useLocalProfilesStore } from '../../stores/useLocalProfilesStore'
import { useShownHostsStore } from '../../stores/useShownHostsStore'
import { useTabStore } from '../../stores/useTabStore'
import { currentShownIdsNow, isRefShown, resolveShownOwnerNow } from '../shown-hosts'
import { recoverHostSessions } from './refresh-sessions'

const STORES = [useHostStore, useShownHostsStore, useLocalProfilesStore, useTabStore] as const

/** Local id → shown, for every local host; `null` until the four stores have hydrated. */
let baseline: Map<string, boolean> | null = null
let stopActive: (() => void) | null = null

function snapshot(): Map<string, boolean> {
  const { hosts } = useHostStore.getState()
  const ids = currentShownIdsNow()
  const out = new Map<string, boolean>()
  for (const id of Object.keys(hosts)) out.set(id, isRefShown(id, hosts, ids))
  return out
}

function check(): void {
  if (!STORES.every((store) => store.persist.hasHydrated())) return // the finish callback catches up
  if (resolveShownOwnerNow() === null) return // nobody can say whose list applies: judged once somebody can
  const now = snapshot()
  const before = baseline
  baseline = now
  if (before === null) return
  const reshown = [...now].filter(([id, shown]) => shown && before.get(id) === false).map(([id]) => id)
  // One call per reshown LOCAL row, not per daemon: reconcile and revive filter panes by the local id they are given,
  // so two rows claiming one daemon (an identity conflict) each need their own — the cost is one extra fetch.
  for (const id of reshown) {
    recoverHostSessions(id).catch(() => { /* a host that cannot answer waits for its next frame */ })
  }
}

/** Installs the subscriber (app lifetime; `main.tsx`). Installing again keeps the one already running. */
export function startHostReshowRecovery(): () => void {
  if (stopActive !== null) return stopActive
  const unsubs = STORES.map((store) => store.persist.onFinishHydration(check))
  unsubs.push(useHostStore.subscribe((state, prev) => {
    if (state.hosts !== prev.hosts) check() // runtime / active-host churn cannot move the answer
  }))
  unsubs.push(useShownHostsStore.subscribe(check))
  unsubs.push(useLocalProfilesStore.subscribe(check)) // the pointer, the epoch, the relabel count, the slaves' lists
  // The world tags: only a change of them — the tab store changes with every tab.
  unsubs.push(useTabStore.subscribe((state, prev) => {
    if (state.worldId !== prev.worldId || state.worldEpoch !== prev.worldEpoch) check()
  }))
  unsubs.push(useWorkspaceStore.subscribe((state, prev) => {
    if (state.worldId !== prev.worldId || state.worldEpoch !== prev.worldEpoch) check()
  }))
  check()
  const stop = () => {
    for (const unsub of unsubs) unsub()
    if (stopActive === stop) stopActive = null
    baseline = null
  }
  stopActive = stop
  return stop
}

export function __resetHostReshowForTest(): void {
  stopActive?.()
  stopActive = null
  baseline = null
}
