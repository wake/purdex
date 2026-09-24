// spa/src/lib/profile/apply-to-stores.ts — lands one section pulled from the SOT
// in the app's stores (Profile Sync spec §4.7; P2b plan Task 7). The impure half
// of the applier: applier.ts computes `(local, incoming) → next`, this file reads
// the slices, writes `next`, and reports the hash the stores hold AFTERWARDS.
//
// How a write lands (the settings stores). A bare `store.setState(patch)` skips the invariants that
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
//
// HOST IDS ARRIVE AS WIRE IDS (host-sync-identity spec §6, §11). `tabs.*` and
// `settings` are translated wire → local through `wireResolverOf` over THIS
// device's host store as it is when they land. The host list itself is per device
// (host ownership spec §5.1): a `hosts` payload is never applied. The hash reported
// back is of the WIRE build (the builders translate), so it compares with the SOT's.
import { useHostSettingsStore } from '../../stores/useHostSettingsStore'
import { useHostStore } from '../../stores/useHostStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { useLayoutStore } from '../../stores/useLayoutStore'
import { useHostLookStore } from '../../stores/useHostLookStore'
import { useShownHostsStore } from '../../stores/useShownHostsStore'
import { useNewTabLayoutStore } from '../../stores/useNewTabLayoutStore'
import { useNotificationSettingsStore } from '../../stores/useNotificationSettingsStore'
import { withOperationLock } from '../../stores/useRebuildStore'
import { useThemeStore } from '../../stores/useThemeStore'
import { useUISettingsStore } from '../../stores/useUISettingsStore'
import { useWorkspaceSettingsStore } from '../../stores/useWorkspaceSettingsStore'
import type { Tab } from '../../types/tab'
import { scheduleHostReresolve } from '../host-reresolve'
import { registerLocale, unregisterLocale } from '../locale-registry'
import type { LocaleDef } from '../locale-registry'
import { registerTheme, unregisterTheme } from '../theme-registry'
import type { ThemeDefinition } from '../theme-registry'
import { applySettings, applyTabs, applyWorkspaces, isWellFormedSection, settingsFromWire, tabsFromWire, upcastLegacySettings, upcastLegacyTabs } from './applier'
import { identityOfSync } from './host-identity'
import { hashSection } from './hash'
import { masterWorkspaceIds, readMasterWorld, writeMasterWorld } from './master-world'
import { sectionKind, workspaceIdOf } from './projections'
import { buildSettingsSection, buildTabsSection, buildWorkspacesSection, hostResolverSignature, wireResolverOf } from './sections'
import type { SettingsBuildInput } from './sections'
import type { ProfileSectionKey, SettingsPayload, SettingsStorageKey, TabsPayload, WorkspacesPayload } from './types'

// === Contract ===

export type ApplyOutcome =
  /** Written. `hash` is rebuilt from the stores afterwards; `null` = the section does not exist locally (nothing was written).
   *  `payload` is the section `hash` was computed from (absent with a `null` hash): when it differs from the SOT's,
   *  the executor pushes it back without waiting for the collector to report it (#1369).
   *  `rewrite`: the hash differs from the payload's, and ONLY by a designed write — pushed back once, not a problem:
   *    'device-local-tabs' (tabs.*): an ordinal-2 payload's interface-only tabs, left out by `upcastLegacyTabs`
   *    (tabs-local-only §3.5) — the rebuild equals the upcast payload. */
  | { ok: true; hash: string | null; payload?: unknown; rewrite?: 'device-local-tabs' }
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
 *   deleted              the host deleted `settings` / `workspaces` — sections this device cannot be without
 *   malformed            not a payload the builders could have produced (`isWellFormedSection`)
 *   rejected-settings    `settings` entries this build refuses (`applySettings`' `rejected`)
 *   unknown-section      a key this build does not apply as a section — `hosts` included (host ownership H3)
 *   host-identity-conflict    two local hosts claim one daemon, so which one an id means is ambiguous (§11.4)
 * The `hosts` apply's own five (`no-host`, `removes-master-host`, `changes-master-host`, `duplicate-host-identity`,
 * `duplicate-host-alias`) went with it (host ownership H3a-3); an older window publishing one is read as `null`.
 */
export type InvalidReason =
  | 'deleted'
  | 'malformed'
  | 'rejected-settings'
  | 'unknown-section'
  | 'host-identity-conflict'

export const INVALID_REASONS: readonly InvalidReason[] = ['deleted', 'malformed', 'rejected-settings', 'unknown-section', 'host-identity-conflict']

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
  'purdex-host-looks': asPersisted(useHostLookStore),
  'purdex-shown-hosts': asPersisted(useShownHostsStore),
}

/** The nine settings stores' current states, by storage key: the input of `buildSettingsSection` and `applySettings`. */
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

const messageOf = (err: unknown): string => (err instanceof Error ? err.message : String(err))

const invalid = (code: InvalidReason, detail: string): ApplyOutcome => ({ ok: false, reason: 'invalid', code, detail })

const BUSY: ApplyOutcome = { ok: false, reason: 'busy' }

/**
 * An ok outcome for a section rebuilt from the stores: its hash, and the very payload that hash was taken of. A
 * snapshot: the stores can move before the caller uses it (the hash await, the lock release's observer), so whether
 * it is still what they hold is the executor's check, made where it stashes the payload (#1369 critic).
 */
const rebuilt = async (payload: unknown): Promise<Extract<ApplyOutcome, { ok: true }>> => ({ ok: true, hash: await hashSection(payload), payload })

const IDENTITY_CONFLICT = (): ApplyOutcome => invalid('host-identity-conflict', 'two local hosts claim one daemon: which one a host id means is ambiguous')

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
/** Thrown — and caught — inside `applySettingsSection`: what the wire → local translation read moved under the apply. */
const HOSTS_MOVED = Symbol('the host identity moved')


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
  const upcast = upcastLegacySettings(incoming)
  if (!isWellFormedSection('settings', upcast)) return invalid('malformed', 'malformed settings payload')
  // wire → local, through the hosts as they are now (the executor pulls `settings` only once `hosts` is up to date).
  const hostState = useHostStore.getState()
  const hostsSeen = hostResolverSignature(hostState)
  const resolve = wireResolverOf(hostState)
  if (resolve === null) return IDENTITY_CONFLICT()
  const payload = settingsFromWire(upcast as SettingsPayload, resolve)
  // Unsettled → no master set: scoping by an empty one would DROP every scoped entry the payload carries.
  const masterIds = masterWorkspaceIds()
  if (masterIds === null) return BUSY
  const { patches, rejected } = applySettings(readSettingsSources(), payload, masterIds)
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
      // Same for the hosts (R1, PR #1365): a daemonId learned, a host added / removed, a conflict appearing — the
      // part written so far was resolved through a resolver that no longer holds.
      if (hostResolverSignature(useHostStore.getState()) !== hostsSeen) throw HOSTS_MOVED
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
    const moved = err === SCOPE_MOVED ? 'the master workspace set moved during the apply' : err === HOSTS_MOVED ? 'the host identity moved during the apply' : null
    if (unfinished.length === 0 && moved !== null) return BUSY
    if (unfinished.length === 0) throw err
    throw new Error(`${moved ?? messageOf(err)} (rollback incomplete — ${unfinished.join('; ')})`, { cause: err })
  }
  // Terminals read the renderer on (re)connect only; the bump is what makes them reconnect.
  if (useUISettingsStore.getState().terminalRenderer !== rendererBefore) useUISettingsStore.getState().bumpTerminalSettingsVersion()
  // No await since the last re-read: the set is still the one this apply was scoped by — which is what the collector will hash.
  // No await since the last re-read either: the identity is the one this apply resolved through (checked above), and
  // with no patch at all nothing was awaited — the resolve and this hash read the same host store.
  const identity = identityOfSync(useHostStore.getState().hosts)
  return rebuilt(buildSettingsSection(readSettingsSources(), masterIds, identity))
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
      return rebuilt(buildWorkspacesSection(after.world.workspaces))
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
  return withOperationLock<ApplyOutcome>(
    PROFILE_SYNC_LOCK_OWNER,
    async () => {
      // wire → local through the hosts as they are NOW — under the lock, in the same synchronous stretch as the
      // write and the hash below (nothing is awaited in between), so no host-store change can fall between the
      // resolve and what is written (the R1 race, PR #1365). The executor pulls a `tabs.*` only once `hosts` is up to date.
      const resolve = wireResolverOf(useHostStore.getState())
      if (resolve === null) return IDENTITY_CONFLICT()
      // An ordinal-2 payload (an older build's interface-only tabs in it) is made canonical first: those tabs never
      // arrive on a device that lacks them, and one this device has is device-local here and kept by `applyTabs`.
      // The upcast is judged on the WIRE payload — the one whose hash the SOT holds and the rebuild is compared to.
      const canonical = payload === null ? null : upcastLegacyTabs(payload as TabsPayload)
      const incoming = canonical === null ? EMPTY_TABS : tabsFromWire(canonical, resolve)
      const read = readMasterWorld()
      if (!read.settled) return BUSY
      const local = read.world
      const applied = applyTabs({ tabs: local.tabs, workspaces: local.workspaces }, workspaceId, incoming)
      if (applied.unrendered) return { ok: true, hash: null }

      // A pane whose host this device does not have is kept exactly as it arrived (host ownership spec §3.2): its
      // wire id is stored verbatim and rendered as "no host here" on this device only. Nothing is marked — a mark is a
      // synced field, and one device lacking a host must not brand that host's panes on every device.
      if (writeMasterWorld({ tabs: applied.next.tabs, workspaces: applied.next.workspaces, activeWorkspaceId: local.activeWorkspaceId }) === 'unsettled') return BUSY

      // Read back from wherever it landed, before anything is awaited.
      const after = readMasterWorld()
      if (!after.settled) return BUSY
      const ws = after.world.workspaces.find((w) => w.id === workspaceId)
      if (!ws) return { ok: true, hash: null }
      // The identity the resolve above used: nothing was awaited since.
      const identity = identityOfSync(useHostStore.getState().hosts)
      const outcome = await rebuilt(buildTabsSection(ws, after.world.tabs, identity))
      // The upcast removed something and the stores hold exactly the upcast payload: the difference from the SOT's
      // hash is the migration (pushed once), not "the stores did not keep what arrived".
      const upcastOnly = canonical !== null && canonical !== payload && outcome.hash === (await hashSection(canonical))
      return upcastOnly ? { ...outcome, rewrite: 'device-local-tabs' } : outcome
    },
    () => ({ ok: false, reason: 'busy' }),
  )
}

// === Entry point ===

/**
 * Applies one section from the SOT to the stores. `payload === null` means the
 * section does not exist on the SOT (deleted): meaningful for `tabs.<id>` only —
 * `settings` / `workspaces` are never deleted, so `null` is `invalid` there.
 * Returns `invalid` or `busy` without having written anything; throws (after
 * rolling back what it wrote) only when a store write itself throws. `busy` can
 * come from `workspaces`, `tabs.<id>` and `settings`.
 *
 * `hosts` is not synced (host ownership spec §5.1): whatever its payload, the
 * answer is `invalid` / `unknown-section`, and nothing is written.
 *
 * An arriving pane whose host is unknown here is kept verbatim, unmarked (host
 * ownership spec §3.2): applying a `tabs.<id>` writes nothing synced about it.
 *
 * When `settings` is applied the `workspaces` section must already be
 * synced. A workspace-scoped entry is written for a master workspace only
 * (`masterWorkspaceIds()`); the entry of a workspace that has not arrived yet is
 * NOT written, the returned hash says so, and the section — now dirty — is
 * pushed back without it: the setting is deleted for every device. The executor
 * orders that too (no `settings` pull, and no push, before `workspaces` is synced).
 */
export async function applySectionToStores(key: ProfileSectionKey, payload: unknown | null, _ctx: ApplyContext): Promise<ApplyOutcome> {
  try {
    switch (sectionKind(key)) {
      case 'hosts':
        // Host ownership H3 (spec §5.1): the host list is per device. The kind is still known (older clients write
        // it), but no payload of it is ever applied — not even looked at.
        return invalid('unknown-section', 'hosts is not synced')
      case 'settings':
        return await applySettingsSection(payload)
      case 'workspaces':
        return await applyWorkspacesSection(payload)
      case 'tabs':
        return await applyTabsSection(key, payload)
      default:
        return invalid('unknown-section', `unknown section key: ${String(key)}`)
    }
  } finally {
    // At the SETTLEMENT boundary, whatever the outcome — a throw included (host ownership plan §0.2): a rollback
    // (`setState(old)`) may have put back a wire id the re-resolve pass had already resolved, and an applied payload
    // may carry one this device can resolve. The pass is idempotent and writes nothing when nothing moves. Only
    // SCHEDULED here — it runs in a microtask and cannot throw into this `finally`, so the outcome or error the
    // caller gets is always the apply's own.
    scheduleHostReresolve()
  }
}
