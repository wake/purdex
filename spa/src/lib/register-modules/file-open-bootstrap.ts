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
  type OpenFileService,
  type OpenFileContext,
  type PopupController,
  type PopupSpec,
  type SearchMatch,
  type SearchRootCapability,
} from '../file-open'
import { createDaemonBackendForHost } from '../fs-backend-daemon'
import { recordRecentFile } from '../recent-files/record-recent-file'
import type { FileInfo, FileSource } from '../../types/fs'
import type { PaneContent } from '../../types/tab'

/**
 * P5 file-open bootstrap.
 *
 * Builds the openFileService instance used by the terminal-link file-path
 * opener. Its `tabOpener` supplies the cluster-insert behavior (threads
 * `afterTabId` through both stores).
 *
 * The popup controller is shared (singleton popup); its session-cwd search
 * re-renders the popup with the Layer-2 results (Task 5.8).
 */

const FILE_KINDS = new Set<string>(['editor', 'image-preview', 'pdf-preview'])
const isFileKind = (c: PaneContent): boolean => FILE_KINDS.has(c.kind)

/**
 * Resolve the cwd for `OpenFileContext` based on the active session, or
 * null when no session can be matched. Used as the path-cache scope key —
 * keep nullable so callers know to fall back to a sensible alternative
 * (file dirname).
 */
function resolveSessionCwd(hostId: string, sessionCode?: string): string | null {
  if (!sessionCode) return null
  const sess = useSessionStore.getState().sessions[hostId]?.find((s) => s.code === sessionCode)
  return sess?.cwd ?? null
}

/**
 * Run the Layer-2 (session cwd) fs.search and re-render the popup with the
 * result, unless the caller's signal aborted while we were awaiting (attack
 * review #5).
 */
async function runExpandedSearch(spec: PopupSpec, signal: AbortSignal): Promise<void> {
  const sessionCode = spec.ctx.sessionCode
  if (!sessionCode) return // capability missing — popup CTA should already be disabled
  const roots: SearchRootCapability[] = [{ kind: 'session-cwd', sessionCode }]

  let hits: SearchMatch[] = []
  try {
    // R2-M1: pass through the popup's signal so dismissing the popup
    // actually tears down the daemon-side WalkDir, not just the SPA-side
    // re-mount. AbortError surfaces below as a hits=[] result; the
    // signal.aborted check then short-circuits before re-rendering.
    hits = await fsSearchByCapability(spec.ctx.hostId, spec.file.name, roots, undefined, signal)
  } catch (err) {
    if ((err as { name?: string }).name === 'AbortError') {
      // Popup was dismissed while the fetch was in flight — caller does the
      // cleanup; we simply unwind without re-rendering.
      return
    }
    // Other errors → still re-render with empty results so the user sees
    // "No matches" rather than a stuck popup. Log for debugging.
    console.warn(`[file-open] fs.search failed: ${(err as Error)?.message ?? String(err)}`)
    hits = []
  }

  if (signal.aborted) return // user closed the popup mid-flight; do NOT re-mount

  const expandedSpec: PopupSpec = {
    mode: 'expanded',
    file: spec.file,
    source: spec.source,
    ctx: spec.ctx,
    layer2Hits: hits,
  }
  // Controlled re-render — popup service reuses the live root + token.
  buildPopupController().show(expandedSpec)
}

/** Popup controller factory (the popup itself is a singleton). */
function buildPopupController(): PopupController {
  return {
    show: (spec) => {
      return showFileNotFoundPopup(spec, {
        sessionCwd: resolveSessionCwd(spec.ctx.hostId, spec.ctx.sessionCode),
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
          recordRecentFile(content)
          const afterTabId = computeClusterInsertTarget(targetWs, isFileKind)
          const tabId = useTabStore.getState().openSingletonTab(content, { afterTabId })
          useWorkspaceStore.getState().insertTab(tabId, targetWs, afterTabId)
        },
        onSearchSessionCwd: (s, signal) => {
          void runExpandedSearch(s, signal)
        },
      })
    },
    hide: hideFileNotFoundPopup,
  }
}

/**
 * The default tabOpener: getDefaultOpener + createContent +
 * openSingletonTab + insertTab. Mirrors the previous direct path in the
 * file-path opener so behavior is unchanged for the happy (file-exists)
 * case.
 */
function defaultTabOpener(file: FileInfo, source: FileSource, ctx: OpenFileContext): void {
  const opener = getDefaultOpener(file)
  if (!opener) return
  const content = opener.createContent(source, file)
  recordRecentFile(content)
  const afterTabId = computeClusterInsertTarget(ctx.sourceWorkspaceId, isFileKind)
  const tabId = useTabStore.getState().openSingletonTab(content, { afterTabId })
  useWorkspaceStore.getState().insertTab(tabId, ctx.sourceWorkspaceId, afterTabId)
}

/** Singleton terminal-link open service (lazy-built so HMR test resets are clean). */
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

/**
 * Direct-open bypass — used by terminal-link's tilde fallback (R3 P2). When
 * the path can't be made absolute (home endpoint failed), the pre-P5
 * behavior was to open it as a blank editor buffer. The stat/cache/popup
 * pipeline can't do that because the daemon rejects non-absolute stat
 * with 400, so this skips straight to the tab-opener.
 */
export function openFileAsBufferDirect(
  file: FileInfo,
  source: FileSource,
  ctx: OpenFileContext,
): void {
  defaultTabOpener(file, source, ctx)
}

/** Public entrypoint used by the terminal-link bootstrap. */
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
  _terminalLinkService = undefined
}
