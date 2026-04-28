import type { LinkOpener } from '../types'
import type { FileInfo, FileSource } from '../../../types/fs'
import { resolveEditorHomePath } from '../../editor-home-resolver'
import { FileNotFoundError, type OpenFileContext } from '../../file-open'

export interface FilePathOpenerDeps {
  /**
   * Open the file via the P5 tryOpenFile pipeline (stat → cache → popup).
   * The caller MUST construct the service at bootstrap (see
   * `register-modules/index.tsx`) so the host-bound backend, popup
   * controller, and tab opener are pre-wired.
   */
  tryOpenFile(file: FileInfo, source: FileSource, ctx: OpenFileContext): Promise<void>
  /** Active workspace ID — used as a fallback when ctx.workspaceId is unset. */
  getActiveWorkspaceId(): string | null
  /** Resolve the cwd of a tmux pane (relative-path expansion). */
  fetchPaneCwd(hostId: string, sessionCode: string, signal?: AbortSignal): Promise<string>
  /** Resolve `~` for tilde-prefixed paths. */
  fetchPaneHome(hostId: string, sessionCode: string, signal?: AbortSignal): Promise<string>
  /**
   * Resolve the cwd for the open-file context — usually the active session's
   * cwd. Used as the path-cache scope key (per-host, per-cwd). When unset
   * (no active session), the resolver may return null and the cwd field is
   * filled from a sensible fallback (workspace projectPath / home).
   */
  resolveOpenContextCwd(hostId: string, sessionCode?: string): string | null
}

function buildFileInfo(path: string): FileInfo {
  const name = path.split('/').pop() ?? path
  const extension = name.includes('.') ? name.split('.').pop()! : ''
  return { name, path, extension, size: 0, isDirectory: false }
}

// Collapse `.` and `..` segments in an absolute path. Used by the tilde
// branch so that `~/./foo` and `~/../foo` resolve to single canonical paths,
// avoiding duplicate editor tabs for the same physical file.
function normalizeAbsPath(abs: string): string {
  const parts = abs.split('/')
  const stack: string[] = []
  for (const part of parts) {
    if (part === '' || part === '.') continue
    if (part === '..') {
      if (stack.length > 0) stack.pop()
      continue
    }
    stack.push(part)
  }
  return '/' + stack.join('/')
}

/**
 * Resolve a relative path against `cwd`, normalizing `.` and `..` segments.
 * Returns `null` if the resolved path escapes `cwd`'s parent directory
 * (i.e. moves more than one level above `cwd`), so the caller can reject
 * path-traversal attempts like `../../../etc/passwd`.
 *
 * Design: allow at most one `..` out of the fetched cwd (common in relative
 * editor paths like `../App.tsx`), but reject deep traversals that escape
 * the cwd's parent entirely.
 */
export function resolveCwdPath(cwd: string, rel: string): string | null {
  const base = cwd.replace(/\/+$/, '')  // strip trailing slashes
  const joined = `${base}/${rel}`
  const parts = joined.split('/')
  const stack: string[] = []
  for (const part of parts) {
    if (part === '' || part === '.') continue
    if (part === '..') {
      if (stack.length > 0) stack.pop()
      continue
    }
    stack.push(part)
  }
  const normalized = '/' + stack.join('/')

  // Boundary: normalized must be within cwd's parent directory.
  // This allows one level of ".." escape (e.g. ../App.tsx from a subdir)
  // but rejects deep traversals that land outside the parent tree.
  const parentIdx = base.lastIndexOf('/')
  // parentIdx === 0 means cwd is /foo — parent is root "/"
  // parentIdx < 0 should not happen since cwd must start with /
  const baseParent = parentIdx <= 0 ? '' : base.slice(0, parentIdx)
  // normalized must equal baseParent, or be nested under it
  if (baseParent === '') {
    // parent is root: any absolute path is within root
    return normalized
  }
  if (normalized !== baseParent && !normalized.startsWith(baseParent + '/')) {
    return null
  }
  return normalized
}

export function createFilePathOpener(deps: FilePathOpenerDeps): LinkOpener {
  return {
    id: 'builtin:file-path',
    priority: 0,
    canOpen: (token) =>
      token.type === 'file' &&
      typeof (token.meta as { path?: unknown } | undefined)?.path === 'string',
    open: async (token, ctx) => {
      const rawPath = (token.meta as { path?: unknown } | undefined)?.path
      if (typeof rawPath !== 'string') return
      if (!ctx.hostId) return

      // R2 + R3 codex: snapshot the link-source workspace from LinkContext
      // before any await so workspace-owned panes preserve isolation across
      // a mid-flight switch. Standalone panes (ctx.workspaceId undefined)
      // legitimately follow the user's current focus, so we *defer* the
      // active-workspace read until after the async resolve below.
      const sourceWorkspaceId = ctx.workspaceId

      let path = rawPath
      if (path.startsWith('~/')) {
        if (!ctx.sessionCode) return
        const controller = new AbortController()
        const timeoutId = setTimeout(() => controller.abort(), 5000)
        let home: string | null = null
        try {
          home = await resolveEditorHomePath(
            {
              hostId: ctx.hostId,
              sessionCode: ctx.sessionCode,
              workspaceId: ctx.workspaceId,
              signal: controller.signal,
            },
            { fetchPaneHome: deps.fetchPaneHome },
          )
        } catch (err) {
          const reason = (err as Error)?.name === 'AbortError' ? 'timeout' : (err as Error)?.message ?? String(err)
          console.warn(`[file-path] home resolve failed (${reason}) for host=${ctx.hostId} session=${ctx.sessionCode} path=${rawPath}; opening as new buffer`)
        } finally {
          clearTimeout(timeoutId)
        }
        if (home && home.startsWith('/')) {
          path = normalizeAbsPath(home.replace(/\/+$/, '') + path.slice(1))
        }
        // else: leave path as rawPath (~/foo) so Editor opens a blank buffer
      } else if (!path.startsWith('/')) {
        // relative / bare: 即時向 tmux pane 查 cwd，normalize 後驗證不越界
        if (!ctx.sessionCode) return
        const controller = new AbortController()
        const timeoutId = setTimeout(() => controller.abort(), 5000)
        let cwd: string
        try {
          cwd = await deps.fetchPaneCwd(ctx.hostId, ctx.sessionCode, controller.signal)
        } catch (err) {
          clearTimeout(timeoutId)
          const reason = (err as Error)?.name === 'AbortError' ? 'timeout' : (err as Error)?.message ?? String(err)
          console.warn(`[file-path] cwd fetch failed (${reason}) for host=${ctx.hostId} session=${ctx.sessionCode} rel=${rawPath}`)
          return
        }
        clearTimeout(timeoutId)
        if (!cwd || !cwd.startsWith('/')) {
          console.warn(`[file-path] cwd not absolute, skipping: host=${ctx.hostId} cwd=${cwd}`)
          return
        }
        const resolved = resolveCwdPath(cwd, path)
        if (resolved === null) {
          console.warn(`[file-path] rejected path escaping cwd: ${path} vs ${cwd}`)
          return
        }
        path = resolved
      }

      const file = buildFileInfo(path)
      const source: FileSource = { type: 'daemon', hostId: ctx.hostId }
      const targetWorkspaceId = sourceWorkspaceId ?? deps.getActiveWorkspaceId()
      if (!targetWorkspaceId) return

      // P5: route through the openFile pipeline so missing files surface the
      // FileNotFoundPopup instead of opening empty buffers. The pipeline's
      // tabOpener (wired at bootstrap) does the same getDefaultOpener +
      // openSingletonTab + insertTab work the previous direct path did.
      // ctx.cwd is the path-cache scope key — best-effort: session cwd when
      // available, otherwise the resolved file's directory.
      const cacheCwdResolved = deps.resolveOpenContextCwd(ctx.hostId, ctx.sessionCode)
      const fallbackCwd = path.replace(/\/[^/]*$/, '') || '/'
      const cacheCwd = cacheCwdResolved ?? fallbackCwd
      const openCtx: OpenFileContext = {
        hostId: ctx.hostId,
        cwd: cacheCwd,
        sourceWorkspaceId: targetWorkspaceId,
        sessionCode: ctx.sessionCode,
      }
      try {
        await deps.tryOpenFile(file, source, openCtx)
      } catch (err) {
        // Click-handler context: we can't propagate the rejection up the stack,
        // so distinguish for the operator:
        // - FileNotFoundError → expected when `popupOnMissingFile` is off; warn.
        // - Anything else (auth/network/host removed) → unexpected; log loud
        //   so console-watching surfaces the broken connection / token issue
        //   instead of a quiet "tryOpenFile failed" line that reads like a
        //   harmless missing file.
        if (err instanceof FileNotFoundError) {
          console.warn(`[file-path] file not found (popup disabled): ${file.path}`)
        } else {
          console.error(
            `[file-path] tryOpenFile failed for ${file.path}: ${(err as Error)?.message ?? String(err)}`,
            err,
          )
        }
      }
    },
  }
}
