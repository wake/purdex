import { useTabStore } from '../stores/useTabStore'
import { useWorkspaceStore } from '../features/workspace/store'
import { getDefaultOpener } from './file-opener-registry'
import { computeClusterInsertTarget } from './tab-insert/compute-cluster-insert-target'
import { getFsBackend } from './fs-backend'
import { basename } from './storage-paths'
import type { FileInfo } from '../types/fs'
import type { PaneContent } from '../types/tab'

/**
 * Open an In-App storage file by routing through the file-opener registry,
 * mirroring the modern `defaultTabOpener` sequence
 * (`register-modules/file-open-bootstrap.ts:194`) but WITHOUT the daemon-shaped
 * `createOpenFileService` (which requires a `hostId`, scopes its cache by
 * `hostId/cwd`, and runs a daemon session/workspace missing-file search +
 * popup — all wrong for `{ type: 'inapp' }`).
 *
 * The five-call core sequence gives us, for free:
 *   - registry dispatch by extension (md→editor, png→image-preview,
 *     pdf→pdf-preview);
 *   - open-or-focus (`openSingletonTab` dedupes by exact `filePath` for
 *     inapp panes, `pane-utils.ts:13-15`) — an already-open file is focused,
 *     a different file gets a new tab, and an *unrelated* editor pane is
 *     never reused (kills the old smart-open hijack);
 *   - workspace placement + cluster insert + `ws.activeTabId` sync via
 *     `insertTab`.
 *
 * `workspaceId` is the source workspace (from the pane/popover context). It is
 * `string | null`: a `null` id means the caller could not resolve an owning
 * workspace, so we **refuse** the open rather than fall back to `''`/the active
 * workspace (`computeClusterInsertTarget('')` anchors on the global active tab
 * and `insertTab(_, '')` no-ops, landing the tab in the wrong place — R2-2).
 *
 * Missing-file handling is a minimal **stat-gate** (R2-1): we `stat` the path
 * first and ABORT when it does not exist. Skipping this lets a deleted/stale
 * path open as an empty editor buffer whose next save would resurrect the file.
 * The tree only offers existing entries, but it can go stale between render and
 * click, so the gate is load-bearing. The Phase 1c download-disposition for
 * non-previewable binaries (docx/xlsx/zip…) is NOT handled here — Phase 1a
 * covers image/pdf/editor only.
 *
 * @returns the id of the opened (or focused) tab, or `undefined` when the open
 *   is refused (null workspace), the file is missing, or no opener matches.
 */
export async function openInAppFile(
  path: string,
  workspaceId: string | null,
): Promise<string | undefined> {
  // R2-2: refuse rather than guess a workspace.
  if (workspaceId == null) return undefined

  // R2-1: stat-gate — never open a pane for a missing/stale path.
  const backend = getFsBackend({ type: 'inapp' })
  if (!backend) return undefined
  let size = 0
  try {
    const stat = await backend.stat(path)
    if (stat.isDirectory) return undefined
    size = stat.size
  } catch {
    return undefined
  }

  const name = basename(path)
  const extension = name.includes('.') ? name.split('.').pop()! : ''
  const file: FileInfo = {
    name,
    path,
    extension,
    size,
    isDirectory: false,
  }

  const opener = getDefaultOpener(file)
  if (!opener) return undefined

  const content = opener.createContent({ type: 'inapp' }, file)
  const afterTabId = computeClusterInsertTarget(workspaceId, isFileKind)
  const tabId = useTabStore.getState().openSingletonTab(content, { afterTabId })
  useWorkspaceStore.getState().insertTab(tabId, workspaceId, afterTabId)
  return tabId
}

// Same predicate the shared open pipeline uses for clustering
// (`file-open-bootstrap.ts:41-42`, not exported there). File-kind panes
// cluster next to each other in the tab bar.
const FILE_KINDS = new Set<string>(['editor', 'image-preview', 'pdf-preview'])
function isFileKind(content: PaneContent): boolean {
  return FILE_KINDS.has(content.kind)
}
