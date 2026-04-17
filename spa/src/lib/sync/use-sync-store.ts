import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { purdexStorage, STORAGE_KEYS, syncManager } from '../storage'
import type { ConflictItem, SyncBundle } from './types'

/**
 * Registry of all known contributor IDs. Populated by registerSyncContributors()
 * at startup, before any UI interaction triggers setActiveProvider().
 */
let _allContributorIds: string[] = []

/** Called by register-sync.ts after all contributors are registered. */
export function setAllContributorIds(ids: string[]): void {
  _allContributorIds = ids
}

// ---------------------------------------------------------------------------
// State shape
// ---------------------------------------------------------------------------

interface SyncStoreState {
  // Persisted state
  lastSyncedBundle: SyncBundle | null
  lastSyncedAt: number | null
  activeProviderId: string | null
  enabledModules: string[]
  clientId: string | null
  /** Host ID to sync through when activeProviderId === 'daemon'. */
  syncHostId: string | null
  pendingConflicts: ConflictItem[]
  pendingRemoteBundle: SyncBundle | null
  pendingConflictsAt: number | null

  // Actions
  setLastSyncedBundle: (bundle: SyncBundle) => void
  setActiveProvider: (providerId: string | null) => void
  toggleModule: (moduleId: string) => void
  getClientId: () => string
  setSyncHostId: (hostId: string | null) => void
  setPendingConflicts: (conflicts: ConflictItem[], remoteBundle: SyncBundle) => void
  clearPendingConflicts: () => void
  reset: () => void
}

// ---------------------------------------------------------------------------
// Initial state (extracted so reset() can reference it)
// ---------------------------------------------------------------------------

const initialState = {
  lastSyncedBundle: null,
  lastSyncedAt: null,
  activeProviderId: null,
  enabledModules: [] as string[],
  clientId: null,
  syncHostId: null,
  pendingConflicts: [] as ConflictItem[],
  pendingRemoteBundle: null,
  pendingConflictsAt: null,
} satisfies Pick<
  SyncStoreState,
  | 'lastSyncedBundle'
  | 'lastSyncedAt'
  | 'activeProviderId'
  | 'enabledModules'
  | 'clientId'
  | 'syncHostId'
  | 'pendingConflicts'
  | 'pendingRemoteBundle'
  | 'pendingConflictsAt'
>

// ---------------------------------------------------------------------------
// Client ID generation
// Stable identifier for this browser/device: "c_" + 12 lowercase hex chars
// ---------------------------------------------------------------------------

function generateClientId(): string {
  const bytes = crypto.getRandomValues(new Uint8Array(6))
  const hex = Array.from(bytes)
    .map((b) => b.toString(16).padStart(2, '0'))
    .join('')
  return `c_${hex}`
}

// ---------------------------------------------------------------------------
// Store
// ---------------------------------------------------------------------------

export const useSyncStore = create<SyncStoreState>()(
  persist(
    (set, get) => ({
      ...initialState,

      setLastSyncedBundle: (bundle) =>
        set({ lastSyncedBundle: bundle, lastSyncedAt: Date.now() }),

      setActiveProvider: (providerId) =>
        set((state) => {
          // When enabling a provider for the first time (empty modules list),
          // auto-enable all registered contributors so sync works out of the box.
          const modules =
            state.enabledModules.length === 0 && providerId != null
              ? [..._allContributorIds]
              : state.enabledModules
          return {
            activeProviderId: providerId,
            enabledModules: modules,
            lastSyncedBundle: null,
            lastSyncedAt: null,
          }
        }),

      toggleModule: (moduleId) =>
        set((state) => {
          const present = state.enabledModules.includes(moduleId)
          return {
            enabledModules: present
              ? state.enabledModules.filter((id) => id !== moduleId)
              : [...state.enabledModules, moduleId],
          }
        }),

      getClientId: () => {
        const existing = get().clientId
        if (existing) return existing
        const id = generateClientId()
        set({ clientId: id })
        return id
      },

      setSyncHostId: (hostId) => set({ syncHostId: hostId }),

      setPendingConflicts: (conflicts, remoteBundle) =>
        set({
          pendingConflicts: conflicts,
          pendingRemoteBundle: remoteBundle,
          pendingConflictsAt: Date.now(),
        }),

      clearPendingConflicts: () =>
        set({
          pendingConflicts: [],
          pendingRemoteBundle: null,
          pendingConflictsAt: null,
        }),

      reset: () => set({ ...initialState }),
    }),
    {
      name: STORAGE_KEYS.SYNC_STATE,
      storage: purdexStorage,
      partialize: (state) => ({
        lastSyncedBundle: state.lastSyncedBundle,
        lastSyncedAt: state.lastSyncedAt,
        activeProviderId: state.activeProviderId,
        enabledModules: state.enabledModules,
        clientId: state.clientId,
        syncHostId: state.syncHostId,
        pendingConflicts: state.pendingConflicts,
        pendingRemoteBundle: state.pendingRemoteBundle,
        pendingConflictsAt: state.pendingConflictsAt,
      }),
    },
  ),
)

syncManager.register(STORAGE_KEYS.SYNC_STATE, useSyncStore)
