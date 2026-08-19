/**
 * path-remap — the one definition of "which paths does a rename / move / delete
 * of `from` touch, and where does each of them land?".
 *
 * The rule is deceptively small and was, until this module, written out three
 * times: the open-pane scan in `storage-actions`, the delete-target scan next to
 * it, and `renamePath` / `removePath` in `useRecentFilesStore`. Each copy had to
 * remember the same two subtleties independently — that the trailing slash is
 * what stops a `/buffer/a` rename from swallowing the sibling `/buffer/ab`
 * (decision 6), and that a daemon path is only affected when the HOST matches
 * too. A fourth entity following a path is exactly where one of those copies
 * would have gone missing.
 *
 * Everything here is pure and store-agnostic. What a given store then DOES with
 * an affected entry stays with that store — the Recent list's same-destination
 * merge, for instance, is its own rule and deliberately not modelled here.
 */
import type { FileSource } from '../types/fs'

/**
 * Source identity minus the path: a rename/move/delete on one source must never
 * touch an identically-named path on another source type, or on another daemon
 * host.
 */
export function isSameFileSource(a: FileSource, b: FileSource): boolean {
  if (a.type !== b.type) return false
  if (a.type === 'daemon' && b.type === 'daemon') return a.hostId === b.hostId
  return true
}

/**
 * Whether `path` is `from` itself or one of its descendants.
 *
 * The trailing slash is the boundary that keeps `/buffer/ab` out of a
 * `/buffer/a` subtree; the exact-equality branch is what lets a plain file
 * target match its own path.
 */
export function isPathUnder(path: string, from: string): boolean {
  return path === from || path.startsWith(from + '/')
}

/**
 * The same subtree test, additionally gated on source identity — the form every
 * store-level scan needs (open panes, recent entries).
 */
export function isPathAffectedBy(
  entry: { source: FileSource; path: string },
  mutation: { source: FileSource; from: string },
): boolean {
  return isSameFileSource(entry.source, mutation.source) && isPathUnder(entry.path, mutation.from)
}

/**
 * Where an affected `path` lands once `from` becomes `to`: `from` itself maps to
 * `to`, and a descendant keeps its suffix. Only meaningful for a path that
 * `isPathUnder(path, from)` already accepted.
 */
export function remapPathUnder(path: string, from: string, to: string): string {
  return to + path.slice(from.length)
}
