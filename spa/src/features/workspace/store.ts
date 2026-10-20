import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { createWorkspace, type Workspace, type IconWeight } from '../../types/tab'
import { fencedWorldStorage, registerFencedStore, STORAGE_KEYS, syncManager } from '../../lib/storage'
import { useTabStore } from '../../stores/useTabStore'
import { useHistoryStore } from '../../stores/useHistoryStore'
import { useWorkspaceSettingsStore } from '../../stores/useWorkspaceSettingsStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { WATCH_WORKSPACE_ID, isWatchTabId } from '../../lib/watch/ids'

export { WATCH_WORKSPACE_ID, isWatchTabId } from '../../lib/watch/ids'

/**
 * The id of the `Unsorted` workspace — where a tab goes when there is no workspace to put it in (Profile Sync
 * spec §4.3: every tab belongs to exactly one workspace). A CONSTANT, not `generateId()`: two windows that
 * create it at once, or two machines that each do, make the SAME workspace, so a cross-window rehydrate and a
 * profile sync converge on one `Unsorted` instead of two. It cannot collide with a generated id (those are
 * exactly 6 chars of base36; this is 8) and it is a legal `tabs.<id>` section key (`[A-Za-z0-9_-]{1,64}`).
 * Nor can it take over a workspace the user already has: every workspace id there is — made here, imported,
 * or synced from another machine — came out of `generateId()`, whose output is always `^[0-9a-z]{6}$`
 * (pinned by lib/id.test.ts), so no existing workspace is called `unsorted` unless it IS this one.
 * The NAME is whatever `workspace.unsorted` said in the language of whoever created it first; an existing
 * one is found by this id and never renamed.
 */
export const UNSORTED_WORKSPACE_ID = 'unsorted'

/**
 * The last active workspace other than `watch`, kept in memory only: where a tab goes when its target would be the
 * watcher area (which holds no tab but its own), and where Home / prev / next shortcuts are relative to from inside the
 * area. Not persisted: a restart with `watch` active falls back to the first non-watch workspace.
 */
let returnWorkspaceId: string | null = null

/** The workspace to return to from the watcher area: the remembered one if it still exists, else the first non-watch one. */
export function getReturnWorkspaceId(): string | null {
  const { workspaces } = useWorkspaceStore.getState()
  const remembered = workspaces.find((ws) => ws.id === returnWorkspaceId && ws.id !== WATCH_WORKSPACE_ID)
  return remembered?.id ?? workspaces.find((ws) => ws.id !== WATCH_WORKSPACE_ID)?.id ?? null
}

/** The workspaces the user sees and picks among: everything but the watcher area. */
export const visibleWorkspaces = (workspaces: readonly Workspace[]): Workspace[] =>
  workspaces.filter((ws) => ws.id !== WATCH_WORKSPACE_ID)

/** Where the watcher area's back button and ⌘W lead (journal v5 §3.7): a workspace and the tab that was on screen in it. */
export interface WatchReturnTarget { workspaceId: string; tabId: string | null }

/**
 * This window's way back, taken when its `activeWorkspaceId` goes from another workspace to `watch` (the subscription
 * at the end of this file) — by `openWatchArea` here or by another window's write synced in; moving inside the area
 * keeps it; `reset` clears it. A target whose workspace is gone (another world has other ids) is no target on leaving
 * (`leaveWatchArea`). In memory only, like `returnWorkspaceId`: after a restart with the area shown there is none.
 */
let watchReturnTarget: WatchReturnTarget | null = null

export const getWatchReturnTarget = (): WatchReturnTarget | null => watchReturnTarget

export const __resetReturnWorkspaceForTest = (): void => { returnWorkspaceId = null; watchReturnTarget = null }

interface WorkspaceState {
  workspaces: Workspace[]
  activeWorkspaceId: string | null
  /** Whose workspaces these are, and the epoch of the switch that put them here — the twin of the two fields
   *  on `useTabStore` (see there). Persisted, device-local, written only by `commitTabWorld`. */
  worldId: string
  worldEpoch: number

  addWorkspace: (name: string, opts?: { icon?: string }) => Workspace
  removeWorkspace: (wsId: string, opts?: { keepSettings?: boolean }) => void
  setActiveWorkspace: (wsId: string | null) => void
  addTabToWorkspace: (wsId: string, tabId: string) => void
  removeTabFromWorkspace: (wsId: string, tabId: string) => void
  setWorkspaceActiveTab: (wsId: string, tabId: string) => void
  reorderWorkspaceTabs: (wsId: string, tabIds: string[]) => void
  reorderWorkspaces: (orderedIds: string[]) => void
  findWorkspaceByTab: (tabId: string) => Workspace | null
  /** The `Unsorted` workspace (`UNSORTED_WORKSPACE_ID`): the existing one as it is, else a new one, appended. */
  ensureUnsortedWorkspace: () => Workspace
  /** The watcher area's workspace (`WATCH_WORKSPACE_ID`): the existing one as it is, else a new one, appended last. Never activates it. */
  ensureWatchWorkspace: () => Workspace
  /** No `workspaceId` → the active workspace → else the first → else a new `Unsorted`. Never a silent no-op. */
  insertTab: (tabId: string, workspaceId?: string, afterTabId?: string | null) => void
  /** `scope`: the tabs the bar shows (the task-group view's range, lib/task-groups/range.ts); when it holds the tab, the tab after the close comes from it, not from the tab's workspace. */
  closeTabInWorkspace: (tabId: string, opts?: { skipHistory?: boolean; scope?: readonly string[] }) => void
  renameWorkspace: (wsId: string, name: string) => void
  setWorkspaceIcon: (wsId: string, icon: string) => void
  setWorkspaceIconWeight: (wsId: string, weight: IconWeight) => void
  importWorkspace: (ws: Workspace) => void
  setModuleConfig: (wsId: string, moduleId: string, key: string, value: unknown) => void
  reset: () => void
}

function createDefaultState(): Pick<WorkspaceState, 'workspaces' | 'activeWorkspaceId' | 'worldId' | 'worldEpoch'> {
  return { workspaces: [], activeWorkspaceId: null, worldId: 'master', worldEpoch: 0 }
}

export const useWorkspaceStore = create<WorkspaceState>()(
  persist(
    (set, get) => ({
      ...createDefaultState(),

      addWorkspace: (name, opts) => {
        const ws = createWorkspace(name, opts?.icon)
        set((state) => ({
          workspaces: [...state.workspaces, ws],
          // Auto-activate if this is the first workspace
          activeWorkspaceId: state.activeWorkspaceId ?? ws.id,
        }))
        return ws
      },

      removeWorkspace: (wsId, opts) => {
        // The watcher area is never removed by the user's workspace actions; only a sync apply or a profile switch
        // (which do not come through here) can take it away, and `startWatchArea` leaves it removed until the next open.
        if (wsId === WATCH_WORKSPACE_ID) return
        if (!get().workspaces.some((ws) => ws.id === wsId)) return
        // Tear-off / merge paths reuse the same `workspace.id` on the receiving
        // window; in those cases the caller passes `keepSettings: true` so
        // workspace-scoped settings survive the move.
        if (!opts?.keepSettings) {
          useWorkspaceSettingsStore.getState().clearWorkspace(wsId)
        }
        // Every tab belongs to exactly one workspace: the tabs of the removed workspace that still EXIST (the
        // user kept them, or they were locked) move to the next workspace — the previous one when the last is
        // removed, a new `Unsorted` when none is left. Tear-off / merge delete their tabs first: nothing moves.
        const liveTabs = useTabStore.getState().tabs
        set((state) => {
          const index = state.workspaces.findIndex((ws) => ws.id === wsId)
          const survivors = state.workspaces[index].tabs.filter((id) => Object.hasOwn(liveTabs, id))
          let remaining = state.workspaces.filter((ws) => ws.id !== wsId)
          // `watch` is never an heir: it holds no tab but its own.
          const heirs = remaining.filter((ws) => ws.id !== WATCH_WORKSPACE_ID)
          let heirId: string | null = null
          if (survivors.length > 0) {
            const heir: Workspace = heirs[Math.min(index, heirs.length - 1)]
              ?? { ...createWorkspace(useI18nStore.getState().t('workspace.unsorted')), id: UNSORTED_WORKSPACE_ID }
            heirId = heir.id
            const merged = { ...heir, tabs: [...heir.tabs, ...survivors], activeTabId: heir.activeTabId ?? survivors[0] }
            remaining = heirs.length === 0 ? [...remaining, merged] : remaining.map((ws) => (ws.id === heir.id ? merged : ws))
          }
          const activeId = state.activeWorkspaceId === wsId || state.activeWorkspaceId === null
            ? (heirId ?? heirs[0]?.id ?? null)
            : state.activeWorkspaceId
          return { workspaces: remaining, activeWorkspaceId: activeId }
        })
      },

      setActiveWorkspace: (wsId) =>
        set({ activeWorkspaceId: wsId }),

      addTabToWorkspace: (wsId, tabId) =>
        set((state) => ({
          workspaces: state.workspaces.map((ws) => {
            if (ws.id !== wsId) return ws
            if (ws.tabs.includes(tabId)) return ws
            return { ...ws, tabs: [...ws.tabs, tabId] }
          }),
        })),

      removeTabFromWorkspace: (wsId, tabId) =>
        set((state) => ({
          workspaces: state.workspaces.map((ws) =>
            ws.id === wsId
              ? {
                  ...ws,
                  tabs: ws.tabs.filter((id) => id !== tabId),
                  activeTabId: ws.activeTabId === tabId ? null : ws.activeTabId,
                }
              : ws,
          ),
        })),

      setWorkspaceActiveTab: (wsId, tabId) =>
        set((state) => ({
          workspaces: state.workspaces.map((ws) =>
            ws.id === wsId ? { ...ws, activeTabId: tabId } : ws,
          ),
        })),

      reorderWorkspaceTabs: (wsId, tabIds) =>
        set((state) => ({
          workspaces: state.workspaces.map((ws) => {
            if (ws.id !== wsId) return ws
            const currentSet = new Set(ws.tabs)
            const seen = new Set<string>()
            const filtered: string[] = []
            for (const id of tabIds) {
              if (currentSet.has(id) && !seen.has(id)) {
                filtered.push(id)
                seen.add(id)
              }
            }
            // Guard: if newOrder is a stale subset, preserve missing tabs at end
            if (filtered.length < ws.tabs.length) {
              const missing = ws.tabs.filter((id) => !seen.has(id))
              return { ...ws, tabs: [...filtered, ...missing] }
            }
            return { ...ws, tabs: filtered }
          }),
        })),

      reorderWorkspaces: (orderedIds) =>
        set((state) => {
          const byId = new Map(state.workspaces.map((ws) => [ws.id, ws]))
          const reordered = orderedIds.map((id) => byId.get(id)).filter(Boolean) as Workspace[]
          // Guard: if orderedIds is a stale subset, preserve missing workspaces at end
          if (reordered.length < state.workspaces.length) {
            const seen = new Set(orderedIds)
            for (const ws of state.workspaces) {
              if (!seen.has(ws.id)) reordered.push(ws)
            }
          }
          return { workspaces: reordered }
        }),

      findWorkspaceByTab: (tabId) => {
        return get().workspaces.find((ws) => ws.tabs.includes(tabId)) ?? null
      },

      ensureUnsortedWorkspace: () => {
        const existing = get().workspaces.find((ws) => ws.id === UNSORTED_WORKSPACE_ID)
        if (existing) return existing
        const ws: Workspace = { ...createWorkspace(useI18nStore.getState().t('workspace.unsorted')), id: UNSORTED_WORKSPACE_ID }
        set((state) => ({
          workspaces: [...state.workspaces, ws],
          activeWorkspaceId: state.activeWorkspaceId ?? ws.id,
        }))
        return ws
      },

      ensureWatchWorkspace: () => {
        const existing = get().workspaces.find((ws) => ws.id === WATCH_WORKSPACE_ID)
        if (existing) return existing
        const ws: Workspace = { ...createWorkspace(useI18nStore.getState().t('watch.area.name')), id: WATCH_WORKSPACE_ID }
        set((state) => ({ workspaces: [...state.workspaces, ws] }))
        return ws
      },

      insertTab: (tabId, workspaceId, afterTabId) => {
        // `== null`, not `=== undefined`: `null` used to mean "force standalone" and is gone from the type; from
        // an untyped caller it is "no target given", like everything else that is not a workspace id.
        let targetWsId = workspaceId
        if (targetWsId == null) {
          const { workspaces, activeWorkspaceId } = get()
          const active = workspaces.find((ws) => ws.id === activeWorkspaceId)
          targetWsId = (active ?? visibleWorkspaces(workspaces)[0] ?? get().ensureUnsortedWorkspace()).id
        }
        // The watcher area holds its own tabs and nothing else: a tab bound for it (an explicit target, or the active
        // workspace being `watch` while the area is shown) goes to the workspace the user came from.
        if (targetWsId === WATCH_WORKSPACE_ID && !isWatchTabId(tabId)) {
          targetWsId = getReturnWorkspaceId() ?? get().ensureUnsortedWorkspace().id
          afterTabId = undefined
        } else if (targetWsId !== WATCH_WORKSPACE_ID && isWatchTabId(tabId)) {
          // The reverse: an area tab belongs to the area, or to nowhere.
          if (!get().workspaces.some((ws) => ws.id === WATCH_WORKSPACE_ID)) return
          targetWsId = WATCH_WORKSPACE_ID
          afterTabId = undefined
        }

        // Concurrent-delete guard: if the caller passes a target workspace
        // that no longer exists (e.g. another session removed it mid-drag),
        // abort rather than running the dedup branch — otherwise the tab
        // would be removed from its source and land in no workspace at all.
        if (!get().workspaces.some((ws) => ws.id === targetWsId)) return

        set((state) => ({
          workspaces: state.workspaces.map((ws) => {
            if (ws.id === targetWsId) {
              if (ws.tabs.includes(tabId)) return { ...ws, activeTabId: tabId }
              let newTabs: string[]
              if (afterTabId === null) {
                newTabs = [tabId, ...ws.tabs]
              } else if (typeof afterTabId === 'string') {
                const idx = ws.tabs.indexOf(afterTabId)
                if (idx !== -1) {
                  newTabs = [...ws.tabs]
                  newTabs.splice(idx + 1, 0, tabId)
                } else {
                  newTabs = [...ws.tabs, tabId]
                }
              } else {
                newTabs = [...ws.tabs, tabId]
              }
              return { ...ws, tabs: newTabs, activeTabId: tabId }
            }
            // Remove from other workspaces (singleton tab dedup)
            if (!ws.tabs.includes(tabId)) return ws
            return {
              ...ws,
              tabs: ws.tabs.filter((id) => id !== tabId),
              activeTabId: ws.activeTabId === tabId ? null : ws.activeTabId,
            }
          }),
        }))
      },

      closeTabInWorkspace: (tabId, opts) => {
        const tabStore = useTabStore.getState()
        const tab = tabStore.tabs[tabId]
        if (!tab || tab.locked) return

        const ws = get().findWorkspaceByTab(tabId)
        // A tab no workspace has adopted yet (lib/adopt-standalone.ts waits before it believes that) still
        // closes; if it was on screen, what comes next is a tab of the workspace on screen.
        const scopeWs = ws ?? get().workspaces.find((w) => w.id === get().activeWorkspaceId) ?? null

        // 1. Pre-compute next tab (before any mutation)
        //    Priority: visitHistory (scoped to the workspace) → adjacent in the workspace
        // In the task-group view the bar's range replaces the workspace as the scope (it can cross workspaces and leave out tabs of the closed tab's own).
        const range = opts?.scope?.includes(tabId) ? opts.scope.filter((id) => id !== tabId && !!tabStore.tabs[id]) : null
        const scopeIds = new Set(range ?? (scopeWs?.tabs ?? []).filter((id) => id !== tabId))

        let nextTabId: string | null = null
        // Try visitHistory first (most recent visited tab still in scope)
        const { visitHistory } = tabStore
        for (let i = visitHistory.length - 1; i >= 0; i--) {
          if (scopeIds.has(visitHistory[i])) {
            nextTabId = visitHistory[i]
            break
          }
        }
        if (nextTabId === null && range !== null) {
          const idx = opts!.scope!.indexOf(tabId)
          const before = opts!.scope!.slice(0, idx).filter((id) => range.includes(id)).length
          nextTabId = range[Math.min(before, range.length - 1)] ?? null
        } else if (nextTabId === null && ws) {
          // Fallback to adjacent
          const idx = ws.tabs.indexOf(tabId)
          const remaining = ws.tabs.filter((id) => id !== tabId)
          nextTabId = remaining[Math.min(idx, remaining.length - 1)] ?? null
        } else if (nextTabId === null && scopeWs) {
          // Not in the list, so nothing is adjacent: the workspace's own active tab, else its first.
          const live = scopeWs.tabs.filter((id) => !!tabStore.tabs[id])
          nextTabId = scopeWs.activeTabId !== null && live.includes(scopeWs.activeTabId) ? scopeWs.activeTabId : (live[0] ?? null)
        }

        // Pre-compute wasActive flags before any mutation
        const wasActive = tabStore.activeTabId === tabId
        const wasWorkspaceActive = ws ? ws.activeTabId === tabId : false

        // 2. Record history (before mutation — tab object still exists)
        if (!opts?.skipHistory) {
          useHistoryStore.getState().recordClose(tab, ws?.id)
        }

        // 3. Remove from workspace
        if (ws) get().removeTabFromWorkspace(ws.id, tabId)

        // 4. Remove from tab store
        useTabStore.getState().closeTab(tabId)

        // 5. Sync active tab
        if (wasActive) {
          useTabStore.getState().setActiveTab(nextTabId)
        }
        if (ws && wasWorkspaceActive && nextTabId && ws.tabs.includes(nextTabId)) {
          get().setWorkspaceActiveTab(ws.id, nextTabId)
        }
        // The next tab of a range may be in another workspace: that workspace is the one shown, as a click on it would make it.
        if (range !== null && wasActive && nextTabId) {
          const nextWs = get().findWorkspaceByTab(nextTabId)
          if (nextWs && nextWs.id !== get().activeWorkspaceId) get().setActiveWorkspace(nextWs.id)
          if (nextWs) get().setWorkspaceActiveTab(nextWs.id, nextTabId)
        }
      },

      renameWorkspace: (wsId, name) =>
        set((state) => ({
          workspaces: state.workspaces.map((ws) =>
            ws.id === wsId ? { ...ws, name } : ws,
          ),
        })),

      setWorkspaceIcon: (wsId, icon) =>
        set((state) => ({
          workspaces: state.workspaces.map((ws) =>
            ws.id === wsId ? { ...ws, icon: icon || undefined } : ws,
          ),
        })),

      setWorkspaceIconWeight: (wsId, weight) =>
        set((state) => ({
          workspaces: state.workspaces.map((ws) =>
            ws.id === wsId ? { ...ws, iconWeight: weight } : ws,
          ),
        })),

      importWorkspace: (ws) =>
        set((state) => ({
          workspaces: state.workspaces.some((w) => w.id === ws.id)
            ? state.workspaces
            : [...state.workspaces, ws],
        })),

      setModuleConfig: (wsId, moduleId, key, value) =>
        set((state) => ({
          workspaces: state.workspaces.map((ws) => {
            if (ws.id !== wsId) return ws
            return {
              ...ws,
              moduleConfig: {
                ...(ws.moduleConfig ?? {}),
                [moduleId]: {
                  ...(ws.moduleConfig?.[moduleId] ?? {}),
                  [key]: value,
                },
              },
            }
          }),
        })),

      // Empties the world; does NOT touch its tag. `worldId` / `worldEpoch` say whose world this store holds, and
      // emptying it does not change whose it is: the Electron tear-off `replace` path (useElectronIpc.ts) resets a
      // window that may be showing a slave, and a tag thrown back to `master` / 0 there would disagree with
      // `useLocalProfilesStore` for ever (lib/profile/master-world.ts: unsettled, and nothing repairs it).
      // The way back into the emptied world goes with it (`watchReturnTarget`).
      reset: () => {
        watchReturnTarget = null
        set({ workspaces: [], activeWorkspaceId: null })
      },
    }),
    {
      name: STORAGE_KEYS.WORKSPACES,
      // A world store: a write from a window that still holds an older world is dropped (lib/storage/world-fence.ts).
      storage: fencedWorldStorage,
      // No version bump for `worldId` / `worldEpoch`: data written before them merges over the defaults
      // (`'master'` / 0), which is what it is. See `useTabStore`.
      version: 1,
      partialize: (state) => ({
        workspaces: state.workspaces,
        activeWorkspaceId: state.activeWorkspaceId,
        worldId: state.worldId,
        worldEpoch: state.worldEpoch,
      }),
    },
  ),
)

syncManager.register(STORAGE_KEYS.WORKSPACES, useWorkspaceStore)
registerFencedStore(STORAGE_KEYS.WORKSPACES, useWorkspaceStore)

// Remember the last workspace the user was in that is not the watcher area (see `returnWorkspaceId`), and take the way
// back on entering the area (see `watchReturnTarget`). Every window runs this on its own store, so a window that a
// synced write takes into the area keeps the workspace it showed itself, and the tab active in it just before.
// A world change does not touch it: `leaveWatchArea` checks the target against the workspaces there are when it is used.
useWorkspaceStore.subscribe((state, prev) => {
  const id = state.activeWorkspaceId
  if (id !== null && id !== WATCH_WORKSPACE_ID) returnWorkspaceId = id
  if (id === WATCH_WORKSPACE_ID && prev.activeWorkspaceId !== WATCH_WORKSPACE_ID) {
    const from = prev.workspaces.find((ws) => ws.id === prev.activeWorkspaceId)
    watchReturnTarget = from ? { workspaceId: from.id, tabId: from.activeTabId } : null
  }
})
