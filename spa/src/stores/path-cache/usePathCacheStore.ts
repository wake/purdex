import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { STORAGE_KEYS, purdexStorage, syncManager } from '../../lib/storage'

const MAX_DIRS_PER_SCOPE = 50
const scopeKey = (hostId: string, workspaceId: string) => `${hostId}:${workspaceId}`

/**
 * Mutate persisted state in place during rehydration so the store ref isn't
 * needed (Zustand calls the rehydrate callback synchronously when storage is
 * synchronous, before the create() return assigns to usePathCacheStore — the
 * old setState() approach hit a TDZ).
 *
 * Exported for unit tests; production wires it through onRehydrateStorage.
 */
export function sanitizeRehydratedPathCache(
  state: { dirsByScope?: unknown } | undefined,
  error: unknown,
): void {
  if (!state) return
  if (error || typeof state.dirsByScope !== 'object' || state.dirsByScope === null || Array.isArray(state.dirsByScope)) {
    state.dirsByScope = {}
    return
  }
  const cleaned: Record<string, string[]> = {}
  for (const [k, v] of Object.entries(state.dirsByScope as Record<string, unknown>)) {
    if (Array.isArray(v) && v.every((x) => typeof x === 'string')) cleaned[k] = v
  }
  state.dirsByScope = cleaned
}

function normalizeDir(raw: unknown): string | null {
  if (typeof raw !== 'string' || !raw.startsWith('/')) return null
  const parts: string[] = []
  for (const seg of raw.split('/')) {
    if (seg === '' || seg === '.') continue
    if (seg === '..') {
      if (parts.length > 0) parts.pop()
      continue
    }
    parts.push(seg)
  }
  return parts.length === 0 ? '/' : '/' + parts.join('/')
}

function dirname(p: string): string {
  const idx = p.lastIndexOf('/')
  if (idx <= 0) return '/'
  return p.substring(0, idx)
}

interface PathCacheState {
  dirsByScope: Record<string, string[]>
  add: (hostId: string, workspaceId: string, dir: string) => void
  lookup: (hostId: string, workspaceId: string, basename: string) => string[]
  pruneStaleCandidate: (hostId: string, workspaceId: string, candidatePath: string) => void
  clearScope: (hostId: string, workspaceId: string) => void
  clearHost: (hostId: string) => void
}

export const usePathCacheStore = create<PathCacheState>()(
  persist(
    (set, get) => ({
      dirsByScope: {},

      add: (hostId, workspaceId, dir) =>
        set((state) => {
          const norm = normalizeDir(dir)
          if (!norm) return state
          const key = scopeKey(hostId, workspaceId)
          const existing = state.dirsByScope[key] ?? []
          const filtered = existing.filter((d) => d !== norm)
          const next = [norm, ...filtered].slice(0, MAX_DIRS_PER_SCOPE)
          return { dirsByScope: { ...state.dirsByScope, [key]: next } }
        }),

      lookup: (hostId, workspaceId, basename) => {
        const key = scopeKey(hostId, workspaceId)
        const dirs = get().dirsByScope[key] ?? []
        return dirs.map((d) => (d === '/' ? `/${basename}` : `${d}/${basename}`))
      },

      pruneStaleCandidate: (hostId, workspaceId, candidatePath) =>
        set((state) => {
          const key = scopeKey(hostId, workspaceId)
          const existing = state.dirsByScope[key]
          if (!existing) return state
          const dir = normalizeDir(dirname(candidatePath))
          if (!dir) return state
          const next = existing.filter((d) => d !== dir)
          if (next.length === existing.length) return state
          return { dirsByScope: { ...state.dirsByScope, [key]: next } }
        }),

      clearScope: (hostId, workspaceId) =>
        set((state) => {
          const key = scopeKey(hostId, workspaceId)
          if (!(key in state.dirsByScope)) return state
          const { [key]: _removed, ...rest } = state.dirsByScope
          void _removed
          return { dirsByScope: rest }
        }),

      clearHost: (hostId) =>
        set((state) => {
          const prefix = `${hostId}:`
          const next: Record<string, string[]> = {}
          let changed = false
          for (const [k, v] of Object.entries(state.dirsByScope)) {
            if (k.startsWith(prefix)) {
              changed = true
            } else {
              next[k] = v
            }
          }
          return changed ? { dirsByScope: next } : state
        }),
    }),
    {
      name: STORAGE_KEYS.PATH_CACHE_V1,
      storage: purdexStorage,
      partialize: (s) => ({ dirsByScope: s.dirsByScope }),
      onRehydrateStorage: () => (state, error) => sanitizeRehydratedPathCache(state, error),
    },
  ),
)

// Cross-window propagation — every persisted purdex-* store registers so a
// write in one window broadcasts to others (matches workspace / tab / etc).
syncManager.register(STORAGE_KEYS.PATH_CACHE_V1, usePathCacheStore)
