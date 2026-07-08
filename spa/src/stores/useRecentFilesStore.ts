import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { purdexStorage, STORAGE_KEYS } from '../lib/storage'
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

interface RecentFilesState {
  files: RecentFileEntry[]
  addRecent: (entry: RecentFileEntry) => void
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
      clear: () => set({ files: [] }),
    }),
    {
      name: STORAGE_KEYS.RECENT_FILES,
      storage: purdexStorage,
    },
  ),
)
