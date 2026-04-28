import { useTabStore } from '../../stores/useTabStore'
import { useWorkspaceStore } from '../../features/workspace/store'
import { useSessionStore } from '../../stores/useSessionStore'
import { getDefaultOpener } from '../file-opener-registry'
import { computeClusterInsertTarget } from '../tab-insert/compute-cluster-insert-target'
import {
  createOpenFileService,
  showFileNotFoundPopup,
  hideFileNotFoundPopup,
  fsSearchByCapability,
  FsSearchError,
  type OpenFileService,
  type OpenFileContext,
  type PopupController,
  type PopupSpec,
  type SearchMatch,
  type SearchRootCapability,
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

/**
 * Run an fs.search and re-mount the popup with the result, unless the
 * caller's signal aborted while we were awaiting (attack review #5).
 *
 * `kind` chooses which capability + which result bucket — Layer 2 fills
 * `layer2Hits`, Layer 3 fills `layer3Hits`. Daemon 501 (workspace
 * projectPath not yet implemented in daemon registry) is silently treated
 * as no-results per the v6 degrade plan, NOT surfaced as an error.
 */
async function runExpandedSearch(
  kind: 'session' | 'workspace',
  spec: PopupSpec,
  signal: AbortSignal,
): Promise<void> {
  let roots: SearchRootCapability[] = []
  if (kind === 'session' && spec.ctx.sessionCode) {
    roots = [{ kind: 'session-cwd', sessionCode: spec.ctx.sessionCode }]
  } else if (kind === 'workspace') {
    roots = [{ kind: 'workspace-projectPath', workspaceId: spec.ctx.sourceWorkspaceId }]
  }
  if (roots.length === 0) return // capability missing — popup CTA should already be disabled

  let hits: SearchMatch[] = []
  try {
    hits = await fsSearchByCapability(spec.ctx.hostId, spec.file.name, roots)
  } catch (err) {
    if (err instanceof FsSearchError && err.status === 501) {
      // v6 degrade — workspace-projectPath not implemented in daemon yet;
      // suppress so the popup re-renders with empty layer3Hits rather than
      // surfacing a misleading server error.
      hits = []
    } else {
      // Other errors → still re-render with empty bucket so the user sees
      // "No matches" rather than a stuck popup. Log for debugging.
      console.warn(`[file-open] fs.search failed: ${(err as Error)?.message ?? String(err)}`)
      hits = []
    }
  }

  if (signal.aborted) return // user closed the popup mid-flight; do NOT re-mount

  const expandedSpec: PopupSpec = {
    mode: 'expanded',
    file: spec.file,
    source: spec.source,
    ctx: spec.ctx,
    layer2Hits: kind === 'session' ? hits : [],
    layer3Hits: kind === 'workspace' ? hits : [],
  }
  // Re-mount via the same controller — preserves the singleton invariant.
  buildPopupController().show(expandedSpec)
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
        onSearchSessionCwd: (s, signal) => {
          void runExpandedSearch('session', s, signal)
        },
        onSearchWorkspace: (s, signal) => {
          void runExpandedSearch('workspace', s, signal)
        },
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
