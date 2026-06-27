import { useTabStore } from '../stores/useTabStore'
import { useWorkspaceStore } from '../features/workspace/store'
import { getDefaultOpener } from './file-opener-registry'
import { computeClusterInsertTarget } from './tab-insert/compute-cluster-insert-target'
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
 * `workspaceId` is the source workspace (from the pane/popover context) — it
 * is required because `computeClusterInsertTarget` / `insertTab` are
 * workspace-scoped.
 *
 * Missing-file handling is intentionally minimal: the tree only offers
 * existing entries, so there is no stat-gate here. The Phase 1c
 * download-disposition for non-previewable binaries (docx/xlsx/zip…) is NOT
 * handled here — Phase 1a covers image/pdf/editor only.
 *
 * @returns the id of the opened (or focused) tab, or `undefined` when no
 *   opener matches the file.
 */
export function openInAppFile(path: string, workspaceId: string): string | undefined {
  const name = basename(path)
  const extension = name.includes('.') ? name.split('.').pop()! : ''
  const file: FileInfo = {
    name,
    path,
    extension,
    size: 0,
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
