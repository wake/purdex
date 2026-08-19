import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { purdexStorage, STORAGE_KEYS } from '../lib/storage'
import { isPathAffectedBy, remapPathUnder } from '../lib/path-remap'
import type { FileSource } from '../types/fs'

const MAX_RECENT = 50

export type RecentFileKind = 'editor' | 'image-preview' | 'pdf-preview'

export interface RecentFileEntry {
  source: FileSource
  path: string
  name: string
  kind: RecentFileKind
  openedAt: number
}

/** Dedup identity: source type (+ host for daemon) plus the path. */
export function recentKey(source: FileSource, path: string): string {
  const sourceKey = source.type === 'daemon' ? `daemon:${source.hostId}` : source.type
  return `${sourceKey} ${path}`
}

/** Basename of a path, mirroring how `recordRecentFile` derives `name`. */
function nameOf(path: string): string {
  return path.split('/').pop() || path
}

interface RecentFilesState {
  files: RecentFileEntry[]
  addRecent: (entry: RecentFileEntry) => void
  /**
   * Re-point the entry at `from` and every descendant onto `to`. Which entries
   * count as affected, and where each lands, is the shared `lib/path-remap`
   * rule — the same one the open-pane scan in `storage-actions` uses. `name` is
   * recomputed from the new basename; `openedAt` is carried over untouched — a
   * rename is not a visit.
   *
   * Collision is this store's own rule, not part of the shared remap: when the
   * destination path already has an entry, the two merge into a single entry at
   * the destination. The renamed entry wins on identity (it is the file that now
   * lives there) but `openedAt` takes the newer of the two, so a rename cannot
   * resurrect a stale entry above more recent ones.
   */
  renamePath: (source: FileSource, from: string, to: string) => void
  /** Drop the entry at `path` and every descendant (same `lib/path-remap` rule). */
  removePath: (source: FileSource, path: string) => void
  clear: () => void
}

export const useRecentFilesStore = create<RecentFilesState>()(
  persist(
    (set) => ({
      files: [],
      addRecent: (entry) =>
        set((state) => {
          const key = recentKey(entry.source, entry.path)
          const filtered = state.files.filter(
            (f) => recentKey(f.source, f.path) !== key,
          )
          return { files: [entry, ...filtered].slice(0, MAX_RECENT) }
        }),
      renamePath: (source, from, to) =>
        set((state) => {
          // Neither branch can grow the list, so the MAX_RECENT cap is untouched.
          const next: RecentFileEntry[] = []
          const indexByKey = new Map<string, number>()
          let changed = false
          for (const file of state.files) {
            const matches = isPathAffectedBy(file, { source, from })
            let renamed = file
            if (matches) {
              const path = remapPathUnder(file.path, from, to)
              renamed = { ...file, path, name: nameOf(path) }
              changed = true
            }
            const key = recentKey(renamed.source, renamed.path)
            const existingIndex = indexByKey.get(key)
            if (existingIndex === undefined) {
              indexByKey.set(key, next.length)
              next.push(renamed)
              continue
            }
            // Collision: keep one entry at the earlier (more recent) slot, let the
            // renamed side own the identity, and take the newer `openedAt`.
            const existing = next[existingIndex]
            const winner = matches ? renamed : existing
            next[existingIndex] = {
              ...winner,
              openedAt: Math.max(existing.openedAt, renamed.openedAt),
            }
            changed = true
          }
          return changed ? { files: next } : { files: state.files }
        }),
      removePath: (source, path) =>
        set((state) => {
          const next = state.files.filter((f) => !isPathAffectedBy(f, { source, from: path }))
          return next.length === state.files.length ? { files: state.files } : { files: next }
        }),
      clear: () => set({ files: [] }),
    }),
    {
      name: STORAGE_KEYS.RECENT_FILES,
      storage: purdexStorage,
    },
  ),
)
