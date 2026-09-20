// spa/src/lib/profile/collector.ts — the collector's impure half: watches the
// app's stores and turns their changes into per-section `(key, hash, payload)`
// reports (Profile Sync spec §4.2–§4.4; P2b plan Task 8).
//
// Two layers, and only the second one is load-bearing:
//   1. the store subscribers hand-diff the PROJECTED slices and arm a per-section
//      trailing debounce — a scheduling optimisation, allowed to over-schedule;
//   2. when a timer fires the section is rebuilt from the stores, hashed, and
//      reported only if the hash differs from the last one reported.
// Nothing here talks to a daemon: the executor (Task 9) owns what a report means.
import { useHostStore } from '../../stores/useHostStore'
import { useWorkspaceStore } from '../../features/workspace/store'
import { useTabStore } from '../../stores/useTabStore'
import { useUISettingsStore } from '../../stores/useUISettingsStore'
import { useThemeStore } from '../../stores/useThemeStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { useNotificationSettingsStore } from '../../stores/useNotificationSettingsStore'
import { useWorkspaceSettingsStore } from '../../stores/useWorkspaceSettingsStore'
import { useHostSettingsStore } from '../../stores/useHostSettingsStore'
import { useNewTabLayoutStore } from '../../stores/useNewTabLayoutStore'
import { useLayoutStore } from '../../stores/useLayoutStore'
import type { Workspace } from '../../types/tab'
import { hashSection } from './hash'
import { masterWorkspaceIds } from './master-world'
import { PROJECTIONS, tabsSectionKey, workspaceIdOf } from './projections'
import {
  buildHostsSection,
  buildSettingsSection,
  buildTabsSection,
  buildWorkspacesSection,
  unsyncableWorkspaceIds,
  type SettingsBuildInput,
} from './sections'
import type { ProfileSectionKey, SettingsStorageKey } from './types'

// === Public surface ===

/** One section's current content. `hash` and `payload` are both `null` when the section no longer exists locally. */
export interface SectionReport {
  key: ProfileSectionKey
  hash: string | null
  payload: unknown | null
}

export interface CollectorOptions {
  onSection: (r: SectionReport) => void
  /** `invalid-workspace-id` (detail: the id), `standalone-tabs` (detail: the count), `build-failed` (detail: key + message). */
  onProblem?: (p: { kind: string; detail: string }) => void
  /** Per-section trailing debounce; default 500. */
  debounceMs?: number
}

export interface Collector {
  /** Builds and reports EVERY section now, changed or not, and cancels all pending timers. Resolves once all reports are out. */
  primeAll(): Promise<void>
  /** Cancels every timer and subscription; a hash still in flight never reports. */
  stop(): void
}

// === The eight settings stores ===

interface SettingsStore {
  getState: () => object
  subscribe: (listener: (next: object, prev: object) => void) => () => void
  persist: { rehydrate: () => void | Promise<void> }
}

/** Keyed by `SettingsStorageKey`, so a ninth projected store is a compile error here until it is added. */
const SETTINGS_STORES: Record<SettingsStorageKey, SettingsStore> = {
  'purdex-ui-settings': useUISettingsStore,
  'purdex-themes': useThemeStore,
  'purdex-i18n': useI18nStore,
  'purdex-notification-settings': useNotificationSettingsStore,
  'purdex-workspace-settings': useWorkspaceSettingsStore,
  'purdex-host-settings': useHostSettingsStore,
  'purdex-newtab-layout': useNewTabLayoutStore,
  'purdex-layout': useLayoutStore,
}

const SETTINGS_KEYS = Object.keys(SETTINGS_STORES) as SettingsStorageKey[]

/** The fields `PROJECTIONS.settings` lists for one store — read from the projection, never restated. */
function projectedSettingsFields(storageKey: SettingsStorageKey): string[] {
  const prefix = `${storageKey}.`
  return PROJECTIONS.settings.filter((p) => p.startsWith(prefix)).map((p) => p.slice(prefix.length).split('.')[0])
}

/** The per-workspace fields `PROJECTIONS.workspaces` lists (`name`, `icon`, …). */
const WORKSPACE_FIELDS = PROJECTIONS.workspaces
  .filter((p) => p.startsWith('workspaces.*.'))
  .map((p) => p.slice('workspaces.*.'.length).split('.')[0]) as (keyof Workspace)[]

/** ALL eight, always: a store left out reads as "not sent", and the receiving side would stay dirty forever. */
function allSettings(): SettingsBuildInput {
  const input: SettingsBuildInput = {}
  for (const key of SETTINGS_KEYS) input[key] = SETTINGS_STORES[key].getState()
  return input
}

function masterSetKey(): string {
  return [...masterWorkspaceIds()].sort().join('\n')
}

/** First occurrence wins — the same rule `buildWorkspacesSection` applies. */
function byId(workspaces: readonly Workspace[]): Map<string, Workspace> {
  const map = new Map<string, Workspace>()
  for (const ws of workspaces) if (!map.has(ws.id)) map.set(ws.id, ws)
  return map
}

// === Collector ===

/** A timer slot: one per section, plus one for the standalone-tab census. */
const STANDALONE = '#standalone'
type Slot = ProfileSectionKey | typeof STANDALONE

const ABSENT = Symbol('absent')

export function startCollector(opts: CollectorOptions): Collector {
  const debounceMs = opts.debounceMs ?? 500
  const timers = new Map<Slot, ReturnType<typeof setTimeout>>()
  /** Last hash reported per section; `null` = reported as vanished. */
  const lastHash = new Map<ProfileSectionKey, string | null>()
  /** Per-section sequence: a hash that finishes after a newer run began is stale. */
  const seq = new Map<ProfileSectionKey, number>()
  const reportedProblems = new Set<string>()
  let standaloneCount = 0
  let stopped = false
  /**
   * The master workspace set `settings` was last scheduled for. The section
   * carries workspace-scoped entries for that set only, so it depends on it as
   * much as on the settings stores: a workspace that appears or goes changes the
   * payload without any settings store moving. (Syncable ids: no `\n` in them.)
   */
  let masterSet = masterSetKey()

  function problemOnce(kind: string, detail: string): void {
    const id = `${kind}\n${detail}`
    if (reportedProblems.has(id)) return
    reportedProblems.add(id)
    opts.onProblem?.({ kind, detail })
  }

  /** `tabs.<id>`, or `null` (reported once per id) for an id the daemon's key charset rejects. */
  function tabsKey(workspaceId: string): ProfileSectionKey | null {
    try {
      return tabsSectionKey(workspaceId)
    } catch {
      problemOnce('invalid-workspace-id', workspaceId)
      return null
    }
  }

  /** Never `buildProfileDocument`: it throws as a whole on one bad workspace id. Each section is built alone. */
  function build(key: ProfileSectionKey): unknown | typeof ABSENT {
    if (key === 'hosts') return buildHostsSection(useHostStore.getState())
    if (key === 'settings') return buildSettingsSection(allSettings(), masterWorkspaceIds())
    if (key === 'workspaces') {
      const { workspaces } = useWorkspaceStore.getState()
      // Left out of the payload by the builder (device-local); said once per id, like their `tabs.*`.
      for (const id of unsyncableWorkspaceIds(workspaces)) problemOnce('invalid-workspace-id', id)
      return buildWorkspacesSection(workspaces)
    }
    const id = workspaceIdOf(key)
    const ws = id === null ? undefined : byId(useWorkspaceStore.getState().workspaces).get(id)
    return ws === undefined ? ABSENT : buildTabsSection(ws, useTabStore.getState().tabs)
  }

  async function run(key: ProfileSectionKey, force: boolean): Promise<void> {
    if (stopped) return
    const mine = (seq.get(key) ?? 0) + 1
    seq.set(key, mine)
    let payload: unknown | typeof ABSENT
    let hash: string | null = null
    try {
      // Payload first, then the hash OF THAT payload: the stores may move during the await.
      payload = build(key)
      if (payload !== ABSENT) hash = await hashSection(payload)
    } catch (err) {
      // Only build/hash failures land here — never an exception thrown by `onSection`.
      if (!stopped) problemOnce('build-failed', `${key}: ${err instanceof Error ? err.message : String(err)}`)
      return
    }
    if (stopped || seq.get(key) !== mine) return
    if (hash === null) {
      if (lastHash.get(key) === null) return // already reported as vanished
      lastHash.set(key, null)
      opts.onSection({ key, hash: null, payload: null })
      return
    }
    if (!force && lastHash.get(key) === hash) return
    lastHash.set(key, hash)
    opts.onSection({ key, hash, payload })
  }

  function censusStandalone(): void {
    const owned = new Set(useWorkspaceStore.getState().workspaces.flatMap((ws) => ws.tabs))
    const count = Object.keys(useTabStore.getState().tabs).filter((id) => !owned.has(id)).length
    if (count === standaloneCount) return
    standaloneCount = count
    opts.onProblem?.({ kind: 'standalone-tabs', detail: String(count) })
  }

  function schedule(slot: Slot): void {
    if (stopped) return
    const pending = timers.get(slot)
    if (pending !== undefined) clearTimeout(pending)
    timers.set(
      slot,
      setTimeout(() => {
        timers.delete(slot)
        if (slot === STANDALONE) censusStandalone()
        else void run(slot, false)
      }, debounceMs),
    )
  }

  function scheduleTabs(workspaceId: string): void {
    const key = tabsKey(workspaceId)
    if (key !== null) schedule(key)
  }

  function clearTimers(): void {
    for (const t of timers.values()) clearTimeout(t)
    timers.clear()
  }

  const unsubscribers: (() => void)[] = [
    useHostStore.subscribe((next, prev) => {
      if (next.hosts !== prev.hosts || next.hostOrder !== prev.hostOrder) schedule('hosts')
    }),

    useWorkspaceStore.subscribe((next, prev) => {
      if (next.workspaces === prev.workspaces) return
      const master = masterSetKey()
      if (master !== masterSet) {
        masterSet = master
        schedule('settings')
      }
      const now = byId(next.workspaces)
      const before = byId(prev.workspaces)
      let listChanged = now.size !== before.size || [...now.keys()].some((id, i) => id !== [...before.keys()][i])
      let membershipChanged = false
      for (const [id, ws] of now) {
        const old = before.get(id)
        if (old === undefined || old.tabs !== ws.tabs) {
          membershipChanged = true
          scheduleTabs(id)
        }
        if (old !== undefined && WORKSPACE_FIELDS.some((f) => old[f] !== ws[f])) listChanged = true
      }
      for (const id of before.keys()) {
        if (!now.has(id)) {
          membershipChanged = true
          scheduleTabs(id)
        }
      }
      if (listChanged) schedule('workspaces')
      if (membershipChanged) schedule(STANDALONE)
    }),

    useTabStore.subscribe((next, prev) => {
      if (next.tabs === prev.tabs) return
      const changed = new Set<string>()
      for (const id of Object.keys(next.tabs)) if (next.tabs[id] !== prev.tabs[id]) changed.add(id)
      for (const id of Object.keys(prev.tabs)) if (!Object.hasOwn(next.tabs, id)) changed.add(id)
      if (changed.size === 0) return
      for (const ws of byId(useWorkspaceStore.getState().workspaces).values()) {
        if (ws.tabs.some((id) => changed.has(id))) scheduleTabs(ws.id)
      }
      schedule(STANDALONE)
    }),

    ...SETTINGS_KEYS.map((storageKey) => {
      const fields = projectedSettingsFields(storageKey)
      return SETTINGS_STORES[storageKey].subscribe((next, prev) => {
        const n = next as Record<string, unknown>
        const p = prev as Record<string, unknown>
        if (fields.some((f) => n[f] !== p[f])) schedule('settings')
      })
    }),
  ]

  return {
    async primeAll() {
      if (stopped) return
      clearTimers()
      const live = new Set<ProfileSectionKey>(['hosts', 'settings', 'workspaces'])
      for (const id of byId(useWorkspaceStore.getState().workspaces).keys()) {
        const key = tabsKey(id)
        if (key !== null) live.add(key)
      }
      // A section reported earlier and gone now is reported as vanished (once: `run` records the null).
      for (const [key, hash] of lastHash) if (hash !== null) live.add(key)
      censusStandalone()
      await Promise.all([...live].map((key) => run(key, true)))
    },
    stop() {
      stopped = true
      clearTimers()
      for (const unsubscribe of unsubscribers) unsubscribe()
    },
  }
}

// === Cross-window rehydrate for stores syncManager does not cover ===

/**
 * The projected settings stores that never call `syncManager.register`, so an
 * edit in another window does not reach this one's memory. `syncManager`'s
 * registry is private to its closure (`storage/sync.ts` exposes register /
 * notify / destroy only), so this cannot be derived at runtime; collector.test.ts
 * records the real `register` calls and fails the day this list stops matching.
 *
 * EMPTY today. Its one member was `purdex-editor-settings`, which left the
 * profile (device-local by its own header — see PROJECTIONS.settings), and every
 * store still projected registers. The list and `watchUnsyncedStores` stay for
 * the day a projected store does not: with an empty list the watcher is a no-op.
 */
export const UNSYNCED_SETTINGS_KEYS: readonly SettingsStorageKey[] = []

/**
 * Listens to the native `storage` event (other windows only, by definition) and
 * rehydrates an unsynced store when its key changed. Runs in EVERY window,
 * leader or follower, independently of any collector. Returns the unlisten.
 */
export function watchUnsyncedStores(): () => void {
  const onStorage = (event: StorageEvent): void => {
    const key = UNSYNCED_SETTINGS_KEYS.find((k) => k === event.key)
    if (key !== undefined) void SETTINGS_STORES[key].persist.rehydrate()
  }
  window.addEventListener('storage', onStorage)
  return () => window.removeEventListener('storage', onStorage)
}
