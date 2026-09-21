import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { createWorkspace, isStandaloneTab, type Workspace, type IconWeight } from '../../types/tab'
import { fencedWorldStorage, registerFencedStore, STORAGE_KEYS, syncManager } from '../../lib/storage'
import { useTabStore } from '../../stores/useTabStore'
import { useHistoryStore } from '../../stores/useHistoryStore'
import { useWorkspaceSettingsStore } from '../../stores/useWorkspaceSettingsStore'
import { useI18nStore } from '../../stores/useI18nStore'

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
  /** No `workspaceId` → the active workspace → else the first → else a new `Unsorted`. Never a silent no-op. */
  insertTab: (tabId: string, workspaceId?: string, afterTabId?: string | null) => void
  closeTabInWorkspace: (tabId: string, opts?: { skipHistory?: boolean }) => void
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
          let heirId: string | null = null
          if (survivors.length > 0) {
            const heir: Workspace = remaining[Math.min(index, remaining.length - 1)]
              ?? { ...createWorkspace(useI18nStore.getState().t('workspace.unsorted')), id: UNSORTED_WORKSPACE_ID }
            heirId = heir.id
            const merged = { ...heir, tabs: [...heir.tabs, ...survivors], activeTabId: heir.activeTabId ?? survivors[0] }
            remaining = remaining.length === 0 ? [merged] : remaining.map((ws) => (ws.id === heir.id ? merged : ws))
          }
          const activeId = state.activeWorkspaceId === wsId || state.activeWorkspaceId === null
            ? (heirId ?? remaining[0]?.id ?? null)
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

      insertTab: (tabId, workspaceId, afterTabId) => {
        // `== null`, not `=== undefined`: `null` used to mean "force standalone" and is gone from the type; from
        // an untyped caller it is "no target given", like everything else that is not a workspace id.
        let targetWsId = workspaceId
        if (targetWsId == null) {
          const { workspaces, activeWorkspaceId } = get()
          const active = workspaces.find((ws) => ws.id === activeWorkspaceId)
          targetWsId = (active ?? workspaces[0] ?? get().ensureUnsortedWorkspace()).id
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
        const workspaces = get().workspaces

        // 1. Pre-compute next tab (before any mutation)
        //    Priority: visitHistory (scoped) → adjacent in workspace/tabOrder
        //    Standalone tabs only scope to other standalone tabs (not workspace tabs)
        const standaloneOrder = tabStore.tabOrder.filter(
          (id) => isStandaloneTab(id, workspaces),
        )
        const scopeIds = ws
          ? new Set(ws.tabs.filter((id) => id !== tabId))
          : new Set(standaloneOrder.filter((id) => id !== tabId))

        let nextTabId: string | null = null
        // Try visitHistory first (most recent visited tab still in scope)
        const { visitHistory } = tabStore
        for (let i = visitHistory.length - 1; i >= 0; i--) {
          if (scopeIds.has(visitHistory[i])) {
            nextTabId = visitHistory[i]
            break
          }
        }
        // Fallback to adjacent
        if (nextTabId === null) {
          const ordered = ws ? ws.tabs : standaloneOrder
          const idx = ordered.indexOf(tabId)
          const remaining = ordered.filter((id) => id !== tabId)
          nextTabId = remaining[Math.min(idx, remaining.length - 1)] ?? null
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
        if (ws && wasWorkspaceActive && nextTabId) {
          get().setWorkspaceActiveTab(ws.id, nextTabId)
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
      reset: () => set({ workspaces: [], activeWorkspaceId: null }),
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
