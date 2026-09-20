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
import { useWorkspaceStore } from '../../features/workspace/store'
import { useHostSettingsStore } from '../../stores/useHostSettingsStore'
import { useHostStore } from '../../stores/useHostStore'
import type { HostConfig } from '../../stores/useHostStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { useLayoutStore } from '../../stores/useLayoutStore'
import { useNewTabLayoutStore } from '../../stores/useNewTabLayoutStore'
import { useNexHostStore } from '../../stores/useNexHostStore'
import { useNotificationSettingsStore } from '../../stores/useNotificationSettingsStore'
import { withOperationLock } from '../../stores/useRebuildStore'
import { useTabStore } from '../../stores/useTabStore'
import { useThemeStore } from '../../stores/useThemeStore'
import { useUISettingsStore } from '../../stores/useUISettingsStore'
import { useWorkspaceSettingsStore } from '../../stores/useWorkspaceSettingsStore'
import type { PaneLayout, Tab, Workspace } from '../../types/tab'
import { deleteHostCascade } from '../host-lifecycle'
import { registerLocale, unregisterLocale } from '../locale-registry'
import type { LocaleDef } from '../locale-registry'
import { registerTheme, unregisterTheme } from '../theme-registry'
import type { ThemeDefinition } from '../theme-registry'
import { applyHosts, applySettings, applyTabs, applyWorkspaces, deriveTabOrder, isWellFormedSection } from './applier'
import { hashSection } from './hash'
import { sectionKind, workspaceIdOf } from './projections'
import { buildHostsSection, buildSettingsSection, buildTabsSection, buildWorkspacesSection } from './sections'
import type { SettingsBuildInput } from './sections'
import type { HostsPayload, ProfileSectionKey, SettingsPayload, SettingsStorageKey, TabsPayload, WorkspacesPayload } from './types'

// === Contract ===

export type ApplyOutcome =
  /** Written. `hash` is rebuilt from the stores afterwards; `null` = the section does not exist locally (nothing was written). */
  | { ok: true; hash: string | null }
  /** The operation lock is held by someone else: retry later, never lock the section. */
  | { ok: false; reason: 'busy' }
  /** This payload must not be applied: the caller locks the section (`locked:invalid`). No store was written. */
  | { ok: false; reason: 'invalid'; detail: string }

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

const invalid = (detail: string): ApplyOutcome => ({ ok: false, reason: 'invalid', detail })

// === host-removed ===

/**
 * `layout` with every live `tmux-session` pane whose host is not in
 * `knownHostIds` marked `terminated: 'host-removed'`. Pure; returns the same
 * object when nothing changed. A pane that is already terminated keeps its reason.
 * (`markMissingHosts` in device-state/reattach.ts does this for a whole
 * `WorkspaceSnapshot`; an apply has one tab's layout.)
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
function hostsRefusal(local: Record<string, HostConfig>, incoming: HostsPayload, masterHostId: string): string | null {
  if (Object.keys(incoming.hosts).length === 0) return 'payload leaves no host'
  if (!Object.hasOwn(incoming.hosts, masterHostId)) return `payload removes the master host ${masterHostId}`
  if (!Object.hasOwn(local, masterHostId)) return null // nothing to compare with
  const mine = local[masterHostId]
  const theirs = incoming.hosts[masterHostId]
  // The credentials in hand are the ones that just fetched this payload, so they are known to work.
  const changed = [mine.ip !== theirs.ip && 'ip', mine.port !== theirs.port && 'port', tokenOf(mine) !== tokenOf(theirs) && 'token'].filter(Boolean)
  return changed.length > 0 ? `payload changes the master host's ${changed.join(', ')}` : null
}

/**
 * Replaces the host list. Additions, edits and reorders are one write. A host the
 * payload REMOVES goes through `deleteHostCascade(id, false)` — the app's own
 * "remove this host, keep its tabs" — so this device ends up exactly where it
 * would be had the user deleted that host here: sessions / agent / execution /
 * execution-list / nex / host-settings / peer / cwd state cleared, every pane on
 * it marked `terminated: 'host-removed'`, hostless execution panes pinned,
 * `runtime` dropped, focus moved off it. Its undo handle is kept only to roll back.
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
  if (payload === null) return invalid('the hosts section cannot be deleted')
  if (!isWellFormedSection('hosts', payload)) return invalid('malformed hosts payload')
  const incoming = payload as HostsPayload
  const refusal = hostsRefusal(useHostStore.getState().hosts, incoming, ctx.masterHostId)
  if (refusal !== null) return invalid(refusal)

  const store = asPersisted(useHostStore)
  // ROLLBACK, and what it is not. When a host-store write throws after the
  // cascade ran (persist: quota / SecurityError), the catch runs the cascade's
  // own undo handles and restores the host slice. That is not a transaction:
  //   - sessions, agent state, host settings, the marked panes: restored by the
  //     cascade's undo (what "Undo" does after a manual delete);
  //   - execution store: NOT restored. Its data comes back by itself — a pane
  //     that is still mounted re-runs summary → history → SSE when its host
  //     reappears (`useExecutionSubscription` depends on `hostPresent`). A held
  //     lease is gone locally and is re-acquired by the next send, exactly as
  //     after a manual undo (`useExecutionLease` drops it the moment the host goes);
  //   - nex-host and execution-list: NOT restored, and NOT re-fetched by
  //     themselves when a host returns under the same id — every `ensure` effect
  //     depends on `hostId` alone, and the reconnect watcher in useNexHostStore
  //     requires `byHost[hostId]` to still exist, which `clearHost` removed. So
  //     the rollback asks once, `ensure(id)`, for each restored host nex-host
  //     knew before; the execution-list watcher opens on its own once nex is
  //     ready. `ensure` must not add a new failure: a synchronous throw is
  //     reported as "rollback incomplete" on the ORIGINAL error, a later
  //     rejection is dropped (the nex-host entry records its own error);
  //   - `runtime[H]` IS restored verbatim, `connected` included, and that is
  //     true rather than stale ONLY because nothing is awaited between the
  //     staging write and the end of the rollback: the connection layer
  //     (`useMultiHostEventWs`) closes a host's WS from a React effect keyed on
  //     the host list, no effect runs inside a synchronous block, and by the time
  //     one can, the list is what it was — the WS never noticed. An `await` in
  //     that stretch would let the effect tear the connection down, and the
  //     restored `connected` would then be a lie until the layer reconnected.
  //     Hence `persist.rehydrate()` is NOT awaited inside the try (it is
  //     synchronous with this storage — premise (d) in the tests — and is awaited
  //     after the last fallible step, for the day it is not).
  const write = async (): Promise<ApplyOutcome> => {
    const state = useHostStore.getState()
    const old = { hosts: state.hosts, hostOrder: state.hostOrder, activeHostId: state.activeHostId, devHostId: state.devHostId, runtime: state.runtime }
    const { next, removedHostIds } = applyHosts(old, incoming)
    const undos: Array<() => void> = []
    const nexKnown = removedHostIds.filter((id) => Object.hasOwn(useNexHostStore.getState().byHost, id))
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
      if (undos.length > 0) {
        for (const id of nexKnown) {
          try {
            void useNexHostStore.getState().ensure(id).catch(() => {})
          } catch (ensureErr) {
            unfinished.push(`nex-host ensure(${id}): ${messageOf(ensureErr)}`)
          }
        }
      }
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

async function applySettingsSection(payload: unknown): Promise<ApplyOutcome> {
  if (payload === null) return invalid('the settings section cannot be deleted')
  if (!isWellFormedSection('settings', payload)) return invalid('malformed settings payload')
  const { patches, rejected } = applySettings(readSettingsSources(), payload as SettingsPayload)
  if (rejected.length > 0) return invalid(`rejected: ${rejected.join(', ')}`)

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
    if (unfinished.length === 0) throw err
    throw new Error(`${messageOf(err)} (rollback incomplete — ${unfinished.join('; ')})`, { cause: err })
  }
  // Terminals read the renderer on (re)connect only; the bump is what makes them reconnect.
  if (useUISettingsStore.getState().terminalRenderer !== rendererBefore) useUISettingsStore.getState().bumpTerminalSettingsVersion()
  return { ok: true, hash: await hashSection(buildSettingsSection(readSettingsSources())) }
}

// === workspaces / tabs.<id> ===

interface TabWorld {
  tabs: Record<string, Tab>
  workspaces: Workspace[]
  activeWorkspaceId: string | null
}

/**
 * Writes the tab store and the workspace store as one unit — either both hold the
 * new world or both hold the old one (`replaceTabSnapshot` is the precedent) —
 * keeping `useTabStore`'s four fields consistent: `tabOrder` re-derived,
 * `visitHistory` restricted to surviving tabs, the global `activeTabId` kept
 * while its tab survives, else the active workspace's, else `null`.
 * `afterWrite` runs inside the same try, and what it touches — the scoped
 * workspace settings a removal clears — is part of the same snapshot: if anything
 * throws, all THREE stores go back. (A clear that landed before a later one threw
 * would otherwise be lost for good while its workspace came back.)
 *
 * No rehydrate here, on purpose. Neither store has a `merge` or an
 * `onRehydrateStorage` (the tab store's `migrate` does not run on a same-version
 * read), so there is no hook to run — while a rehydrate rebuilds the WHOLE state
 * from JSON, giving every tab and workspace a new identity and re-rendering
 * every pane for a change to one of them. Synchronous, so nothing interleaves.
 */
function commitTabWorld(world: TabWorld, afterWrite?: () => void): void {
  const tabState = useTabStore.getState()
  const wsState = useWorkspaceStore.getState()
  const oldTab = { tabs: tabState.tabs, tabOrder: tabState.tabOrder, activeTabId: tabState.activeTabId, visitHistory: tabState.visitHistory }
  const oldWs = { workspaces: wsState.workspaces, activeWorkspaceId: wsState.activeWorkspaceId }
  const oldScoped = { workspaces: useWorkspaceSettingsStore.getState().workspaces }

  const exists = (id: string | null | undefined): id is string => typeof id === 'string' && Object.hasOwn(world.tabs, id)
  const activeWs = world.workspaces.find((w) => w.id === world.activeWorkspaceId)
  const fallback = activeWs?.activeTabId
  const activeTabId = exists(tabState.activeTabId) ? tabState.activeTabId : exists(fallback) ? fallback : null

  const tabStore = asPersisted(useTabStore)
  const wsStore = asPersisted(useWorkspaceStore)
  try {
    tabStore.setState({
      tabs: world.tabs,
      tabOrder: deriveTabOrder(world.workspaces, world.tabs, tabState.tabOrder),
      activeTabId,
      visitHistory: tabState.visitHistory.filter(exists),
    })
    wsStore.setState({ workspaces: world.workspaces, activeWorkspaceId: world.activeWorkspaceId })
    afterWrite?.()
  } catch (err) {
    restore(tabStore, oldTab)
    restore(wsStore, oldWs)
    if (useWorkspaceSettingsStore.getState().workspaces !== oldScoped.workspaces) restore(asPersisted(useWorkspaceSettingsStore), oldScoped)
    throw err
  }
}

async function applyWorkspacesSection(payload: unknown): Promise<ApplyOutcome> {
  if (payload === null) return invalid('the workspaces section cannot be deleted')
  if (!isWellFormedSection('workspaces', payload)) return invalid('malformed workspaces payload')
  return withOperationLock<ApplyOutcome>(
    PROFILE_SYNC_LOCK_OWNER,
    async () => {
      const wsState = useWorkspaceStore.getState()
      const { next, removedWorkspaceIds } = applyWorkspaces({ workspaces: wsState.workspaces, activeWorkspaceId: wsState.activeWorkspaceId }, payload as WorkspacesPayload)
      // A removed workspace takes its tabs with it; left in the record they would turn into standalone tabs.
      const gone = new Set(wsState.workspaces.filter((w) => removedWorkspaceIds.includes(w.id)).flatMap((w) => w.tabs))
      const tabs: Record<string, Tab> = {}
      for (const [id, tab] of Object.entries(useTabStore.getState().tabs)) {
        if (!gone.has(id)) tabs[id] = tab
      }
      commitTabWorld({ tabs, workspaces: next.workspaces, activeWorkspaceId: next.activeWorkspaceId }, () => {
        // What `removeWorkspace` does besides dropping the row.
        for (const id of removedWorkspaceIds) useWorkspaceSettingsStore.getState().clearWorkspace(id)
      })
      return { ok: true, hash: await hashSection(buildWorkspacesSection(useWorkspaceStore.getState().workspaces)) }
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
  if (workspaceId === null) return invalid(`not a tabs section key: ${key}`)
  if (payload !== null && !isWellFormedSection('tabs', payload)) return invalid('malformed tabs payload')
  const incoming = payload === null ? EMPTY_TABS : (payload as TabsPayload)
  return withOperationLock<ApplyOutcome>(
    PROFILE_SYNC_LOCK_OWNER,
    async () => {
      const wsState = useWorkspaceStore.getState()
      const applied = applyTabs({ tabs: useTabStore.getState().tabs, workspaces: wsState.workspaces }, workspaceId, incoming)
      if (applied.unrendered) return { ok: true, hash: null }

      // Session codes are host-scoped and every client talks to the same hosts,
      // so an arriving pane needs no reattach — unless its host is not known here.
      const knownHosts = new Set(Object.keys(useHostStore.getState().hosts))
      const tabs = { ...applied.next.tabs }
      for (const id of incoming.order) {
        const layout = markHostRemovedPanes(tabs[id].layout, knownHosts)
        if (layout !== tabs[id].layout) tabs[id] = { ...tabs[id], layout }
      }
      commitTabWorld({ tabs, workspaces: applied.next.workspaces, activeWorkspaceId: wsState.activeWorkspaceId })

      const ws = useWorkspaceStore.getState().workspaces.find((w) => w.id === workspaceId)
      return { ok: true, hash: ws ? await hashSection(buildTabsSection(ws, useTabStore.getState().tabs)) : null }
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
      return invalid(`unknown section key: ${String(key)}`)
  }
}
