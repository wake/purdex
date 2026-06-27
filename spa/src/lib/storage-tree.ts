/**
 * storage-tree — full-recursive enumeration of an FsBackend into a nested tree.
 *
 * The In-App store is small, so we eagerly enumerate the entire tree in one
 * pass (no lazy per-dir loading): a single `useStorageTree` build hands both the
 * tree UI and the breadcrumb popover a complete `TreeNode[]`. Expand/collapse is
 * therefore UI-only state and never triggers a fetch.
 *
 * Node identity is the FULL path: two files sharing a basename in different
 * directories are distinct nodes (`/buffer/d1/x.md` ≠ `/buffer/d2/x.md`).
 */
import type { FsBackend } from './fs-backend'
import { join, parentOf, STORAGE_ROOT } from './storage-paths'

export interface TreeNode {
  /** Full path under the storage root, e.g. `/buffer/dir/b.md`. The identity. */
  path: string
  /** Basename (last path segment), e.g. `b.md`. */
  name: string
  isDir: boolean
  /** Byte size of the entry (0 for directories). */
  size: number
  /** Child nodes — present (possibly empty) for directories, absent for files. */
  children?: TreeNode[]
}

/** dirs-first, then case-aware name order — applied at every tree level. */
function sortNodes(nodes: TreeNode[]): TreeNode[] {
  return nodes.sort((a, b) => {
    if (a.isDir !== b.isDir) return a.isDir ? -1 : 1
    return a.name.localeCompare(b.name)
  })
}

/**
 * Recursively enumerate every entry under `root` into a sorted `TreeNode[]`.
 * Descends into each directory via `backend.list` (direct children per call);
 * the whole tree is materialized before returning.
 */
export async function listTreeUnder(backend: FsBackend, root: string): Promise<TreeNode[]> {
  const entries = await backend.list(root)
  const nodes: TreeNode[] = []
  for (const entry of entries) {
    const path = join(root, entry.name)
    if (entry.isDir) {
      nodes.push({
        path,
        name: entry.name,
        isDir: true,
        size: entry.size,
        children: await listTreeUnder(backend, path),
      })
    } else {
      nodes.push({ path, name: entry.name, isDir: false, size: entry.size })
    }
  }
  return sortNodes(nodes)
}

/**
 * Depth-first lookup of the node whose full `path` matches, or `null`. Used by
 * the Storage pane to resolve the current selection (a path) back to its
 * `TreeNode` so it can read `isDir` for the `targetDir` derivation (T1b-0).
 */
export function findNode(nodes: TreeNode[], path: string): TreeNode | null {
  for (const node of nodes) {
    if (node.path === path) return node
    if (node.children) {
      const found = findNode(node.children, path)
      if (found) return found
    }
  }
  return null
}

/**
 * The directory that nesting-aware actions (new file / new folder / drop-to)
 * should target given the current single selection (T1b-0, decision 3):
 *
 * - a selected **folder** → the folder itself,
 * - a selected **file** → its parent directory,
 * - **no** selection (or a non-single selection resolving to `null`) → the
 *   storage root.
 */
export function targetDirOf(node: TreeNode | null): string {
  if (!node) return STORAGE_ROOT
  return node.isDir ? node.path : parentOf(node.path)
}
