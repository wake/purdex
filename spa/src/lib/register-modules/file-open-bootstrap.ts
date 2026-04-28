import { useTabStore } from '../../stores/useTabStore'
import { useWorkspaceStore } from '../../features/workspace/store'
import { useSessionStore } from '../../stores/useSessionStore'
import { getDefaultOpener } from '../file-opener-registry'
import { computeClusterInsertTarget } from '../tab-insert/compute-cluster-insert-target'
import {
  createOpenFileService,
  showFileNotFoundPopup,
  hideFileNotFoundPopup,
  type OpenFileService,
  type OpenFileContext,
  type PopupController,
} from '../file-open'
import { createDaemonBackendForHost } from '../fs-backend-daemon'
import type { FileInfo, FileSource } from '../../types/fs'
import type { PaneContent } from '../../types/tab'

/**
 * P5 file-open bootstrap.
 *
 * Builds the openFileService instances used by:
 *   - terminal-link file-path opener
 *   - FileTreeView (workspace files panel)
 *
 * Each consumer gets its own service so its `tabOpener` can supply the
 * cluster-insert behavior appropriate for that surface (terminal-link
 * threads `afterTabId` through both stores; FileTreeView follows the same
 * `openClusteredTab` pattern).
 *
 * The popup controller is shared (singleton popup) but the `onSearch*`
 * callbacks are filled in Task 5.8. For 5.7b they're no-ops so the popup's
 * Cancel button still works and the missing-file UX surfaces, but expand
 * doesn't actually issue an fs.search yet.
 */

const FILE_KINDS = new Set<string>(['editor', 'image-preview', 'pdf-preview'])
const isFileKind = (c: PaneContent): boolean => FILE_KINDS.has(c.kind)

/**
 * Resolve the cwd for `OpenFileContext` based on the active session, or
 * null when no session can be matched. Used as the path-cache scope key —
 * keep nullable so callers know to fall back to a sensible alternative
 * (workspace projectPath, file dirname).
 */
function resolveSessionCwd(hostId: string, sessionCode?: string): string | null {
  if (!sessionCode) return null
  const sess = useSessionStore.getState().sessions[hostId]?.find((s) => s.code === sessionCode)
  return sess?.cwd ?? null
}

/** Factory shared by both surface bootstraps. */
function buildPopupController(): PopupController {
  return {
    show: (spec) =>
      showFileNotFoundPopup(spec, {
        sessionCwd: resolveSessionCwd(spec.ctx.hostId, spec.ctx.sessionCode),
        projectPath: resolveProjectPath(spec.ctx.sourceWorkspaceId),
        onOpenPath: (path) => {
          // Open the patched path through the same tab-opener path the
          // service uses for verified hits — clustering rules apply.
          const targetWs = spec.ctx.sourceWorkspaceId
          const file: FileInfo = {
            ...spec.file,
            path,
          }
          const opener = getDefaultOpener(file)
          if (!opener) return
          const content = opener.createContent(spec.source, file)
          const afterTabId = computeClusterInsertTarget(targetWs, isFileKind)
          const tabId = useTabStore.getState().openSingletonTab(content, { afterTabId })
          useWorkspaceStore.getState().insertTab(tabId, targetWs, afterTabId)
        },
        // 5.8 fills these in. For 5.7b the popup expand UI has the buttons
        // but they're no-ops so the integration point is verifiable
        // without dragging in fs.search wiring this commit.
        onSearchSessionCwd: () => {},
        onSearchWorkspace: () => {},
      }),
    hide: hideFileNotFoundPopup,
  }
}

function resolveProjectPath(workspaceId: string): string | null {
  const ws = useWorkspaceStore.getState().workspaces.find((w) => w.id === workspaceId)
  const cfg = ws?.moduleConfig as Record<string, Record<string, unknown> | undefined> | undefined
  const pp = cfg?.['files']?.['projectPath']
  return typeof pp === 'string' && pp.length > 0 ? pp : null
}

/**
 * The default tabOpener: getDefaultOpener + createContent +
 * openSingletonTab + insertTab. Mirrors the previous direct paths in
 * FileTreeView and file-path opener so behavior is unchanged for the
 * happy (file-exists) case.
 */
function defaultTabOpener(file: FileInfo, source: FileSource, ctx: OpenFileContext): void {
  const opener = getDefaultOpener(file)
  if (!opener) return
  const content = opener.createContent(source, file)
  const afterTabId = computeClusterInsertTarget(ctx.sourceWorkspaceId, isFileKind)
  const tabId = useTabStore.getState().openSingletonTab(content, { afterTabId })
  useWorkspaceStore.getState().insertTab(tabId, ctx.sourceWorkspaceId, afterTabId)
}

/** Singleton file-tree open service (lazy-built so HMR test resets are clean). */
let _fileTreeService: OpenFileService | undefined
function getFileTreeService(): OpenFileService {
  if (!_fileTreeService) {
    _fileTreeService = createOpenFileService({
      fsBackendFactory: (hostId) => createDaemonBackendForHost(hostId),
      popupController: buildPopupController(),
      tabOpener: defaultTabOpener,
    })
  }
  return _fileTreeService
}

/** Singleton terminal-link open service. */
let _terminalLinkService: OpenFileService | undefined
function getTerminalLinkService(): OpenFileService {
  if (!_terminalLinkService) {
    _terminalLinkService = createOpenFileService({
      fsBackendFactory: (hostId) => createDaemonBackendForHost(hostId),
      popupController: buildPopupController(),
      tabOpener: defaultTabOpener,
    })
  }
  return _terminalLinkService
}

/** Public entrypoints used by FileTreeView and terminal-link bootstrap. */
export function tryOpenFileForFileTree(
  file: FileInfo,
  source: FileSource,
  ctx: OpenFileContext,
): Promise<void> {
  return getFileTreeService().tryOpenFile(file, source, ctx)
}

export function tryOpenFileForTerminalLink(
  file: FileInfo,
  source: FileSource,
  ctx: OpenFileContext,
): Promise<void> {
  return getTerminalLinkService().tryOpenFile(file, source, ctx)
}

/** Resolve the open-context cwd best-effort (active session of the host). */
export function resolveOpenContextCwdFromSessions(
  hostId: string,
  sessionCode?: string,
): string | null {
  return resolveSessionCwd(hostId, sessionCode)
}

/** @internal — test reset so HMR / vitest can rebuild the singletons. */
export function __resetFileOpenBootstrap(): void {
  _fileTreeService = undefined
  _terminalLinkService = undefined
}
