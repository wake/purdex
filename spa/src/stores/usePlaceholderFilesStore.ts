/**
 * usePlaceholderFilesStore — the registry of files WE minted eagerly and the
 * user has never touched (T5.1).
 *
 * Why a registry and not a predicate: the three "New File" affordances reserve a
 * REAL empty file the instant they are pressed (the atomic IDB reservation that
 * fixed the double-click race, #854). A tab opened and never typed into leaves
 * that 0 B file behind forever — 24 stray `Untitled-N.txt` in practice. To sweep
 * them automatically we need the fact "this file is ours and untouched", and
 * that fact CANNOT be inferred from buffer state: the obvious predicate
 * (`savedContent === '' && !isDirty && lastStat.size === 0`) is equally true of a
 * file that had content, was deliberately emptied by the user, and saved —
 * because `markSaved` writes whatever was saved into `savedContent`. Deleting on
 * that inference destroys the user's file. So the fact is RECORDED at
 * reservation time and removed the first time the file changes hands.
 *
 * Membership ends — permanently, the file is now the user's — on the first of:
 *   - any successful write to the path (a save, INCLUDING a save of empty
 *     content: the user saved that emptiness deliberately),
 *   - a rename or move (both the old and the new path end up unregistered),
 *   - an explicit delete (the entry goes with the file),
 *   - a storage restore, which swaps the whole tree at once and therefore
 *     invalidates every entry (see `clear`).
 *
 * Scope: **in-app paths only**. Remote (daemon) and local files are never
 * recorded and never reported as placeholders — we do not create them eagerly
 * and must never auto-delete them. `register`/`unregister` take a `FileSource`
 * and no-op for anything else, so callers on a shared code path (the editor save
 * flow serves all three sources) need no guard of their own. That is also why
 * entries are stored as bare paths rather than `recentKey`-style source-prefixed
 * keys: with one possible source the prefix carries no information, and bare
 * paths let the subtree rules in `lib/path-remap` apply directly.
 *
 * The set is deliberately uncapped: entries are short-lived (the T5.2 sweep
 * removes each one when its last pane closes) and a path costs a few dozen
 * bytes.
 */
import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { purdexStorage, STORAGE_KEYS } from '../lib/storage'
import { isPathUnder } from '../lib/path-remap'
import type { FileSource } from '../types/fs'

interface PlaceholderFilesState {
  /** Full in-app paths currently held as untouched placeholders. */
  paths: string[]
  /** Record a freshly reserved path. No-op for a non-in-app source. */
  register: (source: FileSource, path: string) => void
  /**
   * Drop `path` and every descendant of it — the same subtree rule the rename /
   * delete scans use, so one call covers a folder that contains placeholders.
   * No-op for a non-in-app source.
   */
  unregister: (source: FileSource, path: string) => void
  /** Whether this exact (in-app) path is still an untouched placeholder. */
  isPlaceholder: (source: FileSource, path: string) => boolean
  /** Drop everything — the restore invalidation (a whole-tree replacement). */
  clear: () => void
}

export const usePlaceholderFilesStore = create<PlaceholderFilesState>()(
  persist(
    (set, get) => ({
      paths: [],
      register: (source, path) =>
        set((state) => {
          if (source.type !== 'inapp' || state.paths.includes(path)) return { paths: state.paths }
          return { paths: [...state.paths, path] }
        }),
      unregister: (source, path) =>
        set((state) => {
          if (source.type !== 'inapp') return { paths: state.paths }
          const next = state.paths.filter((p) => !isPathUnder(p, path))
          return next.length === state.paths.length ? { paths: state.paths } : { paths: next }
        }),
      isPlaceholder: (source, path) =>
        source.type === 'inapp' && get().paths.includes(path),
      clear: () => set((state) => (state.paths.length === 0 ? { paths: state.paths } : { paths: [] })),
    }),
    {
      name: STORAGE_KEYS.PLACEHOLDER_FILES,
      storage: purdexStorage,
    },
  ),
)
