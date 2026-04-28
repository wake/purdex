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
/**
 * Module-level merged search state (R2-M1 fix). When the user clicks both
 * Layer-2 and Layer-3 CTAs in quick succession, runExpandedSearch is called
 * twice with the same popup token. Without this accumulator, whichever
 * search resolves second wipes the first layer's hits because each call
 * built its expandedSpec from scratch (`layer2Hits: kind==='session' ? hits
 * : []`). The accumulator merges by layer; the popup-service's controlled
 * re-render path (showFileNotFoundPopup → reuse root + token) keeps any
 * peer in-flight fetch's signal valid through the re-render.
 *
 * Reset whenever a fresh popup mounts (mode === 'ask-expand') so two
 * successive missing-file events don't bleed cache.
 */
let mergedHits: { layer2: SearchMatch[]; layer3: SearchMatch[] } = { layer2: [], layer3: [] }

function resetMergedHits(): void {
  mergedHits = { layer2: [], layer3: [] }
}

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
    // R2-M1: pass through the popup's signal so dismissing the popup
    // actually tears down the daemon-side WalkDir, not just the SPA-side
    // re-mount. AbortError surfaces below as a hits=[] result; the
    // signal.aborted check then short-circuits before merging.
    hits = await fsSearchByCapability(spec.ctx.hostId, spec.file.name, roots, undefined, signal)
  } catch (err) {
    if (err instanceof FsSearchError && err.status === 501) {
      // v6 degrade — workspace-projectPath not implemented in daemon yet;
      // suppress so the popup re-renders with empty layer3Hits rather than
      // surfacing a misleading server error.
      hits = []
    } else if ((err as { name?: string }).name === 'AbortError') {
      // Popup was dismissed while the fetch was in flight — caller does the
      // cleanup; we simply unwind without re-rendering.
      return
    } else {
      // Other errors → still re-render with empty bucket so the user sees
      // "No matches" rather than a stuck popup. Log for debugging.
      console.warn(`[file-open] fs.search failed: ${(err as Error)?.message ?? String(err)}`)
      hits = []
    }
  }

  if (signal.aborted) return // user closed the popup mid-flight; do NOT re-mount

  // Merge into the running accumulator (R2-M1) so a second-resolving layer
  // doesn't blank the first layer's results.
  if (kind === 'session') mergedHits.layer2 = hits
  else mergedHits.layer3 = hits

  const expandedSpec: PopupSpec = {
    mode: 'expanded',
    file: spec.file,
    source: spec.source,
    ctx: spec.ctx,
    layer2Hits: mergedHits.layer2,
    layer3Hits: mergedHits.layer3,
  }
  // Controlled re-render — popup service reuses the live root + token.
  buildPopupController().show(expandedSpec)
}

/** Factory shared by both surface bootstraps. */
function buildPopupController(): PopupController {
  return {
    show: (spec) => {
      // R2-M1: a fresh popup session starts whenever tryOpenFile shows
      // ask-expand / layer1-multi (mode 'expanded' is reserved for the
      // controlled re-render path inside runExpandedSearch). Reset the
      // accumulator so the new popup doesn't inherit hits from a previous
      // missing-file event in the same window.
      if (spec.mode !== 'expanded') resetMergedHits()
      return showFileNotFoundPopup(spec, {
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
      })
    },
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
  resetMergedHits()
}
