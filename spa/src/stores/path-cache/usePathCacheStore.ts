import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { STORAGE_KEYS, purdexStorage } from '../../lib/storage'
import {
  type PathCacheEntry,
  scopeKey,
  parseScopeKey,
  normalizeDir,
  normalizeCwd,
  dirname,
  upsertEntry,
  rankCandidates,
  sanitizeEntries,
} from './path-utils'

/**
 * Zustand persist sanitizer — runs the same invariants `add()` enforces so a
 * tampered or stale persisted file can't bypass normalization / cap / dedup
 * (R2-F1). Mutates the rehydrated state object in place; using setState here
 * would TDZ when persist fires synchronously for the localStorage backend
 * (R1 finding #1).
 *
 * Exported for direct unit testing.
 */
export function sanitizeRehydratedPathCache(
  state: { entriesByScope?: unknown } | undefined,
  error: unknown,
): void {
  if (!state) return
  if (error || typeof state.entriesByScope !== 'object' || state.entriesByScope === null || Array.isArray(state.entriesByScope)) {
    state.entriesByScope = {}
    return
  }
  const cleaned: Record<string, PathCacheEntry[]> = {}
  for (const [k, v] of Object.entries(state.entriesByScope as Record<string, unknown>)) {
    if (!parseScopeKey(k)) continue  // drop malformed scope keys
    const entries = sanitizeEntries(v)
    if (entries.length > 0) cleaned[k] = entries
  }
  state.entriesByScope = cleaned
}

interface PathCacheState {
  entriesByScope: Record<string, PathCacheEntry[]>  // key = scopeKey(hostId, cwd)
  add: (hostId: string, cwd: string, sessionCode: string, dir: string) => void
  lookup: (hostId: string, cwd: string, basename: string, currentSessionCode?: string) => string[]
  pruneStaleCandidate: (hostId: string, cwd: string, candidatePath: string) => void
  clearScope: (hostId: string, cwd: string) => void
  clearBySession: (sessionCode: string) => void
  clearHost: (hostId: string) => void
}

export const usePathCacheStore = create<PathCacheState>()(
  persist(
    (set, get) => ({
      entriesByScope: {},

      add: (hostId, cwd, sessionCode, dir) =>
        set((state) => {
          const cwdNorm = normalizeCwd(cwd)
          const dirNorm = normalizeDir(dir)
          if (!cwdNorm || !dirNorm) return state
          const key = scopeKey(hostId, cwdNorm)
          const next = upsertEntry(state.entriesByScope[key], {
            dir: dirNorm,
            sessionCode,
            touchedAt: Date.now(),
          })
          return { entriesByScope: { ...state.entriesByScope, [key]: next } }
        }),

      lookup: (hostId, cwd, basename, currentSessionCode) => {
        const cwdNorm = normalizeCwd(cwd)
        if (!cwdNorm) return []
        const list = get().entriesByScope[scopeKey(hostId, cwdNorm)] ?? []
        return rankCandidates(list, currentSessionCode).map((e) =>
          e.dir === '/' ? `/${basename}` : `${e.dir}/${basename}`,
        )
      },

      pruneStaleCandidate: (hostId, cwd, candidatePath) =>
        set((state) => {
          const cwdNorm = normalizeCwd(cwd)
          if (!cwdNorm) return state
          const key = scopeKey(hostId, cwdNorm)
          const existing = state.entriesByScope[key]
          if (!existing) return state
          const dir = normalizeDir(dirname(candidatePath))
          if (!dir) return state
          const next = existing.filter((e) => e.dir !== dir)
          if (next.length === existing.length) return state
          if (next.length === 0) {
            const { [key]: _removed, ...rest } = state.entriesByScope
            void _removed
            return { entriesByScope: rest }
          }
          return { entriesByScope: { ...state.entriesByScope, [key]: next } }
        }),

      clearScope: (hostId, cwd) =>
        set((state) => {
          const cwdNorm = normalizeCwd(cwd)
          if (!cwdNorm) return state
          const key = scopeKey(hostId, cwdNorm)
          if (!(key in state.entriesByScope)) return state
          const { [key]: _removed, ...rest } = state.entriesByScope
          void _removed
          return { entriesByScope: rest }
        }),

      clearBySession: (sessionCode) =>
        set((state) => {
          let changed = false
          const next: Record<string, PathCacheEntry[]> = {}
          for (const [k, list] of Object.entries(state.entriesByScope)) {
            const filtered = list.filter((e) => e.sessionCode !== sessionCode)
            if (filtered.length !== list.length) changed = true
            if (filtered.length > 0) next[k] = filtered
          }
          return changed ? { entriesByScope: next } : state
        }),

      clearHost: (hostId) =>
        set((state) => {
          let changed = false
          const next: Record<string, PathCacheEntry[]> = {}
          for (const [k, list] of Object.entries(state.entriesByScope)) {
            const parsed = parseScopeKey(k)
            if (parsed && parsed.hostId === hostId) {
              changed = true
            } else {
              next[k] = list
            }
          }
          return changed ? { entriesByScope: next } : state
        }),
    }),
    {
      name: STORAGE_KEYS.PATH_CACHE_V1,
      storage: purdexStorage,
      partialize: (s) => ({ entriesByScope: s.entriesByScope }),
      onRehydrateStorage: () => (state, error) => sanitizeRehydratedPathCache(state, error),
    },
  ),
)

// NOTE: intentionally NOT registered with syncManager. Cache is scoped per
// (hostId, cwd) and lookups only happen inside the window owning the agent
// session — cross-window in-memory copies would be dead state, and sync would
// recreate the tear-off race that R2-D1 exposed. Persisted state still
// survives via per-origin localStorage when a tear-off opens a new window.
