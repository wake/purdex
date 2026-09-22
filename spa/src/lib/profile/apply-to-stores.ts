// spa/src/lib/profile/apply-to-stores.ts — lands one section pulled from the SOT
// in the app's stores (Profile Sync spec §4.7; P2b plan Task 7). The impure half
// of the applier: applier.ts computes `(local, incoming) → next`, this file reads
// the slices, writes `next`, and reports the hash the stores hold AFTERWARDS.
//
// How a write lands (hosts and the eight settings stores). A bare `store.setState(patch)` skips the invariants that
// live in each store's persist `merge` / `onRehydrateStorage` (sanitise, heal,
// theme DOM attribute, the i18n translator `t`). So every write is followed by
// `store.persist.rehydrate()` — the path a cross-window sync already takes. That
// this is sound is pinned in apply-to-stores.test.ts ("premises"):
//   - persist writes localStorage synchronously inside `setState`, so the
//     rehydrate reads exactly what was just written;
//   - with a synchronous storage the whole rehydrate runs synchronously (zustand's
//     `toThenable`), so nothing can interleave between the write and the hooks;
//   - `merge` spreads the CURRENT state first, so non-persisted state (`runtime`,
//     `visitHistory`, actions) survives.
// One thing rehydrate does NOT do: several stores heal by mutating the state
// object in place (`healLayoutInvariant`, `sanitizeScopedModuleMap`, …) and a
// `merge` result is never written back — subscribers are not told and
// localStorage keeps the unhealed value. `publish` closes that with an empty
// `setState({})`: a new state object, listeners fire, persist writes.
//
// The returned hash is rebuilt from the stores, never copied from the SOT: when a
// sanitiser changed what arrived, the section is honestly dirty.
import { useHostSettingsStore } from '../../stores/useHostSettingsStore'
import { useHostStore } from '../../stores/useHostStore'
import type { HostConfig } from '../../stores/useHostStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { useLayoutStore } from '../../stores/useLayoutStore'
import { useNewTabLayoutStore } from '../../stores/useNewTabLayoutStore'
import { splitExecutionKey, useExecutionStore } from '../../stores/useExecutionStore'
import { useExecutionListStore } from '../../stores/useExecutionListStore'
import type { HostListCache } from '../../stores/useExecutionListStore'
import { useNexHostStore } from '../../stores/useNexHostStore'
import type { NexHostEntry } from '../../stores/useNexHostStore'
import type { ExecutionState } from '../nex/event-reducer'
import { useNotificationSettingsStore } from '../../stores/useNotificationSettingsStore'
import { withOperationLock } from '../../stores/useRebuildStore'
import { useThemeStore } from '../../stores/useThemeStore'
import { useUISettingsStore } from '../../stores/useUISettingsStore'
import { useWorkspaceSettingsStore } from '../../stores/useWorkspaceSettingsStore'
import type { PaneLayout, Tab } from '../../types/tab'
import { deleteHostCascade } from '../host-lifecycle'
import { registerLocale, unregisterLocale } from '../locale-registry'
import type { LocaleDef } from '../locale-registry'
import { registerTheme, unregisterTheme } from '../theme-registry'
import type { ThemeDefinition } from '../theme-registry'
import { applyHosts, applySettings, applyTabs, applyWorkspaces, isWellFormedSection, upcastLegacySettings } from './applier'
import { hashSection } from './hash'
import { masterWorkspaceIds, readMasterWorld, writeMasterWorld } from './master-world'
import { sectionKind, workspaceIdOf } from './projections'
import { buildHostsSection, buildSettingsSection, buildTabsSection, buildWorkspacesSection } from './sections'
import type { SettingsBuildInput } from './sections'
import type { HostsPayload, ProfileSectionKey, SettingsPayload, SettingsStorageKey, TabsPayload, WorkspacesPayload } from './types'

// === Contract ===

export type ApplyOutcome =
  /** Written. `hash` is rebuilt from the stores afterwards; `null` = the section does not exist locally (nothing was written). */
  | { ok: true; hash: string | null }
  /** Not now, retry later, never lock the section: the operation lock is held by someone else — or the master's
   *  tab world is unsettled (master-world.ts: a switch is half-way through this window's rehydrates), so there is
   *  nowhere to write `workspaces` / `tabs.*` and no master workspace set to scope `settings` by. */
  | { ok: false; reason: 'busy' }
  /** This payload must not be applied: the caller locks the section (`locked:invalid`). No store was written.
   *  `code` is WHY, from a closed list (the page says it in words); `detail` is for the problem log. */
  | { ok: false; reason: 'invalid'; code: InvalidReason; detail: string }

/**
 * Why a payload is refused — one code per refusal below, and nothing else (P3d-4b). It is published per section
 * (`SectionDetail.invalidReason`) and shown in words, so it never carries the payload's or the transport's text.
 *   deleted              the host deleted `hosts` / `settings` / `workspaces` — sections this device cannot be without
 *   malformed            not a payload the builders could have produced (`isWellFormedSection`)
 *   no-host              a `hosts` payload that leaves no host at all
 *   removes-master-host  a `hosts` payload without the host this device syncs through
 *   changes-master-host  … that changes that host's address, port or token (the credentials known to work)
 *   rejected-settings    `settings` entries this build refuses (`applySettings`' `rejected`)
 *   unknown-section      a key this build does not know as a section
 */
export type InvalidReason = 'deleted' | 'malformed' | 'no-host' | 'removes-master-host' | 'changes-master-host' | 'rejected-settings' | 'unknown-section'

export const INVALID_REASONS: readonly InvalidReason[] = ['deleted', 'malformed', 'no-host', 'removes-master-host', 'changes-master-host', 'rejected-settings', 'unknown-section']

export interface ApplyContext {
  /** The host whose daemon served this payload. Its credentials are the ones known to work. */
  masterHostId: string
}

export const PROFILE_SYNC_LOCK_OWNER = 'profile-sync'

// === Store plumbing ===

/** What this file needs of a persisted zustand store — declared here because each store's state type is private to it. */
interface PersistedStore {
  getState: () => object
  setState: (patch: Record<string, unknown>) => void
  persist: { rehydrate: () => void | Promise<void> }
}

const asPersisted = (store: unknown): PersistedStore => store as PersistedStore

const SETTINGS_STORES: Record<SettingsStorageKey, PersistedStore> = {
  'purdex-ui-settings': asPersisted(useUISettingsStore),
  'purdex-themes': asPersisted(useThemeStore),
  'purdex-i18n': asPersisted(useI18nStore),
  'purdex-notification-settings': asPersisted(useNotificationSettingsStore),
  'purdex-workspace-settings': asPersisted(useWorkspaceSettingsStore),
  'purdex-host-settings': asPersisted(useHostSettingsStore),
  'purdex-newtab-layout': asPersisted(useNewTabLayoutStore),
  'purdex-layout': asPersisted(useLayoutStore),
}

/** The eight settings stores' current states, by storage key: the input of `buildSettingsSection` and `applySettings`. */
export function readSettingsSources(): SettingsBuildInput {
  const out: SettingsBuildInput = {}
  for (const key of Object.keys(SETTINGS_STORES) as SettingsStorageKey[]) out[key] = SETTINGS_STORES[key].getState()
  return out
}

/** Runs the store's own `merge` / `onRehydrateStorage` over what was just written. Synchronous with today's storage; awaited anyway. */
async function rehydrate(store: PersistedStore): Promise<void> {
  await store.persist.rehydrate()
}

/** Tells subscribers about, and persists, whatever the rehydrate healed in place (see the header). */
function publish(store: PersistedStore): void {
  store.setState({})
}

/** Best-effort restore of fields captured before a write; a rollback that throws must not stop the next one. */
function restore(store: PersistedStore, old: Record<string, unknown>): void {
  try {
    store.setState(old)
  } catch {
    // the in-memory state is restored before persist's storage write can throw
  }
}

const messageOf = (err: unknown): string => (err instanceof Error ? err.message : String(err))

const invalid = (code: InvalidReason, detail: string): ApplyOutcome => ({ ok: false, reason: 'invalid', code, detail })

const BUSY: ApplyOutcome = { ok: false, reason: 'busy' }

// === host-removed ===

/**
 * `layout` with every live `tmux-session` pane whose host is not in
 * `knownHostIds` marked `terminated: 'host-removed'`. Pure; returns the same
 * object when nothing changed. A pane that is already terminated keeps its reason.
 */
export function markHostRemovedPanes(layout: PaneLayout, knownHostIds: ReadonlySet<string>): PaneLayout {
  if (layout.type === 'leaf') {
    const c = layout.pane.content
    if (c.kind !== 'tmux-session' || c.terminated !== undefined || knownHostIds.has(c.hostId)) return layout
    return { ...layout, pane: { ...layout.pane, content: { ...c, terminated: 'host-removed' } } }
  }
  let changed = false
  const children = layout.children.map((child) => {
    const next = markHostRemovedPanes(child, knownHostIds)
    if (next !== child) changed = true
    return next
  })
  return changed ? { ...layout, children } : layout
}

// === hosts ===

const tokenOf = (h: HostConfig): string | null => h.token ?? null

/** Why this payload must not replace the host list, or `null`. */
function hostsRefusal(local: Record<string, HostConfig>, incoming: HostsPayload, masterHostId: string): ApplyOutcome | null {
  if (Object.keys(incoming.hosts).length === 0) return invalid('no-host', 'payload leaves no host')
  if (!Object.hasOwn(incoming.hosts, masterHostId)) return invalid('removes-master-host', `payload removes the master host ${masterHostId}`)
  if (!Object.hasOwn(local, masterHostId)) return null // nothing to compare with
  const mine = local[masterHostId]
  const theirs = incoming.hosts[masterHostId]
  // The credentials in hand are the ones that just fetched this payload, so they are known to work.
  const changed = [mine.ip !== theirs.ip && 'ip', mine.port !== theirs.port && 'port', tokenOf(mine) !== tokenOf(theirs) && 'token'].filter(Boolean)
  return changed.length > 0 ? invalid('changes-master-host', `payload changes the master host's ${changed.join(', ')}`) : null
}

/** What `deleteHostCascade` clears for a host and its undo does not bring back — exactly the scope of the three `clearHost`s. */
interface HostCaches {
  hostIds: ReadonlySet<string>
  /** `useExecutionStore.executions` entries whose key names one of the hosts. */
  executions: Record<string, ExecutionState>
  /** `useExecutionListStore.byHost[id]`, where present. */
  lists: Record<string, HostListCache>
  /** `useNexHostStore.byHost[id]`, where present. */
  nex: Record<string, NexHostEntry>
}

function snapshotHostCaches(hostIds: readonly string[]): HostCaches {
  const ids = new Set(hostIds)
  const pick = <T>(record: Record<string, T>): Record<string, T> => {
    const out: Record<string, T> = {}
    for (const id of ids) if (Object.hasOwn(record, id)) out[id] = record[id]
    return out
  }
  const executions: Record<string, ExecutionState> = {}
  for (const [key, value] of Object.entries(useExecutionStore.getState().executions)) {
    if (ids.has(splitExecutionKey(key).hostId)) executions[key] = value
  }
  return { hostIds: ids, executions, lists: pick(useExecutionListStore.getState().byHost), nex: pick(useNexHostStore.getState().byHost) }
}

/**
 * Writes the snapshot back: for THOSE hosts, the entries become exactly what they
 * were (anything that appeared for them meanwhile is dropped); every other host's
 * entries are left as they are now. None of the three stores persists, so a
 * throw here is unexpected — it is still reported, never swallowed. Returns what
 * could not be restored. Order matters: see ROLLBACK in `applyHostsSection`.
 */
function restoreHostCaches(snap: HostCaches): string[] {
  if (snap.hostIds.size === 0) return []
  const others = <T>(record: Record<string, T>, hostOf: (key: string) => string): Record<string, T> => {
    const out: Record<string, T> = {}
    for (const [key, value] of Object.entries(record)) if (!snap.hostIds.has(hostOf(key))) out[key] = value
    return out
  }
  const self = (key: string): string => key
  const steps: Array<[string, () => void]> = [
    ['execution', () => useExecutionStore.setState((s) => ({ executions: { ...others(s.executions, (k) => splitExecutionKey(k).hostId), ...snap.executions } }))],
    ['execution-list', () => useExecutionListStore.setState((s) => ({ byHost: { ...others(s.byHost, self), ...snap.lists } }))],
    ['nex-host', () => useNexHostStore.setState((s) => ({ byHost: { ...others(s.byHost, self), ...snap.nex } }))],
  ]
  const unfinished: string[] = []
  for (const [name, step] of steps) {
    try {
      step()
    } catch (err) {
      unfinished.push(`${name}: ${messageOf(err)}`)
    }
  }
  return unfinished
}

/**
 * Replaces the host list. Additions, edits and reorders are one write. A host the
 * payload REMOVES goes through `deleteHostCascade(id, false)` — the app's own
 * "remove this host, keep its tabs" — so this device ends up exactly where it
 * would be had the user deleted that host here: sessions / agent / execution /
 * execution-list / nex / host-settings / peer / cwd state cleared, every pane on
 * it marked `terminated: 'host-removed'`, hostless execution panes pinned,
 * `runtime` dropped, focus moved off it. Its undo handle is kept only to roll back.
 * The cascade reaches EVERY tab world — the one on screen and the parked ones, the
 * master's included (host-lifecycle.ts, "Parked worlds") — and so does its undo:
 * hosts are live whichever profile is on screen, so this apply never asks
 * master-world.ts anything and is never `busy` for an unsettled world.
 *
 * Two consequences, both intended:
 *   - the marked panes (and the cleared `purdex-host-settings.hosts` row) are
 *     synced fields, so those `tabs.<ws>` sections (and `settings`) go dirty and
 *     are pushed. That is correct — the host is gone from the profile, the device
 *     that removed it marked the same panes, and the two converge;
 *   - the cascade writes the tab store, so a removal needs the operation lock and
 *     can come back `busy`. The lock is taken BEFORE anything is written; an apply
 *     that removes no host never asks for it.
 *
 * The cascade ends in `useHostStore.removeHost`, which refuses to delete the last
 * host. So the write is staged: first `next` PLUS the hosts about to go (never
 * fewer than `next`, which the guard made non-empty), then the cascades, then
 * `next` exactly. All synchronous — no other writer sees the staging.
 */
async function applyHostsSection(payload: unknown, ctx: ApplyContext): Promise<ApplyOutcome> {
  if (payload === null) return invalid('deleted', 'the hosts section cannot be deleted')
  if (!isWellFormedSection('hosts', payload)) return invalid('malformed', 'malformed hosts payload')
  const incoming = payload as HostsPayload
  const refusal = hostsRefusal(useHostStore.getState().hosts, incoming, ctx.masterHostId)
  if (refusal !== null) return refusal

  const store = asPersisted(useHostStore)
  // ROLLBACK, and its edges. When a host-store write throws after the cascade ran
  // (persist: quota / SecurityError), the catch puts back, in this order:
  //   1. what the cascade's own undo handles restore — sessions, agent state,
  //      host settings, the marked panes (what "Undo" does after a manual delete);
  //   2. the host slice, `runtime[H]` included;
  //   3. the three stores the cascade clears and its undo does NOT restore —
  //      execution, execution-list, nex-host — from `snapshotHostCaches`, taken
  //      before the cascade. Nothing here counts on a re-fetch: the removal and
  //      the restore happen inside ONE synchronous block (see below), so React
  //      never sees the host go, no `hostPresent` / `hostId` effect re-runs, and
  //      what is not written back stays lost. (No `ensure` either: the nex-host
  //      entry returns as it was, `fetchedAt` included, so there is nothing to
  //      re-ask — on a fresh entry `ensure` is a no-op, on a stale one a TTL
  //      refresh that the next reader triggers anyway.)
  // What writing state back cannot undo, and why that is acceptable:
  //   - execution-list: `clearHost` CLOSES that host's site-wide SSE and frees its
  //     lane. The cache comes back; the stream is reopened by the app's own
  //     watcher (`startExecutionListInvalidation`), which sees nex-host go from
  //     absent to ready when step 3 restores it, and re-fetches the list. The
  //     list is restored BEFORE nex-host for that reason.
  //   - a mounted `useExecutionLease` reacts to the host leaving the store in a
  //     zustand subscriber (synchronous, so it does run): it stops its heartbeat
  //     and writes `lease: null`. The restore puts the lease back without a
  //     heartbeat — the state that hook already calls "idle": its next
  //     `ensureLease` re-arms the timer if the lease is still valid and
  //     re-acquires if not. Entries such a subscriber CREATED for the removed
  //     host during the cascade are dropped by the restore (it replaces the
  //     host's entries, it does not merge into them).
  //   - the per-pane SSE of `useExecutionSubscription` is torn down from a React
  //     effect, which never ran: it is still open and continues from the restored
  //     `lastSeq`.
  //   - NOT covered: `deleteHostCascade` throwing half-way (its own persist
  //     writes — tab store, host settings, host store — can fail too). It then
  //     returns no undo handle, so step 1 has nothing for that host; steps 2–3
  //     still run. Closing that needs an undo that survives a throw, in
  //     host-lifecycle.ts.
  // A restore step that throws is not swallowed: it is appended to the ORIGINAL
  // error as "rollback incomplete".
  //   `runtime[H]` is restored verbatim, `connected` included, and that is true
  // rather than stale ONLY because nothing is awaited between the staging write
  // and the end of the rollback: the connection layer (`useMultiHostEventWs`)
  // closes a host's WS from a React effect keyed on the host list, no effect runs
  // inside a synchronous block, and by the time one can, the list is what it was
  // — the WS never noticed. An `await` in that stretch would let the effect tear
  // the connection down, and the restored `connected` would be a lie until the
  // layer reconnected. Hence `persist.rehydrate()` is NOT awaited inside the try
  // (it is synchronous with this storage — premise (d) in the tests — and is
  // awaited after the last fallible step, for the day it is not).
  const write = async (): Promise<ApplyOutcome> => {
    const state = useHostStore.getState()
    const old = { hosts: state.hosts, hostOrder: state.hostOrder, activeHostId: state.activeHostId, devHostId: state.devHostId, runtime: state.runtime }
    const { next, removedHostIds } = applyHosts(old, incoming)
    const undos: Array<() => void> = []
    const caches = snapshotHostCaches(removedHostIds) // BEFORE the cascade clears them
    let hooks: void | Promise<void>
    try {
      if (removedHostIds.length > 0) {
        const leaving: Record<string, HostConfig> = {}
        for (const id of removedHostIds) leaving[id] = state.hosts[id]
        store.setState({ hosts: { ...next.hosts, ...leaving }, hostOrder: [...next.hostOrder, ...removedHostIds] })
        for (const id of removedHostIds) undos.push(deleteHostCascade(id, false))
      }
      // Focus is whatever the cascade left (it moves `activeHostId` to the first host, as a manual delete does) while that host survives.
      const now = useHostStore.getState()
      const survives = (id: string | null): string | null => (id !== null && Object.hasOwn(next.hosts, id) ? id : null)
      store.setState({ hosts: next.hosts, hostOrder: next.hostOrder, activeHostId: survives(now.activeHostId), devHostId: survives(now.devHostId) })
      hooks = store.persist.rehydrate() // not awaited here — see ROLLBACK above
      publish(store)
    } catch (err) {
      const unfinished: string[] = []
      for (const undo of undos.reverse()) {
        try {
          undo()
        } catch (undoErr) {
          unfinished.push(`undo: ${messageOf(undoErr)}`) // the host slice below is restored regardless
        }
      }
      restore(store, old)
      unfinished.push(...restoreHostCaches(caches))
      if (unfinished.length === 0) throw err
      throw new Error(`${messageOf(err)} (rollback incomplete — ${unfinished.join('; ')})`, { cause: err })
    }
    await hooks
    return { ok: true, hash: await hashSection(buildHostsSection(useHostStore.getState())) }
  }

  // Decided on the state as it is now; `write` re-reads under the lock, and nothing can run in between (no await).
  const removesAHost = Object.keys(useHostStore.getState().hosts).some((id) => !Object.hasOwn(incoming.hosts, id))
  if (!removesAHost) return write()
  return withOperationLock<ApplyOutcome>(PROFILE_SYNC_LOCK_OWNER, write, () => ({ ok: false, reason: 'busy' }))
}

// === settings ===

/** Puts a dropped custom theme / locale back in its registry. */
type Reregister = () => void

/**
 * Custom themes / locales the patch drops leave the registries too; rehydrate only
 * ever registers. Runs BEFORE the rehydrate, so an `active*Id` naming a dropped
 * entry falls back. Returns how to undo each removal, for the rollback.
 */
function unregisterDropped(key: SettingsStorageKey, before: Record<string, unknown>, patch: Record<string, unknown>): Reregister[] {
  const field = key === 'purdex-themes' ? 'customThemes' : key === 'purdex-i18n' ? 'customLocales' : null
  if (field === null || !Object.hasOwn(patch, field)) return []
  const kept = (patch[field] ?? {}) as Record<string, unknown>
  const undo: Reregister[] = []
  for (const [id, def] of Object.entries((before[field] ?? {}) as Record<string, unknown>)) {
    if (Object.hasOwn(kept, id)) continue
    if (key === 'purdex-themes') {
      unregisterTheme(id)
      undo.push(() => registerTheme(def as ThemeDefinition))
    } else {
      unregisterLocale(id)
      undo.push(() => registerLocale(def as LocaleDef))
    }
  }
  return undo
}

/** Thrown — and caught — inside `applySettingsSection`: the master's workspace set moved under the apply. */
const SCOPE_MOVED = Symbol('the master workspace set moved')

const sameIds = (a: ReadonlySet<string> | null, b: ReadonlySet<string>): boolean => a !== null && a.size === b.size && [...a].every((id) => b.has(id))

/**
 * THE SCOPE IS RE-READ AFTER EVERY AWAIT. The patches are computed once, scoped
 * by the master's workspace set as it is at that moment (`masterIds`) — and then
 * this function awaits, once per store. A `workspaces` apply can land in any of
 * those gaps (the executor's gate — no `settings` pull while `workspaces` is not
 * up to date — is looked at when the pull STARTS and once more before this apply
 * is called; nothing stops a `workspaces` pull from starting afterwards), and so
 * can a user's edit. The scoped patch would then keep the OLD entry of a workspace
 * that has just become the master's — it was "foreign, leave alone" when the
 * patch was cut — and the hash, taken over the NEW set, would call that old value
 * a local edit: pushed, over the SOT's. So: a set that differs after an await (or
 * that nobody can name any more) ends the apply — what was written is put back
 * the way a failure puts it back, and the answer is `busy`; the executor retries,
 * scoped by the set as it then is.
 *   Not the operation lock `applyWorkspacesSection` takes: that lock is for who
 * REWRITES the tab tree; held here it would answer `busy` for every rebuild and
 * switch, and still not see a user's edit. The re-read sees both, for the price
 * of one set comparison per store.
 */
async function applySettingsSection(incoming: unknown): Promise<ApplyOutcome> {
  if (incoming === null) return invalid('deleted', 'the settings section cannot be deleted')
  // An ordinal-3 payload (newtab `profiles`, P3e) — from a pull, the attach, keep-sot or a persisted stash
  // replayed by restoreLocal: all of them come through here. The hash returned below is rebuilt from the
  // stores, so it is the ordinal-4 shape's; the executor sees `pull-hash-mismatch` once and pushes it.
  const payload = upcastLegacySettings(incoming)
  if (!isWellFormedSection('settings', payload)) return invalid('malformed', 'malformed settings payload')
  // Unsettled → no master set: scoping by an empty one would DROP every scoped entry the payload carries.
  const masterIds = masterWorkspaceIds()
  if (masterIds === null) return BUSY
  const { patches, rejected } = applySettings(readSettingsSources(), payload as SettingsPayload, masterIds)
  if (rejected.length > 0) return invalid('rejected-settings', `rejected: ${rejected.join(', ')}`)

  const rendererBefore = useUISettingsStore.getState().terminalRenderer
  const written: Array<{ key: SettingsStorageKey; store: PersistedStore; old: Record<string, unknown> }> = []
  const reregister: Reregister[] = []
  try {
    for (const key of Object.keys(patches) as SettingsStorageKey[]) {
      const patch = patches[key] as Record<string, unknown>
      const store = SETTINGS_STORES[key]
      const before = store.getState() as Record<string, unknown>
      const old: Record<string, unknown> = {}
      for (const field of Object.keys(patch)) old[field] = before[field]
      written.push({ key, store, old })
      store.setState(patch)
      reregister.push(...unregisterDropped(key, before, patch))
      await rehydrate(store)
      publish(store)
      if (!sameIds(masterWorkspaceIds(), masterIds)) throw SCOPE_MOVED
    }
  } catch (err) {
    // A settings write is more than fields: registries, <html> theme / lang, the
    // i18n `t`. So the way back is the way in — re-register what was dropped, then
    // per store `setState(old)` → rehydrate → publish, so the store's own hooks put
    // the DOM and the translator back. If `setState(old)` itself throws (persist:
    // the storage is what is failing), memory IS restored (zustand sets before it
    // persists) but the rehydrate is skipped — it would read the NEW value back
    // from storage — and the hooks have not re-run. That is not swallowed: the
    // error that leaves here says which stores the rollback could not finish.
    const unfinished: string[] = []
    for (const put of reregister) put()
    for (const { key, store, old } of written.reverse()) {
      try {
        store.setState(old)
        await rehydrate(store)
        publish(store)
      } catch (rollbackErr) {
        unfinished.push(`${key}: ${messageOf(rollbackErr)}`)
      }
    }
    if (unfinished.length === 0 && err === SCOPE_MOVED) return BUSY
    if (unfinished.length === 0) throw err
    throw new Error(`${err === SCOPE_MOVED ? 'the master workspace set moved during the apply' : messageOf(err)} (rollback incomplete — ${unfinished.join('; ')})`, { cause: err })
  }
  // Terminals read the renderer on (re)connect only; the bump is what makes them reconnect.
  if (useUISettingsStore.getState().terminalRenderer !== rendererBefore) useUISettingsStore.getState().bumpTerminalSettingsVersion()
  // No await since the last re-read: the set is still the one this apply was scoped by — which is what the collector will hash.
  return { ok: true, hash: await hashSection(buildSettingsSection(readSettingsSources(), masterIds)) }
}

// === workspaces / tabs.<id> ===

// `commitTabWorld` — the two-store write both of these end in — lives in master-world.ts, next to the rule that
// says WHICH world a write is for; it is re-exported here because this file is where a caller looks for it.
export { commitTabWorld } from './master-world'

async function applyWorkspacesSection(payload: unknown): Promise<ApplyOutcome> {
  if (payload === null) return invalid('deleted', 'the workspaces section cannot be deleted')
  if (!isWellFormedSection('workspaces', payload)) return invalid('malformed', 'malformed workspaces payload')
  return withOperationLock<ApplyOutcome>(
    PROFILE_SYNC_LOCK_OWNER,
    async () => {
      const read = readMasterWorld()
      if (!read.settled) return BUSY
      const local = read.world
      const { next, removedWorkspaceIds } = applyWorkspaces({ workspaces: local.workspaces, activeWorkspaceId: local.activeWorkspaceId }, payload as WorkspacesPayload)
      // A removed workspace takes its tabs with it; left in the record they would turn into standalone tabs.
      const gone = new Set(local.workspaces.filter((w) => removedWorkspaceIds.includes(w.id)).flatMap((w) => w.tabs))
      const tabs: Record<string, Tab> = {}
      for (const [id, tab] of Object.entries(local.tabs)) {
        if (!gone.has(id)) tabs[id] = tab
      }
      const written = writeMasterWorld({ tabs, workspaces: next.workspaces, activeWorkspaceId: next.activeWorkspaceId }, () => {
        // What `removeWorkspace` does besides dropping the row. On the parked path too: the scoped settings are a
        // live store whatever is on screen.
        for (const id of removedWorkspaceIds) useWorkspaceSettingsStore.getState().clearWorkspace(id)
      })
      // Read back from wherever it landed (the hash is never copied from what was meant to be written), before
      // anything is awaited. Synchronous since the write, so "unsettled" here is for the type only.
      const after = readMasterWorld()
      if (written === 'unsettled' || !after.settled) return BUSY
      return { ok: true, hash: await hashSection(buildWorkspacesSection(after.world.workspaces)) }
    },
    () => ({ ok: false, reason: 'busy' }),
  )
}

const EMPTY_TABS: TabsPayload = { order: [], tabs: {} }

/**
 * `payload === null` — the SOT deleted this `tabs.<id>`. `workspaces` is the
 * authority on which workspaces exist (§4.6.3): a `tabs.<id>` normally dies
 * because its workspace did, and the `workspaces` apply removes that workspace
 * together with its tabs. So here: workspace still known locally → its tabs are
 * emptied (the section is gone, and an empty workspace is a defined state);
 * workspace unknown → nothing to do.
 */
async function applyTabsSection(key: ProfileSectionKey, payload: unknown): Promise<ApplyOutcome> {
  const workspaceId = workspaceIdOf(key)
  if (workspaceId === null) return invalid('unknown-section', `not a tabs section key: ${key}`)
  if (payload !== null && !isWellFormedSection('tabs', payload)) return invalid('malformed', 'malformed tabs payload')
  const incoming = payload === null ? EMPTY_TABS : (payload as TabsPayload)
  return withOperationLock<ApplyOutcome>(
    PROFILE_SYNC_LOCK_OWNER,
    async () => {
      const read = readMasterWorld()
      if (!read.settled) return BUSY
      const local = read.world
      const applied = applyTabs({ tabs: local.tabs, workspaces: local.workspaces }, workspaceId, incoming)
      if (applied.unrendered) return { ok: true, hash: null }

      // Session codes are host-scoped and every client talks to the same hosts,
      // so an arriving pane needs no reattach — unless its host is not known here.
      // (Hosts are live whichever world is on screen: a slave borrows them.)
      const knownHosts = new Set(Object.keys(useHostStore.getState().hosts))
      const tabs = { ...applied.next.tabs }
      for (const id of incoming.order) {
        const layout = markHostRemovedPanes(tabs[id].layout, knownHosts)
        if (layout !== tabs[id].layout) tabs[id] = { ...tabs[id], layout }
      }
      if (writeMasterWorld({ tabs, workspaces: applied.next.workspaces, activeWorkspaceId: local.activeWorkspaceId }) === 'unsettled') return BUSY

      // Read back from wherever it landed, before anything is awaited.
      const after = readMasterWorld()
      if (!after.settled) return BUSY
      const ws = after.world.workspaces.find((w) => w.id === workspaceId)
      return { ok: true, hash: ws ? await hashSection(buildTabsSection(ws, after.world.tabs)) : null }
    },
    () => ({ ok: false, reason: 'busy' }),
  )
}

// === Entry point ===

/**
 * Applies one section from the SOT to the stores. `payload === null` means the
 * section does not exist on the SOT (deleted): meaningful for `tabs.<id>` only —
 * `hosts` / `settings` / `workspaces` are never deleted, so `null` is `invalid`
 * there. Returns `invalid` or `busy` without having written anything; throws
 * (after rolling back what it wrote) only when a store write itself throws.
 * `busy` can come from `workspaces`, `tabs.<id>`, and a `hosts` apply that removes
 * a host.
 *
 * CALLER CONTRACT: when a `tabs.<id>` section is applied, the `hosts` section
 * must already be synced. An arriving pane whose host is unknown here is marked
 * `host-removed` — and that mark is a synced field that gets pushed back. Applied
 * while `hosts` is behind, it would brand the live pane of a host the other
 * device has just ADDED, on both devices. This layer does not defend against
 * that; the executor orders the pulls (no `tabs.*` before `hosts` is synced).
 *
 * Likewise, when `settings` is applied the `workspaces` section must already be
 * synced. A workspace-scoped entry is written for a master workspace only
 * (`masterWorkspaceIds()`); the entry of a workspace that has not arrived yet is
 * NOT written, the returned hash says so, and the section — now dirty — is
 * pushed back without it: the setting is deleted for every device. The executor
 * orders that too (no `settings` pull, and no push, before `workspaces` is synced).
 */
export async function applySectionToStores(key: ProfileSectionKey, payload: unknown | null, ctx: ApplyContext): Promise<ApplyOutcome> {
  switch (sectionKind(key)) {
    case 'hosts':
      return applyHostsSection(payload, ctx)
    case 'settings':
      return applySettingsSection(payload)
    case 'workspaces':
      return applyWorkspacesSection(payload)
    case 'tabs':
      return applyTabsSection(key, payload)
    default:
      return invalid('unknown-section', `unknown section key: ${String(key)}`)
  }
}
