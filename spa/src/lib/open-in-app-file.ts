import { useTabStore } from '../stores/useTabStore'
import { useWorkspaceStore } from '../features/workspace/store'
import { getDefaultOpener } from './file-opener-registry'
import { computeClusterInsertTarget } from './tab-insert/compute-cluster-insert-target'
import { getFsBackend } from './fs-backend'
import { basename } from './storage-paths'
import { roleForExtension } from './file-extension-roles'
import { triggerDownload } from './download-file'
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
 * click, so the gate is load-bearing. After the gate, a non-previewable binary
 * (docx/xlsx/zip…) is routed to a download via `roleForExtension` (Phase 1c
 * T1c-3) instead of opening a (garbled) editor pane.
 *
 * @returns the id of the opened (or focused) tab, or `undefined` when the file
 *   was downloaded (no tab), the open is refused (null workspace), the file is
 *   missing, or no opener matches.
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

  // T1c-3: binary open-disposition. A non-previewable binary (docx/xlsx/zip/…)
  // is downloaded as a side-effect — it has no pane, so it is handled BEFORE
  // opener dispatch rather than via `FileOpener.createContent` (which must
  // return a `PaneContent`). The stat-gate above already proved the path
  // exists, so the `read` cannot resurrect a stale file. Returns `undefined`:
  // no tab is opened. (Image/pdf → preview pane; text/code → monaco below.)
  if (roleForExtension(extension) === 'download') {
    // The stat-gate proved the path existed a moment ago, but the entry can be
    // deleted (or the read otherwise fail) between the stat and this read — a
    // TOCTOU window. Guard the read+download so a failure quietly refuses (no
    // tab, no throw), consistent with the stat-gate's silent abort above
    // (codex R2 C2), rather than escaping as an unhandled rejection.
    try {
      const bytes = await backend.read(path)
      triggerDownload(new Blob([new Uint8Array(bytes)]), name)
    } catch {
      return undefined
    }
    return undefined
  }

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
