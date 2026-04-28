import type { FileInfo, FileSource } from '../../types/fs'
import { usePathCacheStore } from '../../stores/path-cache/usePathCacheStore'
import { useEditorSettingsStore } from '../../stores/useEditorSettingsStore'

/**
 * Per-call open context. Captured at click-time and threaded through every
 * async step — host / workspace / cwd MUST NOT be re-read from active stores
 * mid-flow (Deviation 1; protects against the race the user surfaces by
 * switching workspace while a stat round-trip is pending).
 */
export interface OpenFileContext {
  /** Host ID where this file lives. Backend bound to this — see `createOpenFileService`. */
  hostId: string
  /** Path-cache scope key (hostId, cwd). Captured per click; usually = workspace projectPath OR session cwd OR home. */
  cwd: string
  /** Workspace whose context generated the click. Used for Layer-3 fs.search root + popup display. */
  sourceWorkspaceId: string
  /** Optional session origin; used as priority tag for path-cache lookup AND as Layer-2 capability. */
  sessionCode?: string
}

/** Minimal subset of `FsBackend` this service depends on (only `stat`). */
export interface FsBackendForOpen {
  stat: (path: string) => Promise<unknown>
}

/**
 * Popup specs the popup mount service can render. Layer 1/2/3 progression:
 * - `layer1-multi`: hook cache produced multiple verified candidates
 * - `ask-expand`: cache empty (or 0 verified) → user must opt into fs.search
 * - `expanded`: post-fs.search results (filled in by Task 5.8)
 */
export type PopupSpec =
  | { mode: 'ask-expand'; file: FileInfo; source: FileSource; ctx: OpenFileContext }
  | { mode: 'layer1-multi'; hits: string[]; file: FileInfo; source: FileSource; ctx: OpenFileContext }
  | {
      mode: 'expanded'
      file: FileInfo
      source: FileSource
      ctx: OpenFileContext
      layer2Hits: { path: string; modTime: string; sizeBytes: number; root: string }[]
      layer3Hits: { path: string; modTime: string; sizeBytes: number; root: string }[]
    }

export interface PopupController {
  /**
   * Mount popup with `spec`. Returns an AbortController whose `signal` is
   * aborted when the popup is closed (so async callers — e.g. layer-2
   * fs.search — can bail out if the user dismissed the popup mid-flight).
   */
  show: (spec: PopupSpec) => AbortController
  hide: () => void
}

export type TabOpener = (file: FileInfo, source: FileSource, ctx: OpenFileContext) => void

export interface OpenFileDeps {
  /**
   * Builds (or returns) a host-bound backend at the *start* of every tryOpenFile
   * call. The returned backend must continue to address `hostId` for the
   * duration of the flow even if the user switches active host mid-way
   * (Deviation 2 + attack-critical C5).
   */
  fsBackendFactory: (hostId: string) => FsBackendForOpen
  popupController: PopupController
  tabOpener: TabOpener
}

export interface OpenFileService {
  tryOpenFile(file: FileInfo, source: FileSource, ctx: OpenFileContext): Promise<void>
}

/** Thrown when popup is disabled and target file is missing. */
export class FileNotFoundError extends Error {
  readonly path: string
  constructor(path: string) {
    super(`File not found: ${path}`)
    this.path = path
    this.name = 'FileNotFoundError'
  }
}

/**
 * Strict not-found classifier — only ENOENT (Node fs) and HTTP 404 (daemon
 * rest) count as "missing". Auth (401/403), network (ENETDOWN, ECONNREFUSED),
 * and host-removed errors propagate unchanged so the user sees the real cause
 * rather than a misleading "file not found" popup (attack-critical C5).
 */
export function isNotFoundError(err: unknown): boolean {
  if (!err || typeof err !== 'object') return false
  const e = err as { code?: unknown; status?: unknown }
  if (e.code === 'ENOENT') return true
  if (e.status === 404) return true
  return false
}

/** Bind cache lookup + prune to a single (hostId, cwd) scope per Deviation 1. */
function lookupCachedCandidates(ctx: OpenFileContext, basename: string): string[] {
  return usePathCacheStore.getState().lookup(ctx.hostId, ctx.cwd, basename, ctx.sessionCode)
}

function pruneCandidate(ctx: OpenFileContext, candidatePath: string): void {
  usePathCacheStore.getState().pruneStaleCandidate(ctx.hostId, ctx.cwd, candidatePath)
}

/**
 * Build the open-file service. The returned object is stateless aside from
 * its closures over `deps` — caller may keep a single instance for the
 * surface (terminal-link / FileTreeView / etc.) and reuse it across clicks.
 */
export function createOpenFileService(deps: OpenFileDeps): OpenFileService {
  return {
    async tryOpenFile(file, source, ctx) {
      // Resolve the host-bound backend ONCE up-front. All subsequent stat
      // calls use this same backend so a mid-flow active-host switch can't
      // re-route them at the user's other host.
      const fs = deps.fsBackendFactory(ctx.hostId)

      // 1. Direct stat — only ENOENT / 404 counts as "missing"; everything
      //    else (auth/network/host gone) bubbles unchanged.
      let exists = false
      try {
        const stat = await fs.stat(file.path)
        exists = !!stat
      } catch (err) {
        if (!isNotFoundError(err)) throw err
        exists = false
      }
      if (exists) {
        deps.tabOpener(file, source, ctx)
        return
      }

      // 2. Popup gate — when off, surface a typed FileNotFoundError so callers
      //    can decide their own UX (toast / log / silent).
      const ui = useEditorSettingsStore.getState()
      if (!ui.popupOnMissingFile) throw new FileNotFoundError(file.path)

      // 3. Layer 1 (path-cache verified candidates). Candidates that no
      //    longer exist are pruned right here — keeps the cache crisp without
      //    a separate sweep job.
      if (ui.autoSearchLayer1) {
        const candidates = lookupCachedCandidates(ctx, file.name)
        const verified: string[] = []
        for (const candidate of candidates) {
          try {
            const stat = await fs.stat(candidate)
            if (stat) {
              verified.push(candidate)
            } else {
              pruneCandidate(ctx, candidate)
            }
          } catch (err) {
            if (!isNotFoundError(err)) throw err
            pruneCandidate(ctx, candidate)
          }
        }
        if (verified.length === 1) {
          deps.tabOpener({ ...file, path: verified[0] }, source, ctx)
          return
        }
        if (verified.length > 1) {
          deps.popupController.show({
            mode: 'layer1-multi',
            hits: verified,
            file,
            source,
            ctx,
          })
          return
        }
      }

      // 4. Fall through to ask-expand (Layer 2/3 picked up by 5.8).
      deps.popupController.show({ mode: 'ask-expand', file, source, ctx })
    },
  }
}
